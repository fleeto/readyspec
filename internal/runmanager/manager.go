// Package runmanager coordinates durable Runs and applies the global preflight barrier.
package runmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/diagnostic"
	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/planstore"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/topology"
)

type Manager struct {
	store    *runstore.Store
	executor executor.Executor
	mu       sync.Mutex
	active   map[string]context.CancelFunc
}

func (m *Manager) Submit(request runstore.Request, credentials map[string]executor.Credential) (*runstore.Run, bool, error) {
	if err := validatePlanState(request); err != nil {
		wipeCredentials(credentials)
		return nil, false, err
	}
	if request.Config.Backend != "native_ssh" {
		wipeCredentials(credentials)
		return m.store.Accept(request)
	}
	installer, ok := m.executor.(interface {
		InstallRunCredentials(string, map[string]executor.Credential) (func(), error)
	})
	if !ok {
		wipeCredentials(credentials)
		return nil, false, fmt.Errorf("INFRA_ERROR: native SSH credential vault is unavailable")
	}
	boundHosts := map[string]bool{}
	for _, targets := range request.Config.RoleMap {
		for _, hostID := range targets {
			boundHosts[hostID] = true
		}
	}
	for hostID := range credentials {
		if !boundHosts[hostID] {
			credential := credentials[hostID]
			for i := range credential.Password {
				credential.Password[i] = 0
			}
			for i := range credential.SudoPassword {
				credential.SudoPassword[i] = 0
			}
			delete(credentials, hostID)
		}
	}
	for _, host := range request.Config.Inventory {
		if !boundHosts[host.ID] {
			continue
		}
		credential, exists := credentials[host.ID]
		if !exists {
			wipeCredentials(credentials)
			return nil, false, &diagnostic.Error{Code: "CREDENTIAL_HOST_MISSING", HostID: host.ID}
		}
		if credential.Method != request.Config.Auth.Method {
			wipeCredentials(credentials)
			return nil, false, &diagnostic.Error{Code: "CREDENTIAL_METHOD_MISMATCH", HostID: host.ID}
		}
		if request.Config.Auth.Method == "private_key" && credential.Signer == nil ||
			request.Config.Auth.Method == "agent" && credential.AgentSocket == "" ||
			request.Config.Auth.Method == "password" && len(credential.Password) == 0 {
			wipeCredentials(credentials)
			code := "CREDENTIAL_PASSWORD_EMPTY"
			if request.Config.Auth.Method == "private_key" {
				code = "PRIVATE_KEY_PARSE_FAILED"
			} else if request.Config.Auth.Method == "agent" {
				code = "AGENT_ENDPOINT_UNAVAILABLE"
			}
			return nil, false, &diagnostic.Error{Code: code, HostID: host.ID}
		}
	}
	var once sync.Once
	wipe := func() { once.Do(func() { wipeCredentials(credentials) }) }
	accepted, created, err := m.store.AcceptWithCommit(request, func(runID string) (func(), error) {
		rollback, err := installer.InstallRunCredentials(runID, credentials)
		if err != nil {
			if errors.Is(err, executor.ErrCredentialUnavailable) {
				return nil, &diagnostic.Error{Code: "AGENT_ENDPOINT_UNAVAILABLE"}
			}
			return nil, fmt.Errorf("INFRA_ERROR: install task credentials")
		}
		return rollback, nil
	})
	wipe()
	return accepted, created, err
}

func validatePlanState(request runstore.Request) error {
	if request.Plan == nil {
		return fmt.Errorf("PLAN_ERROR: missing plan")
	}
	if request.Plan.State == "ARCHIVED" {
		return fmt.Errorf("PLAN_ARCHIVED: archived plans cannot be executed")
	}
	if request.Config.Mode == "GATE" {
		if request.Plan.State != "RELEASED" {
			return fmt.Errorf("PLAN_GATE_REQUIRES_RELEASED")
		}
		raw, err := plan.DocumentJSON(request.Plan)
		if err != nil {
			return fmt.Errorf("PLAN_ERROR: cannot validate release manifest")
		}
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("PLAN_ERROR: cannot validate release manifest")
		}
		if err := planstore.VerifyManifest(document); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) List() []runstore.Run { return m.store.List() }

func (m *Manager) Get(runID string) (*runstore.Run, error) { return m.store.Get(runID) }

func (m *Manager) LookupSubmission(key string) (runstore.Acceptance, bool, error) {
	return m.store.LookupIdempotency(key)
}

func (m *Manager) CleanupItems(runID string) []report.CleanupItem {
	return m.store.CleanupItems(runID)
}

func (m *Manager) ReadEvents(runID string, after int64) (*runstore.Run, []runstore.Event, int64, bool, error) {
	return m.store.ReadEvents(runID, after)
}

func (m *Manager) ResolveCleanup(ctx context.Context, runID, cleanupID string, credential executor.Credential, confirmTerminate bool, ignoreReason string) (report.CleanupItem, error) {
	run, err := m.store.Get(runID)
	if err != nil {
		return report.CleanupItem{}, err
	}
	var item report.CleanupItem
	for _, candidate := range m.store.CleanupItems(runID) {
		if candidate.CleanupID == cleanupID {
			item = candidate
			break
		}
	}
	if item.CleanupID == "" {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_NOT_FOUND")
	}
	if strings.TrimSpace(ignoreReason) != "" {
		return m.store.ResolveCleanup(runID, cleanupID, "IGNORED", ignoreReason)
	}
	if run.Report == nil {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_RUN_NOT_ARCHIVED")
	}
	implementation, ok := m.executor.(*executor.NativeSSH)
	if !ok || run.Config.Backend != "native_ssh" {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_EXECUTOR_UNAVAILABLE")
	}
	var host *inventory.Host
	for i := range run.Config.Inventory {
		if run.Config.Inventory[i].ID == item.HostID {
			host = &run.Config.Inventory[i]
			break
		}
	}
	if host == nil {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_HOST_NOT_FOUND")
	}
	var expectedHostKey string
	for _, result := range run.Report.Hosts {
		if result.HostID == item.HostID {
			expectedHostKey = result.HostKeyFP
			break
		}
	}
	if expectedHostKey == "" {
		expectedHostKey = item.HostKeyFP
	}
	if expectedHostKey == "" {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_IDENTITY_UNAVAILABLE")
	}
	roles := []string{}
	for role, hosts := range run.Config.RoleMap {
		for _, hostID := range hosts {
			if hostID == item.HostID {
				roles = append(roles, role)
			}
		}
	}
	resolved, err := implementation.ResolveCleanup(ctx, expectedHostKey, executor.HostInput{
		Inventory: *host, Roles: roles, AuthMethod: run.Config.Auth.Method,
		PublicKeyFingerprint: run.Config.Auth.PublicKeyFingerprint,
	}, run.Config.Auth.PublicKeyFingerprint, item, credential, confirmTerminate)
	if err != nil {
		return report.CleanupItem{}, err
	}
	return m.store.ResolveCleanup(runID, cleanupID, resolved.Status, resolved.Reason)
}

func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := m.ProcessNext(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func wipeCredentials(credentials map[string]executor.Credential) {
	for hostID, credential := range credentials {
		for i := range credential.Password {
			credential.Password[i] = 0
		}
		for i := range credential.SudoPassword {
			credential.SudoPassword[i] = 0
		}
		credential.Password = nil
		credential.SudoPassword = nil
		credential.Signer = nil
		credential.AgentSocket = ""
		delete(credentials, hostID)
	}
}

type preflightResult struct {
	input          executor.HostInput
	value          executor.PreflightResult
	processingTime time.Duration
}

func New(store *runstore.Store, implementation executor.Executor) (*Manager, error) {
	if store == nil || implementation == nil {
		return nil, fmt.Errorf("run store and executor are required")
	}
	return &Manager{store: store, executor: implementation, active: map[string]context.CancelFunc{}}, nil
}

func (m *Manager) Cancel(runID string) (*runstore.Run, error) {
	run, err := m.store.Cancel(runID)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	cancel := m.active[runID]
	if cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	if cancel == nil {
		if err := m.archive(run, "CANCELLED", "CANCEL_ACCEPTED", "UNKNOWN", []report.HostResult{}, []topology.Result{}, []report.Failure{}); err != nil {
			return nil, err
		}
		m.destroyCredentials(runID)
		return m.store.Get(runID)
	}
	return run, nil
}

func (m *Manager) destroyCredentials(runID string) {
	if destroyer, ok := m.executor.(interface{ DestroyRun(string) }); ok {
		destroyer.DestroyRun(runID)
	}
}

// ProcessNext executes at most one queued Run, with all hosts classified before
// any host is allowed to execute.
func (m *Manager) ProcessNext(ctx context.Context) (bool, error) {
	run, ok, err := m.store.EnqueueNext()
	if err != nil || !ok {
		return ok, err
	}
	defer m.destroyCredentials(run.Acceptance.RunID)
	implementation, err := executorForRun(run, m.executor)
	if err != nil {
		return true, m.finalizeFailure(run, "EXECUTOR_UNAVAILABLE", "EXECUTOR", "accepted executor backend is unavailable", "UNKNOWN")
	}
	wholeDeadline := run.Acceptance.AcceptedAt.Add(time.Duration(run.Config.Budgets.RunSeconds) * time.Second)
	queueDeadline := run.Acceptance.AcceptedAt.Add(time.Duration(run.Config.Budgets.QueueSeconds) * time.Second)
	if time.Now().After(queueDeadline) {
		return true, m.finalizeFailure(run, "QUEUE_TIMEOUT", "QUEUE", "Run expired before execution began", "UNKNOWN")
	}
	if !time.Now().Before(wholeDeadline) {
		return true, m.finalizeFailure(run, "RUN_TIMEOUT", "BUDGET", "whole-Run budget expired", "UNKNOWN")
	}
	workCtx, cancel := context.WithDeadline(ctx, wholeDeadline)
	m.mu.Lock()
	m.active[run.Acceptance.RunID] = cancel
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.active, run.Acceptance.RunID)
		m.mu.Unlock()
	}()
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			_, _ = m.Cancel(run.Acceptance.RunID)
		case <-watchStop:
		}
	}()
	defer func() {
		close(watchStop)
		<-watchDone
	}()
	return true, m.execute(workCtx, ctx, run, implementation)
}

func executorForRun(run *runstore.Run, fallback executor.Executor) (executor.Executor, error) {
	switch run.Config.Backend {
	case "mock_simulator":
		return executor.Simulator{Profiles: run.Config.Simulator}, nil
	case "native_ssh":
		if _, ok := fallback.(*executor.NativeSSH); !ok {
			if _, simulator := fallback.(executor.Simulator); simulator {
				return nil, fmt.Errorf("native SSH executor unavailable")
			}
		}
		return fallback, nil
	default:
		return nil, fmt.Errorf("unknown executor backend")
	}
}

func (m *Manager) execute(ctx, parentCtx context.Context, run *runstore.Run, implementation executor.Executor) error {
	bindings := bindingsByHost(run.Config)
	previewInput := preview.Input{Hosts: run.Config.Inventory, RoleMap: run.Config.RoleMap, Overrides: run.Config.Overrides}
	previewPlan := *run.Plan
	previewPlan.Roles = append([]plan.Role(nil), run.Plan.Roles...)
	for i := range previewPlan.Roles {
		previewPlan.Roles[i].MinNodes = 0
		previewPlan.Roles[i].MaxNodes = nil
		previewPlan.Roles[i].AllowColocation = true
	}
	compiledPreview, err := preview.Build(&previewPlan, previewInput)
	if err != nil {
		return m.finalizeFailure(run, "PLAN_ERROR", "PREVIEW", "cannot compile frozen Run inputs", "UNKNOWN")
	}
	artifactsByHost := map[string]compiler.ExecutionArtifacts{}
	for _, host := range compiledPreview.Hosts {
		artifactsByHost[host.Host.ID] = host.Artifacts
	}
	hostInputs := make([]executor.HostInput, 0, len(compiledPreview.Hosts))
	for _, target := range compiledPreview.Hosts {
		host := target.Host
		roles := append([]string(nil), bindings[host.ID]...)
		sort.Strings(roles)
		hostInputs = append(hostInputs, executor.HostInput{Inventory: host, Roles: roles, AuthMethod: run.Config.Auth.Method, PublicKeyFingerprint: run.Config.Auth.PublicKeyFingerprint})
	}
	classified := make([]preflightResult, len(hostInputs))
	semaphore := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, host := range hostInputs {
		wg.Add(1)
		go func(index int, host executor.HostInput) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				classified[index] = preflightResult{input: host, value: executor.PreflightResult{Failure: &executor.Failure{Class: executor.FailureExecution, Phase: "PREFLIGHT", Message: "Run budget expired"}}}
				return
			}
			defer func() { <-semaphore }()
			stageCtx, stageCancel := context.WithTimeout(ctx, time.Duration(run.Config.Budgets.HostSeconds)*time.Second)
			started := time.Now()
			result := implementation.Preflight(stageCtx, executor.PreflightRequest{RunID: run.Acceptance.RunID, Plan: run.Plan, Config: previewInput, Host: host, Artifacts: artifactsByHost[host.Inventory.ID], Journal: m.store})
			processing := time.Since(started)
			stageErr := stageCtx.Err()
			stageCancel()
			if stageErr != nil && result.Failure == nil {
				class, phase := executor.FailureExecution, "PREFLIGHT"
				if errors.Is(stageErr, context.DeadlineExceeded) && ctx.Err() == nil {
					class, phase = executor.FailureHostTimeout, "BUDGET"
				}
				result.Failure = &executor.Failure{Class: class, Phase: phase, Message: "host processing budget expired"}
			}
			if result.Failure != nil {
				if phaseErr := m.store.Phase(context.Background(), run.Acceptance.RunID, host.Inventory.ID, "FINISHED"); phaseErr != nil {
					result.Failure = &executor.Failure{Class: executor.FailureExecution, Phase: "JOURNAL", Message: "preflight outcome could not be durably recorded"}
				}
			}
			classified[index] = preflightResult{input: host, value: result, processingTime: processing}
		}(i, host)
	}
	wg.Wait()
	if _, err := m.acceptContextCancellation(parentCtx, run.Acceptance.RunID); err != nil {
		return err
	}
	if isCancellationAccepted(m.store, run.Acceptance.RunID) {
		hosts := preflightFailures(classified)
		facts := make([]topology.Host, 0, len(classified))
		for _, item := range classified {
			f := item.value.Facts
			facts = append(facts, topology.Host{ID: item.input.Inventory.ID, Arch: f.Arch, MachineID: f.MachineID, BootID: f.BootID, HostKey: f.HostKey, Roles: item.input.Roles})
		}
		topologyResults := topology.Check(run.Plan, facts, run.Config.Scope, nil)
		verdict := report.EvaluateVerdict(run.Plan, run.Config.Mode, run.Config.Scope, run.Config.Backend, "CANCELLED", hosts, topologyResults)
		return m.archive(run, "CANCELLED", "CANCEL_ACCEPTED", verdict, hosts, topologyResults, []report.Failure{})
	}

	authRejected := false
	for _, item := range classified {
		if item.value.Failure != nil && item.value.Failure.Class == executor.FailureAuth {
			authRejected = true
		}
	}
	if authRejected {
		hosts := preflightFailures(classified)
		return m.archive(run, "FAILED", "AUTH_PREFLIGHT_REJECTED", "UNKNOWN", hosts, []topology.Result{}, []report.Failure{{Code: "AUTH_ERROR", Phase: "PREFLIGHT", Message: "authentication or required privilege was rejected"}})
	}
	for _, item := range classified {
		if item.value.Failure != nil && item.value.Failure.Class == executor.FailureSudoUnknown {
			hosts := preflightFailures(classified)
			return m.archive(run, "FAILED", "SUDO_POLICY_UNVERIFIABLE", "UNKNOWN", hosts, []topology.Result{}, []report.Failure{{Code: "SUDO_POLICY_UNVERIFIABLE", Phase: "PREFLIGHT", Message: "required sudo policy could not be verified"}})
		}
	}

	facts := make([]topology.Host, 0, len(classified))
	for _, item := range classified {
		f := item.value.Facts
		roles := append([]string(nil), item.input.Roles...)
		facts = append(facts, topology.Host{ID: item.input.Inventory.ID, Arch: f.Arch, MachineID: f.MachineID, BootID: f.BootID, HostKey: f.HostKey, Roles: roles})
	}
	duplicate := duplicateIdentityHosts(facts)
	observations := []topology.ItemObservation{}
	hostResults := preflightFailures(classified)
	executionFailed := false
	validModeResults := 0
	globalFailure := []report.Failure{}
	for _, item := range classified {
		if _, err := m.acceptContextCancellation(parentCtx, run.Acceptance.RunID); err != nil {
			return err
		}
		if isCancellationAccepted(m.store, run.Acceptance.RunID) {
			break
		}
		if item.value.Failure != nil || duplicate[item.input.Inventory.ID] {
			if item.value.Failure != nil {
				executionFailed = true
			} else {
				executionFailed = true
				hostResults = append(hostResults, report.HostResult{HostID: item.input.Inventory.ID, Status: "FAILED", Roles: item.input.Roles, ItemResults: []compiler.ItemResult{}, Failures: []report.Failure{{Code: "IDENTITY_CONFLICT", Phase: "PREFLIGHT", HostID: item.input.Inventory.ID, Message: "duplicate target machine identity"}}})
			}
			continue
		}
		remaining := time.Duration(run.Config.Budgets.HostSeconds)*time.Second - item.processingTime
		if remaining <= 0 {
			executionFailed = true
			hostResults = append(hostResults, report.HostResult{HostID: item.input.Inventory.ID, Status: "FAILED", Roles: item.input.Roles, ItemResults: []compiler.ItemResult{}, Failures: []report.Failure{{Code: "HOST_TIMEOUT", Phase: "BUDGET", HostID: item.input.Inventory.ID, Message: "host processing budget expired before execution"}}})
			continue
		}
		hostCtx, hostCancel := context.WithTimeout(ctx, remaining)
		outcome := implementation.Execute(hostCtx, executor.ExecuteRequest{RunID: run.Acceptance.RunID, Plan: run.Plan, Config: previewInput, Host: item.input, Facts: item.value.Facts, Artifacts: artifactsByHost[item.input.Inventory.ID]}, m.store)
		if outcome.Failure != nil {
			_ = m.store.Phase(context.Background(), run.Acceptance.RunID, item.input.Inventory.ID, "FINISHED")
		}
		hostErr := hostCtx.Err()
		hostCancel()
		if errors.Is(hostErr, context.DeadlineExceeded) && ctx.Err() == nil && !isCancellationAccepted(m.store, run.Acceptance.RunID) {
			outcome.Failure = &executor.Failure{Class: executor.FailureHostTimeout, Phase: "BUDGET", Message: "host processing budget expired"}
		}
		if outcome.Failure != nil {
			executionFailed = true
		}
		for _, result := range outcome.Results {
			observations = append(observations, topology.ItemObservation{HostID: item.input.Inventory.ID, RoleID: result.RoleID, ItemID: result.ItemID})
		}
		for _, complete := range outcome.Modes {
			if complete {
				validModeResults++
			} else {
				executionFailed = true
			}
		}
		failures := []report.Failure{}
		if outcome.Failure != nil {
			failures = append(failures, report.Failure{Code: string(outcome.Failure.Class), Phase: outcome.Failure.Phase, HostID: item.input.Inventory.ID, Message: outcome.Failure.Message, Diagnostics: append([]diagnostic.Item(nil), outcome.Failure.Diagnostics...)})
		}
		status := "COMPLETED"
		if outcome.Failure != nil && len(outcome.Modes) > 0 {
			completedMode := false
			for _, complete := range outcome.Modes {
				completedMode = completedMode || complete
			}
			if completedMode {
				status = "PARTIAL"
			} else {
				status = "FAILED"
			}
		} else if outcome.Failure != nil {
			status = "FAILED"
		} else {
			for _, complete := range outcome.Modes {
				if !complete {
					status = "PARTIAL"
				}
			}
		}
		hostResult := report.HostResult{
			HostID: item.input.Inventory.ID, Status: status, MachineID: item.value.Facts.MachineID,
			BootID: item.value.Facts.BootID, HostKeyFP: item.value.Facts.HostKey, Arch: item.value.Facts.Arch,
			Roles: item.input.Roles, ItemResults: outcome.Results, Failures: failures,
		}
		if item.value.Facts.MachineID != "" {
			uid := item.value.Facts.UID
			hostResult.LoginUID = &uid
			hostResult.HighRiskRootAudit = uid == 0
			for i := range hostResult.ItemResults {
				for j := range hostResult.ItemResults[i].Evidence {
					hostResult.ItemResults[i].Evidence[j].HighRiskRootAudit = uid == 0
				}
			}
		}
		if err := m.store.AppendHostOutcome(run.Acceptance.RunID, hostResult); err != nil {
			return err
		}
		hostResults = append(hostResults, hostResult)
	}
	topologyResults := topology.Check(run.Plan, facts, run.Config.Scope, observations)
	if err := m.store.AppendTopologyResults(run.Acceptance.RunID, topologyResults); err != nil {
		return err
	}
	if _, err := m.acceptContextCancellation(parentCtx, run.Acceptance.RunID); err != nil {
		return err
	}
	status := "COMPLETED"
	if isCancellationAccepted(m.store, run.Acceptance.RunID) {
		status = "CANCELLED"
	}
	if status == "CANCELLED" {
		globalFailure = append(globalFailure, report.Failure{Code: "CANCEL_ACCEPTED", Phase: "CANCELLATION", Message: "cancellation was durably accepted"})
	} else if ctx.Err() != nil && parentCtx.Err() == nil {
		if validModeResults > 0 {
			status = "PARTIAL"
		} else {
			status = "FAILED"
		}
		executionFailed = true
		globalFailure = append(globalFailure, report.Failure{Code: "RUN_TIMEOUT", Phase: "BUDGET", Message: "Run budget expired"})
	} else if executionFailed && validModeResults > 0 {
		status = "PARTIAL"
	} else if executionFailed && validModeResults == 0 {
		status = "FAILED"
	}
	if len(hostInputs) == 0 {
		status = "FAILED"
		globalFailure = append(globalFailure, report.Failure{Code: "NO_TARGETS", Phase: "PREFLIGHT", Message: "Run contains no target hosts"})
	}
	verdict := report.EvaluateVerdict(run.Plan, run.Config.Mode, run.Config.Scope, run.Config.Backend, status, hostResults, topologyResults)
	for _, host := range hostResults {
		globalFailure = append(globalFailure, host.Failures...)
	}
	return m.archive(run, status, terminationReason(status, globalFailure), verdict, hostResults, topologyResults, globalFailure)
}

func (m *Manager) acceptContextCancellation(ctx context.Context, runID string) (bool, error) {
	if ctx.Err() == nil {
		return isCancellationAccepted(m.store, runID), nil
	}
	if isCancellationAccepted(m.store, runID) {
		return true, nil
	}
	if _, err := m.store.Cancel(runID); err != nil {
		if errors.Is(err, runstore.ErrRunFinalizing) {
			return isCancellationAccepted(m.store, runID), nil
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) finalizeFailure(run *runstore.Run, code, phase, message, verdict string) error {
	return m.archive(run, "FAILED", code, verdict, []report.HostResult{}, []topology.Result{}, []report.Failure{{Code: code, Phase: phase, Message: message}})
}

func (m *Manager) archive(run *runstore.Run, status, reason, verdict string, hosts []report.HostResult, topologyResults []topology.Result, failures []report.Failure) error {
	now := time.Now().UTC()
	// Termination explains why execution ended; it is not the domain reason
	// for the verdict. In particular, normal completion has an empty termination
	// reason but can still have NO_REQUIRED_CHECKS and other uncertainty causes.
	verdict, reasons := report.EvaluateVerdictWithReasons(run.Plan, run.Config.Mode, run.Config.Scope, run.Config.Backend, status, hosts, topologyResults)
	if status == "CANCELLED" && verdict == "" {
		verdict = "UNKNOWN"
	}
	cleanup := m.store.CleanupItems(run.Acceptance.RunID)
	for i := range failures {
		failures[i] = withFailureDiagnostic(failures[i])
	}
	for i := range hosts {
		for j := range hosts[i].Failures {
			hosts[i].Failures[j] = withFailureDiagnostic(hosts[i].Failures[j])
		}
	}
	value := report.RunReport{
		SchemaVersion: "readyspec.report/v1", RunID: run.Acceptance.RunID, PlanDigest: run.Config.PlanDigest,
		RunMode: run.Config.Mode, RunScope: run.Config.Scope, ExecutorBackend: run.Config.Backend,
		AuthMethod: run.Config.Auth.Method,
		RunStatus:  status, Verdict: verdict, TerminationReason: reason,
		VerdictReasons: reasons, StartedAt: run.Acceptance.AcceptedAt, FinishedAt: now,
		TopologyResults: topologyResults, Hosts: hosts, CleanupItems: cleanup, Failures: failures,
	}
	if value.FinishedAt.Before(value.StartedAt) {
		value.FinishedAt = value.StartedAt
	}
	err := m.store.Finalize(run.Acceptance.RunID, value)
	if err != nil && strings.Contains(err.Error(), "RUN_CANCELLATION_WINS") {
		value.RunStatus = "CANCELLED"
		value.TerminationReason = "CANCEL_ACCEPTED"
		value.Verdict = report.EvaluateVerdict(run.Plan, run.Config.Mode, run.Config.Scope, run.Config.Backend, "CANCELLED", hosts, topologyResults)
		value.VerdictReasons = append(value.VerdictReasons, "cancellation was durably accepted before finalization")
		value.Failures = append(value.Failures, report.Failure{Code: "CANCEL_ACCEPTED", Phase: "CANCELLATION", Message: "cancellation was durably accepted before finalization"})
		return m.store.Finalize(run.Acceptance.RunID, value)
	}
	return err
}

func withFailureDiagnostic(value report.Failure) report.Failure {
	if len(value.Diagnostics) != 0 {
		return value
	}
	code := "REMOTE_EXECUTION_FAILED"
	switch value.Code {
	case "AUTH_ERROR":
		code = "SSH_HANDSHAKE_UNVERIFIABLE"
	case "NETWORK_ERROR":
		code = "SSH_CONNECTION_FAILED"
	case "IDENTITY_ERROR":
		code = "SSH_HOST_IDENTITY_UNVERIFIABLE"
	case "HOST_TIMEOUT":
		code = "REMOTE_COMMAND_TIMEOUT"
	case "SUDO_POLICY_UNVERIFIABLE", "SUDO_UNVERIFIABLE":
		code = "SUDO_RESULT_UNVERIFIABLE"
	}
	item := diagnostic.ForCode(code, value.HostID)
	item.Phase = value.Phase
	value.Diagnostics = []diagnostic.Item{item}
	return value
}

func isCancellationAccepted(store *runstore.Store, runID string) bool {
	run, err := store.Get(runID)
	return err == nil && (run.Status == "CANCELLING" || run.Status == "CANCELLED")
}

func bindingsByHost(config runstore.RunConfig) map[string][]string {
	out := map[string][]string{}
	for role, hosts := range config.RoleMap {
		for _, host := range hosts {
			out[host] = append(out[host], role)
		}
	}
	return out
}

func preflightFailures(items []preflightResult) []report.HostResult {
	result := []report.HostResult{}
	for _, item := range items {
		if item.value.Failure == nil {
			continue
		}
		failure := item.value.Failure
		result = append(result, report.HostResult{
			HostID: item.input.Inventory.ID, Status: "FAILED", Roles: append([]string(nil), item.input.Roles...),
			ItemResults: []compiler.ItemResult{},
			Failures:    []report.Failure{{Code: string(failure.Class), Phase: failure.Phase, HostID: item.input.Inventory.ID, Message: failure.Message, Diagnostics: append([]diagnostic.Item(nil), failure.Diagnostics...)}},
		})
	}
	return result
}

func duplicateIdentityHosts(hosts []topology.Host) map[string]bool {
	duplicates := map[string]bool{}
	seen := map[[3]string]string{}
	for _, host := range hosts {
		if host.MachineID == "" || host.BootID == "" || host.HostKey == "" {
			continue
		}
		key := [3]string{host.MachineID, host.BootID, host.HostKey}
		if old, ok := seen[key]; ok && old != host.ID {
			duplicates[old], duplicates[host.ID] = true, true
		} else {
			seen[key] = host.ID
		}
	}
	return duplicates
}

func terminationReason(status string, failures []report.Failure) string {
	if status == "COMPLETED" {
		return ""
	}
	if len(failures) > 0 {
		return failures[0].Code
	}
	return status
}
