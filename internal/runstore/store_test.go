package runstore_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/topology"
	"github.com/fleeto/readyspec/internal/workspace"
)

const draftPlan = `schema_version: readyspec.plan/v1
plan_id: runstore-test
name: Runstore test
description: ""
software_name: demo
software_version: '1'
version: '1.0.0'
state: DRAFT
topology_policy:
  allowed_arch: [x86_64]
roles:
  - role_id: app
    name: App
    min_nodes: 1
    max_nodes: 3
    allow_colocation: false
    items: []
embedded_templates: []
template_lock: {}
`

func newHarness(t *testing.T) (string, *workspace.Store, *workspace.Lease, *runstore.Store, *plan.Plan, runstore.RunConfig) {
	t.Helper()
	dir := t.TempDir()
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.Load(strings.NewReader(draftPlan))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	cfg := runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "TRIAL", Scope: "PARTIAL", Backend: "mock_simulator",
		Inventory: []inventory.Host{{ID: "node-1", Address: "127.0.0.1", Port: 22, Username: "tester"}},
		RoleMap:   map[string][]string{"app": {"node-1"}}, Auth: runstore.AuthIdentity{Method: "password"},
		Budgets: runstore.Budgets{QueueSeconds: 1800, RunSeconds: 3600, HostSeconds: 600},
	}
	return dir, ws, lease, rs, p, cfg
}

func TestAcceptIdempotencyAndConflict(t *testing.T) {
	_, ws, lease, store, p, cfg := newHarness(t)
	defer ws.Close()
	defer lease.Close()
	first, created, err := store.Accept(runstore.Request{IdempotencyKey: "same", Plan: p, Config: cfg})
	if err != nil || !created {
		t.Fatalf("first Accept: created=%v err=%v", created, err)
	}
	second, created, err := store.Accept(runstore.Request{IdempotencyKey: "same", Plan: p, Config: cfg})
	if err != nil || created || second.Acceptance.RunID != first.Acceptance.RunID {
		t.Fatalf("duplicate Accept: created=%v run=%v err=%v", created, second, err)
	}
	changed := cfg
	changed.Scope = "FULL"
	if _, _, err := store.Accept(runstore.Request{IdempotencyKey: "same", Plan: p, Config: changed}); !errors.Is(err, runstore.ErrIdempotencyConflict) {
		t.Fatalf("changed duplicate error = %v", err)
	}
}

func TestSameKeyConcurrentAcceptCreatesOneRun(t *testing.T) {
	_, ws, lease, store, p, cfg := newHarness(t)
	defer ws.Close()
	defer lease.Close()
	const count = 24
	type result struct {
		id      string
		created bool
		err     error
	}
	results := make(chan result, count)
	for i := 0; i < count; i++ {
		go func() {
			run, created, err := store.Accept(runstore.Request{IdempotencyKey: "concurrent", Plan: p, Config: cfg})
			if run == nil {
				results <- result{created: created, err: err}
				return
			}
			results <- result{id: run.Acceptance.RunID, created: created, err: err}
		}()
	}
	wantID := ""
	createdCount := 0
	for i := 0; i < count; i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if wantID == "" {
			wantID = got.id
		}
		if got.id != wantID {
			t.Fatalf("concurrent submission returned multiple IDs: %s and %s", wantID, got.id)
		}
		if got.created {
			createdCount++
		}
	}
	if createdCount != 1 || len(store.List()) != 1 {
		t.Fatalf("created=%d persisted runs=%d", createdCount, len(store.List()))
	}
}

func TestQueueCapacityIsTenWaitingRuns(t *testing.T) {
	_, ws, lease, store, p, cfg := newHarness(t)
	defer ws.Close()
	defer lease.Close()
	for i := 0; i < 10; i++ {
		if _, _, err := store.Accept(runstore.Request{IdempotencyKey: string(rune('a' + i)), Plan: p, Config: cfg}); err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
	}
	if _, _, err := store.Accept(runstore.Request{IdempotencyKey: "overflow", Plan: p, Config: cfg}); !errors.Is(err, runstore.ErrQueueFull) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestCrashRecoveryArchivesAcceptedRunWithoutRerun(t *testing.T) {
	dir, ws, lease, store, p, cfg := newHarness(t)
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "crash", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ExecutionStarted(context.Background(), run.Acceptance.RunID, "node-a", "STANDARD"); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ws.Close(); err != nil {
		t.Fatal(err)
	}
	ws2, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	lease2, err := ws2.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease2.Close()
	recovered, err := runstore.Open(ws2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recovered.Get(run.Acceptance.RunID)
	if err != nil || got.Report == nil || got.Status != "FAILED" || got.Report.TerminationReason != "CONTROLLER_INTERRUPTED" {
		t.Fatalf("recovered run=%+v err=%v", got, err)
	}
	if got.Plan.Digest != p.Digest || got.Plan.SourceDocument["description"] != "" {
		t.Fatalf("recovery did not retain the digest-bound plan document: digest=%s document=%#v", got.Plan.Digest, got.Plan.SourceDocument)
	}
	if _, ok, err := recovered.EnqueueNext(); err != nil || ok {
		t.Fatalf("recovered accepted run was requeued: dispatched=%v err=%v", ok, err)
	}
	for _, event := range got.Events {
		if event.Kind == "EXECUTION_STARTED" && event.HostID == "node-a" && event.Mode == "STANDARD" {
			return
		}
	}
	t.Fatal("recovery lost the durable execution marker")
}

func TestCrashRecoverySubprocessHelper(t *testing.T) {
	dir := os.Getenv("READYSPEC_CRASH_TEST_ROOT")
	if dir == "" {
		return
	}
	ws, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.Load(strings.NewReader(draftPlan))
	if err != nil {
		t.Fatal(err)
	}
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	cfg := runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "TRIAL", Scope: "PARTIAL", Backend: "mock_simulator",
		Inventory: []inventory.Host{{ID: "node-1", Address: "127.0.0.1", Port: 22, Username: "tester"}},
		RoleMap:   map[string][]string{"app": {"node-1"}}, Auth: runstore.AuthIdentity{Method: "password"},
		Budgets: runstore.Budgets{QueueSeconds: 1800, RunSeconds: 3600, HostSeconds: 600},
	}
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "subprocess-crash", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	action := os.Getenv("READYSPEC_CRASH_TEST_ACTION")
	switch action {
	case "after_accept":
	case "after_marker":
		if err := store.ExecutionStarted(context.Background(), run.Acceptance.RunID, "node-1", "STANDARD"); err != nil {
			t.Fatal(err)
		}
	case "after_cancel":
		if _, err := store.Cancel(run.Acceptance.RunID); err != nil {
			t.Fatal(err)
		}
	case "after_report":
		value := report.RunReport{
			SchemaVersion: "readyspec.report/v1", RunID: run.Acceptance.RunID, PlanDigest: p.Digest,
			RunMode: cfg.Mode, RunScope: cfg.Scope, ExecutorBackend: cfg.Backend,
			RunStatus: "CANCELLED", Verdict: "UNKNOWN", TerminationReason: "CANCEL_ACCEPTED",
			VerdictReasons: []string{"CANCEL_ACCEPTED"}, StartedAt: run.Acceptance.AcceptedAt, FinishedAt: time.Now().UTC(),
			TopologyResults: []topology.Result{}, Hosts: []report.HostResult{}, CleanupItems: []report.CleanupItem{},
			Failures: []report.Failure{},
		}
		if err := ws.WriteJSON("runs/"+run.Acceptance.RunID+"/report.json", value); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown subprocess crash action %q", action)
	}
	_ = lease
	os.Exit(0)
}

func TestCrashRecoveryAcrossSubprocessExitWindows(t *testing.T) {
	for _, tc := range []struct {
		action     string
		wantStatus string
		wantReason string
		wantMarker bool
	}{
		{action: "after_accept", wantStatus: "FAILED", wantReason: "CONTROLLER_INTERRUPTED"},
		{action: "after_marker", wantStatus: "FAILED", wantReason: "CONTROLLER_INTERRUPTED", wantMarker: true},
		{action: "after_cancel", wantStatus: "CANCELLED", wantReason: "CANCEL_ACCEPTED"},
		{action: "after_report", wantStatus: "CANCELLED", wantReason: "CANCEL_ACCEPTED"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoverySubprocessHelper$")
			cmd.Env = append(os.Environ(), "READYSPEC_CRASH_TEST_ROOT="+dir, "READYSPEC_CRASH_TEST_ACTION="+tc.action)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("crash helper failed: %v\n%s", err, output)
			}
			ws, err := workspace.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			lease, err := ws.TryLock()
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			recovered, err := runstore.Open(ws)
			if err != nil {
				t.Fatal(err)
			}
			runs := recovered.List()
			if len(runs) != 1 {
				t.Fatalf("recovered %d Runs, want 1", len(runs))
			}
			got := runs[0]
			if got.Report == nil || got.Report.RunStatus != tc.wantStatus || got.Report.TerminationReason != tc.wantReason {
				t.Fatalf("recovered report=%+v want status/reason=%s/%s", got.Report, tc.wantStatus, tc.wantReason)
			}
			foundMarker := false
			for _, event := range got.Events {
				if event.Kind == "EXECUTION_STARTED" && event.HostID == "node-1" && event.Mode == "STANDARD" {
					foundMarker = true
				}
			}
			if foundMarker != tc.wantMarker {
				t.Fatalf("marker recovered=%v want=%v", foundMarker, tc.wantMarker)
			}
			if _, ok, err := recovered.EnqueueNext(); err != nil || ok {
				t.Fatalf("recovered Run was dispatched: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestDurableCancelRecoversAsCancelled(t *testing.T) {
	dir, ws, lease, store, p, cfg := newHarness(t)
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "cancel", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cancel(run.Acceptance.RunID); err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	_ = ws.Close()
	ws2, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	lease2, err := ws2.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease2.Close()
	recovered, err := runstore.Open(ws2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recovered.Get(run.Acceptance.RunID)
	if err != nil || got.Report == nil || got.Status != "CANCELLED" || report.ExitCode(report.ExitConditions{Report: got.Report}) != 14 {
		t.Fatalf("cancel recovery=%+v err=%v", got, err)
	}
}

func TestRepeatedCancellationWhileCancellingDoesNotAppendDuplicateAcceptance(t *testing.T) {
	_, _, _, store, p, cfg := newHarness(t)
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "cancel-idempotency", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	const callers = 16
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Cancel(run.Acceptance.RunID)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.Get(run.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	cancellations := 0
	for _, event := range got.Events {
		if event.Kind == "CANCEL_ACCEPTED" {
			cancellations++
		}
	}
	if cancellations != 1 {
		t.Fatalf("journal contains %d cancellation acceptance events, want 1", cancellations)
	}
}

func TestCommittedReportWinsRecoveryWindow(t *testing.T) {
	dir, ws, lease, store, p, cfg := newHarness(t)
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "report-window", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	started := run.Acceptance.AcceptedAt
	value := report.RunReport{
		SchemaVersion: "readyspec.report/v1", RunID: run.Acceptance.RunID, PlanDigest: p.Digest,
		RunMode: cfg.Mode, RunScope: cfg.Scope, ExecutorBackend: cfg.Backend,
		RunStatus: "CANCELLED", Verdict: "UNKNOWN", TerminationReason: "CANCEL_ACCEPTED",
		VerdictReasons: []string{"CANCEL_ACCEPTED"}, StartedAt: started, FinishedAt: time.Now().UTC(),
		TopologyResults: []topology.Result{}, Hosts: []report.HostResult{}, CleanupItems: []report.CleanupItem{},
		Failures: []report.Failure{},
	}
	if err := ws.WriteJSON("runs/"+run.Acceptance.RunID+"/report.json", value); err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	_ = ws.Close()
	ws2, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	lease2, err := ws2.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease2.Close()
	recovered, err := runstore.Open(ws2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := recovered.Get(run.Acceptance.RunID)
	if err != nil || got.Report == nil || got.Report.TerminationReason != "CANCEL_ACCEPTED" {
		t.Fatalf("committed report was not authoritative: %+v err=%v", got, err)
	}
}

func TestReportArchiveFailureDoesNotCommitTerminalState(t *testing.T) {
	dir, _, _, store, p, cfg := newHarness(t)
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "archive-failure", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "runs", run.Acceptance.RunID, "report.json"), 0700); err != nil {
		t.Fatal(err)
	}
	value := report.RunReport{
		SchemaVersion: "readyspec.report/v1", RunID: run.Acceptance.RunID, PlanDigest: p.Digest,
		RunMode: cfg.Mode, RunScope: cfg.Scope, ExecutorBackend: cfg.Backend,
		RunStatus: "FAILED", Verdict: "UNKNOWN", TerminationReason: "EXECUTION_INTERRUPTED",
		VerdictReasons: []string{}, StartedAt: run.Acceptance.AcceptedAt, FinishedAt: time.Now().UTC(),
		TopologyResults: []topology.Result{}, Hosts: []report.HostResult{}, CleanupItems: []report.CleanupItem{},
		Failures: []report.Failure{},
	}
	if err := store.Finalize(run.Acceptance.RunID, value); err == nil {
		t.Fatal("report archive unexpectedly succeeded over a directory")
	}
	got, err := store.Get(run.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Report != nil || got.Status == "FAILED" {
		t.Fatalf("archive failure committed terminal state: status=%s report=%+v", got.Status, got.Report)
	}
}

func TestCleanupResolutionIsAuditedAfterImmutableReportArchive(t *testing.T) {
	_, _, _, store, p, cfg := newHarness(t)
	run, _, err := store.Accept(runstore.Request{IdempotencyKey: "cleanup-resolution", Plan: p, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	item := report.CleanupItem{CleanupID: "cleanup-1", HostID: "node", MachineID: "machine", BootID: "boot", Status: "PENDING", RemotePath: "/tmp/readyspec-run-owner", OwnerToken: "0123456789abcdef0123456789abcdef"}
	if err := store.CleanupRegistered(context.Background(), run.Acceptance.RunID, item); err != nil {
		t.Fatal(err)
	}
	started := run.Acceptance.AcceptedAt
	archived := report.RunReport{
		SchemaVersion: "readyspec.report/v1", RunID: run.Acceptance.RunID, PlanDigest: p.Digest,
		RunMode: cfg.Mode, RunScope: cfg.Scope, ExecutorBackend: cfg.Backend,
		RunStatus: "FAILED", Verdict: "UNKNOWN", TerminationReason: "EXECUTION_INTERRUPTED",
		VerdictReasons: []string{"EXECUTION_INTERRUPTED"}, StartedAt: started, FinishedAt: time.Now().UTC(),
		TopologyResults: []topology.Result{}, Hosts: []report.HostResult{}, CleanupItems: []report.CleanupItem{item},
		Failures: []report.Failure{},
	}
	if err := store.Finalize(run.Acceptance.RunID, archived); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(run.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Report.CleanupItems[0].OwnerToken != "" {
		t.Fatal("owner token leaked into archived report")
	}
	resolved, err := store.ResolveCleanup(run.Acceptance.RunID, item.CleanupID, "IGNORED", "operator confirmed no cleanup is required")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "IGNORED" || resolved.AuditReason == "" || resolved.OwnerToken != item.OwnerToken {
		t.Fatalf("unexpected cleanup audit record: %+v", resolved)
	}
	latest := store.CleanupItems(run.Acceptance.RunID)
	if len(latest) != 1 || latest[0].Status != "IGNORED" {
		t.Fatalf("cleanup journal did not retain resolution: %+v", latest)
	}
	got, _ = store.Get(run.Acceptance.RunID)
	if got.Report.CleanupItems[0].Status != "PENDING" {
		t.Fatal("operator resolution mutated immutable RunReport")
	}
	if _, err := store.ResolveCleanup(run.Acceptance.RunID, item.CleanupID, "IGNORED", ""); err == nil {
		t.Fatal("empty ignore audit reason was accepted")
	}
}
