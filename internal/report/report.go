// Package report defines and validates the authoritative archived Run result.
package report

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/topology"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/report.v1.json
var schemaFiles embed.FS

type Failure struct {
	Code    string `json:"code"`
	Phase   string `json:"phase"`
	HostID  string `json:"host_id,omitempty"`
	Message string `json:"message"`
}

type HostResult struct {
	HostID            string                `json:"host_id"`
	Status            string                `json:"status"`
	MachineID         string                `json:"machine_id,omitempty"`
	BootID            string                `json:"boot_id,omitempty"`
	HostKeyFP         string                `json:"ssh_host_key_fp,omitempty"`
	Arch              string                `json:"arch,omitempty"`
	LoginUID          *int                  `json:"login_uid,omitempty"`
	HighRiskRootAudit bool                  `json:"high_risk_root_audit,omitempty"`
	Roles             []string              `json:"roles"`
	ItemResults       []compiler.ItemResult `json:"item_results"`
	Failures          []Failure             `json:"failures"`
}

type CleanupItem struct {
	CleanupID      string `json:"cleanup_id"`
	HostID         string `json:"host_id"`
	HostKeyFP      string `json:"ssh_host_key_fp,omitempty"`
	MachineID      string `json:"machine_id,omitempty"`
	BootID         string `json:"boot_id,omitempty"`
	Status         string `json:"status"`
	RemotePath     string `json:"remote_path,omitempty"`
	PrivilegedPath string `json:"privileged_path,omitempty"`
	OwnerToken     string `json:"owner_token,omitempty"`
	Reason         string `json:"reason,omitempty"`
	AuditReason    string `json:"audit_reason,omitempty"`
}

type RunReport struct {
	SchemaVersion     string            `json:"schema_version"`
	RunID             string            `json:"run_id"`
	PlanDigest        string            `json:"plan_digest"`
	RunMode           string            `json:"run_mode"`
	RunScope          string            `json:"run_scope"`
	ExecutorBackend   string            `json:"executor_backend"`
	AuthMethod        string            `json:"auth_method,omitempty"`
	RunStatus         string            `json:"run_status"`
	Verdict           string            `json:"verdict"`
	TerminationReason string            `json:"termination_reason,omitempty"`
	VerdictReasons    []string          `json:"verdict_reasons"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        time.Time         `json:"finished_at"`
	TopologyResults   []topology.Result `json:"topology_results"`
	Hosts             []HostResult      `json:"hosts"`
	CleanupItems      []CleanupItem     `json:"cleanup_items"`
	Failures          []Failure         `json:"failures"`
}

var compiled = sync.OnceValues(func() (*jsonschema.Schema, error) {
	raw, err := schemaFiles.ReadFile("schemas/report.v1.json")
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(offlineLoader{})
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	if err := compiler.AddResource("https://readyspec.dev/schemas/report.v1.json", doc); err != nil {
		return nil, err
	}
	return compiler.Compile("https://readyspec.dev/schemas/report.v1.json")
})

func Validate(value RunReport) error {
	if value.SchemaVersion != "readyspec.report/v1" {
		return fmt.Errorf("REPORT_ERROR: unsupported schema version")
	}
	if value.RunID == "" || value.StartedAt.IsZero() || value.FinishedAt.IsZero() || value.FinishedAt.Before(value.StartedAt) {
		return fmt.Errorf("REPORT_ERROR: invalid run identity or timestamps")
	}
	if value.RunStatus == "CANCELLED" && value.TerminationReason == "" {
		return fmt.Errorf("REPORT_ERROR: cancelled report requires a termination reason")
	}
	if value.RunStatus == "FAILED" && value.Verdict == "READY" {
		return fmt.Errorf("REPORT_ERROR: failed run cannot be ready")
	}
	seenHosts := make(map[string]bool, len(value.Hosts))
	for _, host := range value.Hosts {
		if host.HostID == "" || seenHosts[host.HostID] {
			return fmt.Errorf("REPORT_ERROR: duplicate or empty host id")
		}
		seenHosts[host.HostID] = true
	}
	for _, cleanup := range value.CleanupItems {
		if cleanup.Status == "IGNORED" && strings.TrimSpace(cleanup.AuditReason) == "" {
			return fmt.Errorf("REPORT_ERROR: ignored cleanup requires an audit reason")
		}
	}
	schema, err := compiled()
	if err != nil {
		return fmt.Errorf("INFRA_ERROR: compile report schema: %w", err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("REPORT_ERROR: encode report")
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return fmt.Errorf("REPORT_ERROR: decode report")
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("REPORT_ERROR: document does not match readyspec.report/v1")
	}
	return nil
}

func Decode(raw []byte) (RunReport, error) {
	var value RunReport
	if err := json.Unmarshal(raw, &value); err != nil {
		return RunReport{}, fmt.Errorf("REPORT_ERROR: invalid JSON")
	}
	if err := Validate(value); err != nil {
		return RunReport{}, err
	}
	return value, nil
}

type ExitConditions struct {
	AuthError     bool
	WorkspaceBusy bool
	PlanError     bool
	InfraError    bool
	Report        *RunReport
}

// ExitCode applies SPEC §10.3's ordered mapping.
func ExitCode(input ExitConditions) int {
	switch {
	case input.AuthError:
		return 15
	case input.WorkspaceBusy:
		return 16
	case input.PlanError:
		return 12
	case input.InfraError || input.Report == nil:
		return 13
	case input.Report.RunStatus == "CANCELLED":
		return 14
	case input.Report.Verdict == "NOT_READY":
		return 10
	case input.Report.Verdict == "UNKNOWN":
		return 11
	case input.Report.Verdict == "READY":
		return 0
	default:
		return 13
	}
}

// EvaluateVerdict applies the domain ordering to report observations. Missing
// required observations create evidence uncertainty; they never imply PASS.
func EvaluateVerdict(p *plan.Plan, mode, scope, executor, status string, hosts []HostResult, topologyResults []topology.Result) string {
	if p == nil {
		return "UNKNOWN"
	}
	requiredItems := map[[2]string]bool{}
	for _, role := range p.Roles {
		for _, item := range role.Items {
			if item.Enabled && item.Importance == "REQUIRED" {
				requiredItems[[2]string{role.RoleID, item.ItemID}] = true
			}
		}
	}
	requiredStates := make([]string, 0, len(requiredItems))
	seen := map[[3]string]string{}
	for _, host := range hosts {
		for _, item := range host.ItemResults {
			key := [3]string{host.HostID, item.RoleID, item.ItemID}
			if _, duplicate := seen[key]; duplicate {
				seen[key] = "ERROR"
			} else {
				seen[key] = item.Status
			}
		}
	}
	for _, host := range hosts {
		for _, role := range host.Roles {
			for key := range requiredItems {
				if key[0] == role {
					state := seen[[3]string{host.HostID, role, key[1]}]
					if state == "" {
						state = "ERROR"
					}
					requiredStates = append(requiredStates, state)
				}
			}
		}
	}
	topologyStates := make([]string, 0, len(topologyResults))
	for _, result := range topologyResults {
		topologyStates = append(topologyStates, result.Status)
	}
	return topology.Verdict(topology.VerdictInput{
		PlanState: p.State, Mode: mode, Scope: scope, Executor: executor, Status: status,
		Required: requiredStates, Topology: topologyStates,
	})
}

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) {
	return nil, fs.ErrNotExist
}
