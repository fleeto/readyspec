package cli_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/cli"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/topology"
	"github.com/fleeto/readyspec/internal/workspace"
)

func TestCleanupResolveIgnoreIsAuditedAndHiddenFromListOutput(t *testing.T) {
	const source = `schema_version: readyspec.plan/v1
plan_id: cleanup-cli-test
name: Cleanup CLI test
software_name: fixture
software_version: '1'
version: '1.0.0'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: 1, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`
	p, err := plan.Load(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	hosts := []inventory.Host{{ID: "node", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	cfg := runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "TRIAL", Scope: "FULL", Backend: "native_ssh",
		Inventory: hosts, RoleMap: map[string][]string{"app": {"node"}},
		Auth:    runstore.AuthIdentity{Method: "password"},
		Budgets: runstore.Budgets{QueueSeconds: 1800, RunSeconds: 3600, HostSeconds: 600},
	}
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "cleanup-cli", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	const ownerToken = "0123456789abcdef0123456789abcdef"
	item := report.CleanupItem{CleanupID: "cleanup-cli-item", HostID: "node", MachineID: "machine", BootID: "boot", Status: "PENDING", RemotePath: "/tmp/readyspec-cli-cleanup", OwnerToken: ownerToken}
	if err := store.CleanupRegistered(nil, run.Acceptance.RunID, item); err != nil {
		t.Fatal(err)
	}
	now := run.Acceptance.AcceptedAt
	archived := report.RunReport{
		SchemaVersion: "readyspec.report/v1", RunID: run.Acceptance.RunID, PlanDigest: p.Digest,
		RunMode: cfg.Mode, RunScope: cfg.Scope, ExecutorBackend: cfg.Backend,
		RunStatus: "FAILED", Verdict: "UNKNOWN", TerminationReason: "CONTROLLER_INTERRUPTED",
		VerdictReasons: []string{"CONTROLLER_INTERRUPTED"}, StartedAt: now, FinishedAt: time.Now().UTC(),
		TopologyResults: []topology.Result{}, Hosts: []report.HostResult{}, CleanupItems: []report.CleanupItem{item},
		Failures: []report.Failure{},
	}
	if err := store.Finalize(run.Acceptance.RunID, archived); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	code := cli.Execute([]string{
		"--workspace", dir, "cleanup", "resolve", "--run-id", run.Acceptance.RunID,
		"--cleanup-id", item.CleanupID, "--ignore-reason", "operator confirmed no cleanup is required",
	}, &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), `"status":"IGNORED"`) || strings.Contains(out.String(), ownerToken) {
		t.Fatalf("cleanup resolve: code=%d out=%s stderr=%s", code, out.String(), stderr.String())
	}
	out.Reset()
	stderr.Reset()
	code = cli.Execute([]string{"--workspace", dir, "cleanup", "list", "--json"}, &out, &stderr)
	if code != 0 || strings.Contains(out.String(), item.CleanupID) || strings.Contains(out.String(), ownerToken) {
		t.Fatalf("cleanup list: code=%d out=%s stderr=%s", code, out.String(), stderr.String())
	}

	ws, err = workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	lease, err = ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	store, err = runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	resolved := store.CleanupItems(run.Acceptance.RunID)
	if len(resolved) != 1 || resolved[0].Status != "IGNORED" || resolved[0].AuditReason != "operator confirmed no cleanup is required" {
		t.Fatalf("cleanup audit was not persisted: %+v", resolved)
	}
}
