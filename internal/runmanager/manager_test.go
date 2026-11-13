package runmanager_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/template"
	"github.com/fleeto/readyspec/internal/workspace"
)

const planSource = `schema_version: readyspec.plan/v1
plan_id: manager-test
name: Manager test
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

func TestSubmitUnavailableAgentIsAuthenticationError(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ws.Close(); err != nil {
			t.Errorf("close workspace: %v", err)
		}
	})
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plan.Load(strings.NewReader(planSource))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runmanager.New(store, executor.NewNativeSSH())
	if err != nil {
		t.Fatal(err)
	}
	request := runstore.Request{IdempotencyKey: "unavailable-agent", Plan: p, Config: runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "TRIAL", Scope: "FULL", Backend: "native_ssh",
		Inventory: []inventory.Host{{ID: "node", Address: "127.0.0.1", Port: 22, Username: "tester"}},
		RoleMap:   map[string][]string{"app": {"node"}},
		Auth:      runstore.AuthIdentity{Method: "agent", PublicKeyFingerprint: "SHA256:selected"},
		Budgets:   runstore.Budgets{QueueSeconds: 1800, RunSeconds: 3600, HostSeconds: 600},
	}}
	run, _, err := manager.Submit(request, map[string]executor.Credential{"node": {
		Method: "agent", AgentSocket: filepath.Join(t.TempDir(), "missing.sock"),
	}})
	if err == nil || !strings.HasPrefix(err.Error(), "AUTH_ERROR:") || run != nil {
		t.Fatalf("unavailable Agent must reject submission as AUTH_ERROR, got run=%v err=%v", run, err)
	}
	if runs := store.List(); len(runs) != 0 {
		t.Fatalf("unavailable Agent left %d accepted runs", len(runs))
	}
}

type countingExecutor struct {
	executions atomic.Int32
	failHost   string
	failClass  executor.FailureClass
}

func (e *countingExecutor) Preflight(_ context.Context, req executor.PreflightRequest) executor.PreflightResult {
	if req.Host.Inventory.ID == e.failHost {
		class := e.failClass
		if class == "" {
			class = executor.FailureAuth
		}
		return executor.PreflightResult{Failure: &executor.Failure{Class: class, Phase: "AUTH", Message: "preflight rejected"}}
	}
	return executor.PreflightResult{Facts: executor.HostFacts{MachineID: "machine-" + req.Host.Inventory.ID, BootID: "boot-" + req.Host.Inventory.ID, HostKey: "key-" + req.Host.Inventory.ID, Arch: "x86_64"}}
}

func (e *countingExecutor) Execute(context.Context, executor.ExecuteRequest, executor.Journal) executor.HostOutcome {
	e.executions.Add(1)
	return executor.HostOutcome{Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
}

type budgetExecutor struct {
	preflights atomic.Int32
	executions atomic.Int32
	modeResult bool
	started    chan struct{}
}

type matrixExecutor struct {
	facts    map[string]executor.HostFacts
	outcomes map[string]executor.HostOutcome
}

type preflightBudgetExecutor struct {
	executions atomic.Int32
}

func (*preflightBudgetExecutor) Preflight(ctx context.Context, _ executor.PreflightRequest) executor.PreflightResult {
	<-ctx.Done()
	return executor.PreflightResult{}
}

func (e *preflightBudgetExecutor) Execute(context.Context, executor.ExecuteRequest, executor.Journal) executor.HostOutcome {
	e.executions.Add(1)
	return executor.HostOutcome{Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
}

type selectivePreflightBudgetExecutor struct {
	executions atomic.Int32
}

func (*selectivePreflightBudgetExecutor) Preflight(ctx context.Context, req executor.PreflightRequest) executor.PreflightResult {
	if req.Host.Inventory.ID == "node-slow" {
		<-ctx.Done()
		return executor.PreflightResult{}
	}
	return executor.PreflightResult{Facts: executor.HostFacts{MachineID: "machine-fast", BootID: "boot-fast", HostKey: "key-fast", Arch: "x86_64"}}
}

func (e *selectivePreflightBudgetExecutor) Execute(context.Context, executor.ExecuteRequest, executor.Journal) executor.HostOutcome {
	e.executions.Add(1)
	return executor.HostOutcome{Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true}}
}

type blockingCancelExecutor struct {
	started     chan struct{}
	contextDone chan struct{}
	allowReturn chan struct{}
}

func (*blockingCancelExecutor) Preflight(context.Context, executor.PreflightRequest) executor.PreflightResult {
	return executor.PreflightResult{Facts: executor.HostFacts{MachineID: "machine", BootID: "boot", HostKey: "key", Arch: "x86_64"}}
}

func (e *blockingCancelExecutor) Execute(ctx context.Context, _ executor.ExecuteRequest, _ executor.Journal) executor.HostOutcome {
	close(e.started)
	<-ctx.Done()
	close(e.contextDone)
	<-e.allowReturn
	return executor.HostOutcome{Failure: &executor.Failure{Class: executor.FailureExecution, Phase: "CANCELLED", Message: "execution stopped"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false}}
}

func (e *matrixExecutor) Preflight(_ context.Context, req executor.PreflightRequest) executor.PreflightResult {
	facts, ok := e.facts[req.Host.Inventory.ID]
	if !ok {
		facts = executor.HostFacts{MachineID: "machine-" + req.Host.Inventory.ID, BootID: "boot-" + req.Host.Inventory.ID, HostKey: "key-" + req.Host.Inventory.ID, Arch: "x86_64"}
	}
	return executor.PreflightResult{Facts: facts}
}

func (e *matrixExecutor) Execute(ctx context.Context, req executor.ExecuteRequest, journal executor.Journal) executor.HostOutcome {
	if outcome, ok := e.outcomes[req.Host.Inventory.ID]; ok {
		for _, cleanup := range outcome.Cleanup {
			if err := journal.CleanupRegistered(ctx, req.RunID, cleanup); err != nil {
				return executor.HostOutcome{Failure: &executor.Failure{Class: executor.FailureExecution, Phase: "CLEANUP_JOURNAL", Message: "cannot register cleanup"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: outcome.Modes}
			}
		}
		return outcome
	}
	return executor.HostOutcome{Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true}}
}

func (e *budgetExecutor) Preflight(context.Context, executor.PreflightRequest) executor.PreflightResult {
	e.preflights.Add(1)
	return executor.PreflightResult{Facts: executor.HostFacts{MachineID: "machine", BootID: "boot", HostKey: "key", Arch: "x86_64"}}
}

func (e *budgetExecutor) Execute(ctx context.Context, _ executor.ExecuteRequest, _ executor.Journal) executor.HostOutcome {
	e.executions.Add(1)
	if e.started != nil {
		close(e.started)
	}
	<-ctx.Done()
	return executor.HostOutcome{
		Failure: &executor.Failure{Class: executor.FailureExecution, Phase: "TIMEOUT", Message: "budget expired"},
		Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": e.modeResult},
	}
}

func TestSystemContextCancellationIsDurableRunCancellation(t *testing.T) {
	implementation := &budgetExecutor{modeResult: true, started: make(chan struct{})}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 10})
	defer ws.Close()
	defer lease.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.ProcessNext(ctx)
		done <- err
	}()
	select {
	case <-implementation.started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("executor did not begin before system-context cancellation")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunManager did not finish after system-context cancellation")
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.RunStatus != "CANCELLED" || run.Report.TerminationReason != "CANCEL_ACCEPTED" || report.ExitCode(report.ExitConditions{Report: run.Report}) != 14 {
		t.Fatalf("system cancellation report=%+v", run.Report)
	}
	foundCancelEvent := false
	for _, event := range run.Events {
		if event.Kind == "CANCEL_ACCEPTED" {
			foundCancelEvent = true
			break
		}
	}
	if !foundCancelEvent {
		t.Fatal("system cancellation was not durably recorded")
	}
}

func TestSystemCancellationIsPersistedBeforeExecutorReturns(t *testing.T) {
	implementation := &blockingCancelExecutor{started: make(chan struct{}), contextDone: make(chan struct{}), allowReturn: make(chan struct{})}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 10})
	defer ws.Close()
	defer lease.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := manager.ProcessNext(ctx); done <- err }()
	select {
	case <-implementation.started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("executor did not start")
	}
	cancel()
	select {
	case <-implementation.contextDone:
	case <-time.After(2 * time.Second):
		close(implementation.allowReturn)
		t.Fatal("execution context was not cancelled")
	}
	persisted := false
	deadline := time.After(time.Second)
	for !persisted {
		select {
		case <-deadline:
			close(implementation.allowReturn)
			t.Fatal("CANCEL_ACCEPTED was not persisted before executor return")
		default:
			run, err := store.Get(runID)
			if err != nil {
				close(implementation.allowReturn)
				t.Fatal(err)
			}
			for _, event := range run.Events {
				if event.Kind == "CANCEL_ACCEPTED" {
					persisted = true
					break
				}
			}
			if !persisted {
				time.Sleep(time.Millisecond)
			}
		}
	}
	close(implementation.allowReturn)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunManager did not finish after executor was released")
	}
}

func TestShutdownCancellationDoesNotDispatchNextQueuedRun(t *testing.T) {
	implementation := &budgetExecutor{modeResult: true, started: make(chan struct{})}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, firstRunID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 10})
	defer ws.Close()
	defer lease.Close()
	p, err := plan.Load(strings.NewReader(planSource))
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := store.Accept(runstore.Request{
		IdempotencyKey: "queued-after-active",
		Plan:           p,
		Config: runstore.RunConfig{
			PlanDigest: p.Digest, Mode: "GATE", Scope: "FULL", Backend: "native_ssh",
			Inventory: hosts, RoleMap: map[string][]string{"app": {"node-a"}}, Auth: runstore.AuthIdentity{Method: "password"},
			Budgets: runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 10},
		},
	})
	if err != nil || !created {
		t.Fatalf("accept queued Run: created=%v err=%v", created, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(ctx) }()
	select {
	case <-implementation.started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("active Run did not start")
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunManager did not stop after context cancellation")
	}
	first, err := store.Get(firstRunID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.Get(second.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Report == nil || first.Report.RunStatus != "CANCELLED" || queued.Status != "PENDING" || queued.Report != nil || implementation.executions.Load() != 1 {
		t.Fatalf("shutdown dispatched or lost queued work: first=%+v queued=%+v executions=%d", first.Report, queued, implementation.executions.Load())
	}
}

func TestQueuedRunCancellationArchivesImmediatelyWithoutExecution(t *testing.T) {
	implementation := &countingExecutor{}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setup(t, hosts, map[string][]string{"app": {"node-a"}}, implementation)
	defer ws.Close()
	defer lease.Close()
	if _, err := manager.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.RunStatus != "CANCELLED" || report.ExitCode(report.ExitConditions{Report: run.Report}) != 14 || implementation.executions.Load() != 0 {
		t.Fatalf("queued cancellation was not immediately archived: report=%+v executions=%d", run.Report, implementation.executions.Load())
	}
	if ok, err := manager.ProcessNext(context.Background()); err != nil || ok {
		t.Fatalf("cancelled queued Run remained dispatchable: ok=%v err=%v", ok, err)
	}
}

func TestActiveRunCancellationStopsExecutionAndArchivesEvidence(t *testing.T) {
	implementation := &budgetExecutor{modeResult: true, started: make(chan struct{})}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 10})
	defer ws.Close()
	defer lease.Close()
	done := make(chan error, 1)
	go func() {
		_, err := manager.ProcessNext(context.Background())
		done <- err
	}()
	select {
	case <-implementation.started:
	case <-time.After(2 * time.Second):
		t.Fatal("active Run did not start")
	}
	if _, err := manager.Cancel(runID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active execution did not stop after cancellation")
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.RunStatus != "CANCELLED" || report.ExitCode(report.ExitConditions{Report: run.Report}) != 14 {
		t.Fatalf("active cancellation report=%+v", run.Report)
	}
	if len(run.Report.Hosts) != 1 || run.Report.Hosts[0].Status != "PARTIAL" {
		t.Fatalf("cancelled execution evidence was not retained: %+v", run.Report.Hosts)
	}
}

func TestMixedHostOutcomesMapToRunStatusVerdictAndExitCode(t *testing.T) {
	failure := &executor.Failure{Class: executor.FailureExecution, Phase: "EXECUTE", Message: "fixture failure"}
	tests := []struct {
		name        string
		facts       map[string]executor.HostFacts
		outcomes    map[string]executor.HostOutcome
		wantStatus  string
		wantVerdict string
		wantExit    int
		wantCleanup int
	}{
		{
			name:       "all hosts and modes complete",
			wantStatus: "COMPLETED", wantVerdict: "UNKNOWN", wantExit: 11,
		},
		{
			name: "one valid mode and one failed host",
			outcomes: map[string]executor.HostOutcome{
				"node-b": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false}},
			},
			wantStatus: "PARTIAL", wantVerdict: "UNKNOWN", wantExit: 11,
		},
		{
			name: "standard mode valid and privileged mode failed",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true, "PRIVILEGED": false}},
			},
			wantStatus: "PARTIAL", wantVerdict: "UNKNOWN", wantExit: 11,
		},
		{
			name: "cleanup warning does not rewrite completed execution",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{{CleanupID: "cleanup-node-a", HostID: "node-a", Status: "UNKNOWN", Reason: "remote cleanup could not be confirmed"}}, Modes: map[string]bool{"STANDARD": true, "PRIVILEGED": true}},
			},
			wantStatus: "COMPLETED", wantVerdict: "UNKNOWN", wantExit: 11, wantCleanup: 1,
		},
		{
			name: "no host produced a valid mode",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false}},
				"node-b": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false}},
			},
			wantStatus: "FAILED", wantVerdict: "UNKNOWN", wantExit: 11,
		},
		{
			name: "observed topology fail remains not ready without valid modes",
			facts: map[string]executor.HostFacts{
				"node-a": {MachineID: "machine-node-a", BootID: "boot-node-a", HostKey: "key-node-a", Arch: "aarch64"},
			},
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false}},
				"node-b": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false}},
			},
			wantStatus: "FAILED", wantVerdict: "NOT_READY", wantExit: 10,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hosts := []inventory.Host{
				{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"},
				{ID: "node-b", Address: "127.0.0.2", Port: 22, Username: "tester"},
			}
			implementation := &matrixExecutor{facts: tc.facts, outcomes: tc.outcomes}
			ws, lease, store, manager, runID := setup(t, hosts, map[string][]string{"app": {"node-a", "node-b"}}, implementation)
			defer ws.Close()
			defer lease.Close()
			if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
				t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
			}
			run, err := store.Get(runID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Report == nil || run.Report.RunStatus != tc.wantStatus || run.Report.Verdict != tc.wantVerdict {
				t.Fatalf("report status/verdict=(%v,%v), want=(%v,%v): %+v", run.Report.RunStatus, run.Report.Verdict, tc.wantStatus, tc.wantVerdict, run.Report)
			}
			if got := report.ExitCode(report.ExitConditions{Report: run.Report}); got != tc.wantExit {
				t.Fatalf("exit code=%d want=%d", got, tc.wantExit)
			}
			if len(run.Report.CleanupItems) != tc.wantCleanup {
				t.Fatalf("cleanup item count=%d want=%d", len(run.Report.CleanupItems), tc.wantCleanup)
			}
		})
	}
}

func TestMixedStandardAndPrivilegedModesAcrossHosts(t *testing.T) {
	failure := &executor.Failure{Class: executor.FailureExecution, Phase: "EXECUTE", Message: "fixture mode failure"}
	tests := []struct {
		name      string
		outcomes  map[string]executor.HostOutcome
		wantRun   string
		wantHosts map[string]string
		wantExit  int
	}{
		{
			name: "all planned modes valid on every host",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true, "PRIVILEGED": true}},
				"node-b": {Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true, "PRIVILEGED": true}},
			},
			wantRun: "COMPLETED", wantHosts: map[string]string{"node-a": "COMPLETED", "node-b": "COMPLETED"}, wantExit: 11,
		},
		{
			name: "each host completes a different mode",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true, "PRIVILEGED": false}},
				"node-b": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false, "PRIVILEGED": true}},
			},
			wantRun: "PARTIAL", wantHosts: map[string]string{"node-a": "PARTIAL", "node-b": "PARTIAL"}, wantExit: 11,
		},
		{
			name: "one host completes both modes while another completes none",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true, "PRIVILEGED": true}},
				"node-b": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false, "PRIVILEGED": false}},
			},
			wantRun: "PARTIAL", wantHosts: map[string]string{"node-a": "COMPLETED", "node-b": "FAILED"}, wantExit: 11,
		},
		{
			name: "no host completes either mode",
			outcomes: map[string]executor.HostOutcome{
				"node-a": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false, "PRIVILEGED": false}},
				"node-b": {Failure: failure, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": false, "PRIVILEGED": false}},
			},
			wantRun: "FAILED", wantHosts: map[string]string{"node-a": "FAILED", "node-b": "FAILED"}, wantExit: 11,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hosts := []inventory.Host{
				{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"},
				{ID: "node-b", Address: "127.0.0.2", Port: 22, Username: "tester"},
			}
			implementation := &matrixExecutor{outcomes: tc.outcomes}
			ws, lease, store, manager, runID := setup(t, hosts, map[string][]string{"app": {"node-a", "node-b"}}, implementation)
			defer ws.Close()
			defer lease.Close()
			if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
				t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
			}
			run, err := store.Get(runID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Report == nil || run.Report.RunStatus != tc.wantRun || run.Report.Verdict != "UNKNOWN" {
				t.Fatalf("RunStatus/Verdict=(%v,%v), want=(%s,UNKNOWN)", run.Report.RunStatus, run.Report.Verdict, tc.wantRun)
			}
			if got := report.ExitCode(report.ExitConditions{Report: run.Report}); got != tc.wantExit {
				t.Fatalf("exit code=%d want=%d", got, tc.wantExit)
			}
			if len(run.Report.Hosts) != len(tc.wantHosts) {
				t.Fatalf("reported %d hosts, want %d", len(run.Report.Hosts), len(tc.wantHosts))
			}
			for _, host := range run.Report.Hosts {
				if want := tc.wantHosts[host.HostID]; host.Status != want {
					t.Errorf("host %s status=%s want=%s", host.HostID, host.Status, want)
				}
			}
		})
	}
}

func setup(t *testing.T, hosts []inventory.Host, roleMap map[string][]string, implementation executor.Executor) (*workspace.Store, *workspace.Lease, *runstore.Store, *runmanager.Manager, string) {
	return setupWithBudgets(t, hosts, roleMap, implementation, runstore.Budgets{QueueSeconds: 1800, RunSeconds: 3600, HostSeconds: 600})
}

func setupWithBudgets(t *testing.T, hosts []inventory.Host, roleMap map[string][]string, implementation executor.Executor, budgets runstore.Budgets) (*workspace.Store, *workspace.Lease, *runstore.Store, *runmanager.Manager, string) {
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
	p, err := plan.Load(strings.NewReader(planSource))
	if err != nil {
		t.Fatal(err)
	}
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	config := runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "GATE", Scope: "FULL", Backend: "native_ssh",
		Inventory: hosts, RoleMap: roleMap, Auth: runstore.AuthIdentity{Method: "password"},
		Budgets: budgets,
	}
	accepted, _, err := store.Accept(runstore.Request{IdempotencyKey: "manager-test", Plan: p, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runmanager.New(store, implementation)
	if err != nil {
		t.Fatal(err)
	}
	return ws, lease, store, manager, accepted.Acceptance.RunID
}

func TestQueueBudgetExpiresBeforePreflight(t *testing.T) {
	implementation := &budgetExecutor{}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 1, RunSeconds: 10, HostSeconds: 5})
	defer ws.Close()
	defer lease.Close()
	time.Sleep(1100 * time.Millisecond)
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext after queue expiry: ok=%v err=%v", ok, err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if implementation.preflights.Load() != 0 || run.Report == nil || run.Report.TerminationReason != "QUEUE_TIMEOUT" || run.Report.RunStatus != "FAILED" || report.ExitCode(report.ExitConditions{Report: run.Report}) != 11 {
		t.Fatalf("queue timeout started work or produced wrong report: preflights=%d report=%+v", implementation.preflights.Load(), run.Report)
	}
}

func TestWholeRunTimeoutStatusDependsOnValidModeResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		modeResult bool
		wantStatus string
	}{
		{name: "no valid mode result", wantStatus: "FAILED"},
		{name: "valid mode result retained", modeResult: true, wantStatus: "PARTIAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			implementation := &budgetExecutor{modeResult: tc.modeResult}
			hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
			ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 1, HostSeconds: 5})
			defer ws.Close()
			defer lease.Close()
			if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
				t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
			}
			run, err := store.Get(runID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Report == nil || run.Report.TerminationReason != "RUN_TIMEOUT" || run.Report.RunStatus != tc.wantStatus {
				t.Fatalf("timeout report=%+v want status %s", run.Report, tc.wantStatus)
			}
		})
	}
}

func TestHostBudgetTimeoutDoesNotWaitForWholeRunDeadline(t *testing.T) {
	implementation := &budgetExecutor{}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 1})
	defer ws.Close()
	defer lease.Close()
	started := time.Now()
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("host timeout waited beyond its own budget: %v", elapsed)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.RunStatus != "FAILED" || run.Report.TerminationReason != "HOST_TIMEOUT" || report.ExitCode(report.ExitConditions{Report: run.Report}) != 11 {
		t.Fatalf("host timeout report=%+v", run.Report)
	}
}

func TestHostBudgetTimeoutDuringPreflightIsTypedAndStopsExecution(t *testing.T) {
	implementation := &preflightBudgetExecutor{}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-a"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 1})
	defer ws.Close()
	defer lease.Close()
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if implementation.executions.Load() != 0 || run.Report == nil || run.Report.RunStatus != "FAILED" || run.Report.TerminationReason != "HOST_TIMEOUT" || report.ExitCode(report.ExitConditions{Report: run.Report}) != 11 {
		t.Fatalf("preflight host timeout report=%+v executions=%d", run.Report, implementation.executions.Load())
	}
}

func TestOneHostBudgetExpiryDoesNotStopOtherEligibleHosts(t *testing.T) {
	implementation := &selectivePreflightBudgetExecutor{}
	hosts := []inventory.Host{
		{ID: "node-slow", Address: "127.0.0.1", Port: 22, Username: "tester"},
		{ID: "node-fast", Address: "127.0.0.2", Port: 22, Username: "tester"},
	}
	ws, lease, store, manager, runID := setupWithBudgets(t, hosts, map[string][]string{"app": {"node-slow", "node-fast"}}, implementation, runstore.Budgets{QueueSeconds: 10, RunSeconds: 10, HostSeconds: 1})
	defer ws.Close()
	defer lease.Close()
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if implementation.executions.Load() != 1 || run.Report == nil || run.Report.RunStatus != "PARTIAL" || run.Report.TerminationReason != "HOST_TIMEOUT" {
		t.Fatalf("one host timeout stopped eligible work or mapped incorrectly: executions=%d report=%+v", implementation.executions.Load(), run.Report)
	}
	if report.ExitCode(report.ExitConditions{Report: run.Report}) != 11 {
		t.Fatal("partial evidence without explicit FAIL must map to exit 11")
	}
}

func TestPreflightAuthRejectionPreventsEveryExecution(t *testing.T) {
	implementation := &countingExecutor{failHost: "node-a"}
	hosts := []inventory.Host{
		{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"},
		{ID: "node-b", Address: "127.0.0.2", Port: 22, Username: "tester"},
	}
	ws, lease, store, manager, runID := setup(t, hosts, map[string][]string{"app": {"node-a", "node-b"}}, implementation)
	defer ws.Close()
	defer lease.Close()
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if implementation.executions.Load() != 0 {
		t.Fatalf("executed %d hosts after global auth rejection", implementation.executions.Load())
	}
	if run.Report == nil || run.Report.RunStatus != "FAILED" || run.Report.TerminationReason != "AUTH_PREFLIGHT_REJECTED" {
		t.Fatalf("unexpected auth refusal report: %+v", run.Report)
	}
	for _, host := range run.Report.Hosts {
		if len(host.ItemResults) != 0 {
			t.Fatalf("auth refusal invented observations: %+v", host.ItemResults)
		}
	}
	if report.ExitCode(report.ExitConditions{AuthError: true, Report: run.Report}) != 15 {
		t.Fatal("auth refusal did not map to exit 15")
	}
}

func TestUnverifiableSudoPolicyRefusesAllTargetsWithUnknownVerdict(t *testing.T) {
	implementation := &countingExecutor{failHost: "node-a", failClass: executor.FailureSudoUnknown}
	hosts := []inventory.Host{
		{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"},
		{ID: "node-b", Address: "127.0.0.2", Port: 22, Username: "tester"},
	}
	ws, lease, store, manager, runID := setup(t, hosts, map[string][]string{"app": {"node-a", "node-b"}}, implementation)
	defer ws.Close()
	defer lease.Close()
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if implementation.executions.Load() != 0 {
		t.Fatalf("executed %d hosts after sudo policy could not be verified", implementation.executions.Load())
	}
	if run.Report == nil || run.Report.RunStatus != "FAILED" || run.Report.Verdict != "UNKNOWN" || run.Report.TerminationReason != "SUDO_POLICY_UNVERIFIABLE" {
		t.Fatalf("unexpected sudo policy refusal report: %+v", run.Report)
	}
	if report.ExitCode(report.ExitConditions{Report: run.Report}) != 11 {
		t.Fatal("unverifiable sudo policy did not map to exit 11")
	}
	for _, host := range run.Report.Hosts {
		if len(host.ItemResults) != 0 {
			t.Fatalf("sudo refusal invented observations: %+v", host.ItemResults)
		}
	}
}

func TestSimulatorEmptyPlanCompletesWithoutInventingReady(t *testing.T) {
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: 22, Username: "tester"}}
	profiles := executor.Profiles{Hosts: map[string]executor.Profile{
		"node-a": {Preflight: "READY", MachineID: "machine-a", BootID: "boot-a", HostKey: "key-a", Arch: "x86_64", UID: 1000},
	}}
	ws, lease, store, manager, runID := setupWithConfig(t, hosts, map[string][]string{"app": {"node-a"}}, executor.Simulator{Profiles: profiles}, "mock_simulator")
	defer ws.Close()
	defer lease.Close()
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("ProcessNext: ok=%v err=%v", ok, err)
	}
	run, err := store.Get(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.RunStatus != "COMPLETED" || run.Report.Verdict != "UNKNOWN" {
		t.Fatalf("empty simulator run result = %+v", run.Report)
	}
	if report.ExitCode(report.ExitConditions{Report: run.Report}) != 11 {
		t.Fatal("empty mock plan must not produce a gate-pass exit")
	}
}

func TestArchivedRootAuditUsesVerifiedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, username string
		uid            int
		root           bool
	}{
		{"root", "root", 0, true},
		{"uid_zero_alias", "root-alias", 0, true},
		{"username_is_not_identity", "root", 1000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, ok := template.Lookup("H01", "1.0.0")
			if !ok {
				t.Fatal("H01 template unavailable")
			}
			item := plan.CheckItem{ItemID: "arch", Name: "Architecture", TemplateID: "H01", TemplateVersion: "1.0.0", TemplateDigest: snapshot["digest"].(string), Importance: "REQUIRED", Enabled: true, Params: map[string]any{"allowed_arch": []any{"x86_64"}}, ExplicitNullParams: []string{}, OverridableParams: []string{}}
			doc := map[string]any{"schema_version": "readyspec.plan/v1", "plan_id": "root-audit", "name": "Root audit", "software_name": "fixture", "software_version": "1", "version": "1.0.0", "state": "DRAFT", "topology_policy": map[string]any{"allowed_arch": []string{"x86_64"}}, "roles": []plan.Role{{RoleID: "app", Name: "App", MinNodes: 1, AllowColocation: false, Items: []plan.CheckItem{item}}}, "embedded_templates": []any{snapshot}, "template_lock": map[string]any{"H01@1.0.0": snapshot["digest"]}}
			raw, _ := json.Marshal(doc)
			implementation := &matrixExecutor{
				facts:    map[string]executor.HostFacts{"node": {MachineID: "machine", BootID: "boot", HostKey: "key", Arch: "x86_64", UID: tc.uid, Root: tc.root}},
				outcomes: map[string]executor.HostOutcome{"node": {Results: []compiler.ItemResult{{RoleID: "app", ItemID: "arch", Status: "PASS", TemplateID: "H01", TemplateVersion: "1.0.0", ExecutionMode: "STANDARD", EvidenceMode: "METADATA", Evidence: []compiler.Evidence{{Resource: "architecture", Property: "stdout", Actual: "x86_64", Source: "uname -m"}}}}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{"STANDARD": true}}},
			}
			ws, lease, store, manager, runID := setupWithConfig(t, []inventory.Host{{ID: "node", Address: "127.0.0.1", Port: 22, Username: tc.username}}, map[string][]string{"app": {"node"}}, implementation, "native_ssh", string(raw))
			defer ws.Close()
			defer lease.Close()
			if _, err := manager.ProcessNext(context.Background()); err != nil {
				t.Fatal(err)
			}
			run, err := store.Get(runID)
			if err != nil || run.Report == nil {
				t.Fatalf("report unavailable: %v", err)
			}
			encoded, _ := json.Marshal(run.Report)
			var archived map[string]any
			if err := json.Unmarshal(encoded, &archived); err != nil {
				t.Fatal(err)
			}
			host := archived["hosts"].([]any)[0].(map[string]any)
			if host["login_uid"] != float64(tc.uid) {
				t.Fatalf("verified UID was not archived: %+v", host)
			}
			if audit, _ := host["high_risk_root_audit"].(bool); audit != tc.root {
				t.Fatalf("host root audit = %v, want %v", audit, tc.root)
			}
			result := host["item_results"].([]any)[0].(map[string]any)
			for _, value := range result["evidence"].([]any) {
				if audit, _ := value.(map[string]any)["high_risk_root_audit"].(bool); audit != tc.root {
					t.Fatalf("evidence root audit = %v, want %v", audit, tc.root)
				}
			}
		})
	}
}

func setupWithConfig(t *testing.T, hosts []inventory.Host, roleMap map[string][]string, implementation executor.Executor, backend string, sources ...string) (*workspace.Store, *workspace.Lease, *runstore.Store, *runmanager.Manager, string) {
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
	source := planSource
	if len(sources) > 0 {
		source = sources[0]
	}
	p, err := plan.Load(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	config := runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "GATE", Scope: "FULL", Backend: backend,
		Inventory: hosts, RoleMap: roleMap, Auth: runstore.AuthIdentity{Method: "password"},
		Budgets: runstore.Budgets{QueueSeconds: 1800, RunSeconds: 3600, HostSeconds: 600},
	}
	if simulator, ok := implementation.(executor.Simulator); ok {
		config.Simulator = simulator.Profiles
	}
	accepted, _, err := store.Accept(runstore.Request{IdempotencyKey: "manager-test", Plan: p, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := runmanager.New(store, implementation)
	if err != nil {
		t.Fatal(err)
	}
	return ws, lease, store, manager, accepted.Acceptance.RunID
}

var _ executor.Executor = (*countingExecutor)(nil)
