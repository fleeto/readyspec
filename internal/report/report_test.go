package report_test

import (
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/topology"
)

func validReport() report.RunReport {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	return report.RunReport{
		SchemaVersion: "readyspec.report/v1", RunID: "run-test",
		PlanDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		RunMode:    "GATE", RunScope: "FULL", ExecutorBackend: "native_ssh",
		RunStatus: "COMPLETED", Verdict: "READY", VerdictReasons: []string{},
		StartedAt: now, FinishedAt: now.Add(time.Second),
		TopologyResults: []topology.Result{}, Hosts: []report.HostResult{},
		CleanupItems: []report.CleanupItem{}, Failures: []report.Failure{},
	}
}

func TestValidateRunReportRequiresValidSchemaAndCleanupAudit(t *testing.T) {
	value := validReport()
	value.Hosts = []report.HostResult{{
		HostID: "node-1", Status: "COMPLETED", Roles: []string{"app"},
		ItemResults: []compiler.ItemResult{{
			RoleID: "app", ItemID: "disk", Status: "PASS", TemplateID: "S01", TemplateVersion: "1.0.0",
			ExecutionMode: "STANDARD", EvidenceMode: "EXISTENCE",
			ParameterSources: map[string]string{"path": "host", "filetype": "template"},
		}}, Failures: []report.Failure{},
	}}
	if err := report.Validate(value); err != nil {
		t.Fatalf("valid report with parameter provenance rejected: %v", err)
	}
	value.CleanupItems = []report.CleanupItem{{CleanupID: "cleanup-1", HostID: "node-1", Status: "IGNORED"}}
	if err := report.Validate(value); err == nil {
		t.Fatal("accepted IGNORED cleanup without audit reason")
	}
	value.CleanupItems[0].AuditReason = "operator confirmed no cleanup needed"
	if err := report.Validate(value); err != nil {
		t.Fatal(err)
	}
}

func TestExitCodeUsesSPECPrecedence(t *testing.T) {
	value := validReport()
	cases := []struct {
		name  string
		input report.ExitConditions
		want  int
	}{
		{"auth precedes busy", report.ExitConditions{AuthError: true, WorkspaceBusy: true}, 15},
		{"busy precedes plan", report.ExitConditions{WorkspaceBusy: true, PlanError: true}, 16},
		{"plan precedes infrastructure", report.ExitConditions{PlanError: true, InfraError: true}, 12},
		{"infrastructure precedes cancellation", report.ExitConditions{InfraError: true, Report: &value}, 13},
		{"cancel precedes not ready", report.ExitConditions{Report: &report.RunReport{RunStatus: "CANCELLED", Verdict: "NOT_READY"}}, 14},
		{"not ready", report.ExitConditions{Report: &report.RunReport{RunStatus: "PARTIAL", Verdict: "NOT_READY"}}, 10},
		{"unknown", report.ExitConditions{Report: &report.RunReport{RunStatus: "FAILED", Verdict: "UNKNOWN"}}, 11},
		{"ready", report.ExitConditions{Report: &value}, 0},
		{"missing report", report.ExitConditions{}, 13},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := report.ExitCode(tt.input); got != tt.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEvaluateVerdictMapsRequiredEvidenceAndRunEligibility(t *testing.T) {
	basePlan := &plan.Plan{
		State: "RELEASED",
		Roles: []plan.Role{{RoleID: "app", Items: []plan.CheckItem{{ItemID: "required-check", Enabled: true, Importance: "REQUIRED"}}}},
	}
	baseHost := report.HostResult{
		HostID: "node-a", Status: "COMPLETED", Roles: []string{"app"},
		ItemResults: []compiler.ItemResult{{RoleID: "app", ItemID: "required-check", Status: "PASS"}},
	}
	baseTopology := []topology.Result{{Code: "ARCH", HostID: "node-a", Status: "PASS"}}
	tests := []struct {
		name      string
		planState string
		mode      string
		scope     string
		executor  string
		status    string
		hosts     []report.HostResult
		topology  []topology.Result
		want      string
	}{
		{name: "all required evidence passes", planState: "RELEASED", mode: "GATE", scope: "FULL", executor: "native_ssh", status: "COMPLETED", hosts: []report.HostResult{baseHost}, topology: baseTopology, want: "READY"},
		{name: "required failure is not ready", planState: "RELEASED", mode: "GATE", scope: "FULL", executor: "native_ssh", status: "COMPLETED", hosts: []report.HostResult{{HostID: "node-a", Roles: []string{"app"}, ItemResults: []compiler.ItemResult{{RoleID: "app", ItemID: "required-check", Status: "FAIL"}}}}, topology: baseTopology, want: "NOT_READY"},
		{name: "topology failure outranks incomplete eligibility", planState: "DRAFT", mode: "TRIAL", scope: "PARTIAL", executor: "mock_simulator", status: "FAILED", hosts: []report.HostResult{baseHost}, topology: []topology.Result{{Code: "ROLE_COUNT", Status: "FAIL"}}, want: "NOT_READY"},
		{name: "required error is unknown", planState: "RELEASED", mode: "GATE", scope: "FULL", executor: "native_ssh", status: "COMPLETED", hosts: []report.HostResult{{HostID: "node-a", Roles: []string{"app"}, ItemResults: []compiler.ItemResult{{RoleID: "app", ItemID: "required-check", Status: "ERROR"}}}}, topology: baseTopology, want: "UNKNOWN"},
		{name: "missing required observation is unknown", planState: "RELEASED", mode: "GATE", scope: "FULL", executor: "native_ssh", status: "COMPLETED", hosts: []report.HostResult{{HostID: "node-a", Roles: []string{"app"}}}, topology: baseTopology, want: "UNKNOWN"},
		{name: "duplicate observation is unknown", planState: "RELEASED", mode: "GATE", scope: "FULL", executor: "native_ssh", status: "COMPLETED", hosts: []report.HostResult{{HostID: "node-a", Roles: []string{"app"}, ItemResults: []compiler.ItemResult{{RoleID: "app", ItemID: "required-check", Status: "PASS"}, {RoleID: "app", ItemID: "required-check", Status: "PASS"}}}}, topology: baseTopology, want: "UNKNOWN"},
		{name: "simulator cannot be ready", planState: "RELEASED", mode: "GATE", scope: "FULL", executor: "mock_simulator", status: "COMPLETED", hosts: []report.HostResult{baseHost}, topology: baseTopology, want: "UNKNOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := *basePlan
			p.State = tt.planState
			got := report.EvaluateVerdict(&p, tt.mode, tt.scope, tt.executor, tt.status, tt.hosts, tt.topology)
			if got != tt.want {
				t.Fatalf("EvaluateVerdict()=%q want=%q", got, tt.want)
			}
		})
	}
}
