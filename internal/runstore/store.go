// Package runstore owns durable Run acceptance, journals, reports, and recovery.
package runstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/semantic"
	"github.com/fleeto/readyspec/internal/topology"
	"github.com/fleeto/readyspec/internal/workspace"
)

const (
	maxWaitingRuns = 10
	journalName    = "events.ndjson"
	acceptedName   = "accepted.json"
	reportName     = "report.json"
)

var (
	ErrIdempotencyConflict = errors.New("RUN_IDEMPOTENCY_CONFLICT")
	ErrQueueFull           = errors.New("WORKSPACE_BUSY")
	ErrRunNotFound         = errors.New("RUN_NOT_FOUND")
	ErrRunFinalizing       = errors.New("RUN_ALREADY_FINALIZING")
)

type Budgets struct {
	QueueSeconds int64 `json:"queue_seconds"`
	RunSeconds   int64 `json:"run_seconds"`
	HostSeconds  int64 `json:"host_seconds"`
}

func (b *Budgets) withDefaults() {
	if b.QueueSeconds == 0 {
		b.QueueSeconds = 30 * 60
	}
	if b.RunSeconds == 0 {
		b.RunSeconds = 60 * 60
	}
	if b.HostSeconds == 0 {
		b.HostSeconds = 10 * 60
	}
}

type AuthIdentity struct {
	Method               string `json:"method"`
	PublicKeyFingerprint string `json:"public_key_fingerprint,omitempty"`
}

// RunConfig contains only durable, non-secret execution inputs.
type RunConfig struct {
	PlanDigest string              `json:"plan_digest"`
	Mode       string              `json:"run_mode"`
	Scope      string              `json:"run_scope"`
	Backend    string              `json:"executor_backend"`
	Inventory  []inventory.Host    `json:"inventory"`
	RoleMap    map[string][]string `json:"role_map"`
	Overrides  []preview.Override  `json:"field_overrides,omitempty"`
	Auth       AuthIdentity        `json:"auth_identity"`
	Simulator  executor.Profiles   `json:"simulator_profiles,omitempty"`
	Budgets    Budgets             `json:"budgets"`
}

type Request struct {
	IdempotencyKey string
	Plan           *plan.Plan
	Config         RunConfig
}

type Acceptance struct {
	SchemaVersion  string    `json:"schema_version"`
	RunID          string    `json:"run_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	SemanticDigest string    `json:"semantic_digest"`
	PlanDigest     string    `json:"plan_digest"`
	AcceptedAt     time.Time `json:"accepted_at"`
	InitialStatus  string    `json:"initial_status"`
	PlanPath       string    `json:"plan_path"`
	ConfigPath     string    `json:"config_path"`
}

type Event struct {
	Sequence int64           `json:"sequence"`
	Kind     string          `json:"kind"`
	At       time.Time       `json:"at"`
	HostID   string          `json:"host_id,omitempty"`
	Mode     string          `json:"execution_mode,omitempty"`
	Status   string          `json:"status,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type Run struct {
	Acceptance    Acceptance        `json:"acceptance"`
	Plan          *plan.Plan        `json:"plan"`
	Config        RunConfig         `json:"config"`
	Status        string            `json:"status"`
	Events        []Event           `json:"events"`
	Report        *report.RunReport `json:"report,omitempty"`
	RecoveryError string            `json:"recovery_error,omitempty"`
	finalizing    bool
}

type Store struct {
	mu          sync.Mutex
	workspace   *workspace.Store
	runs        map[string]*Run
	idempotency map[string]string
	queue       []string
	active      string
}

func Open(ws *workspace.Store) (*Store, error) {
	s := &Store{workspace: ws, runs: map[string]*Run{}, idempotency: map[string]string{}, queue: []string{}}
	entries, err := ws.ReadDir("runs")
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read run records: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		id := entry.Name()
		var accepted Acceptance
		if err := ws.ReadJSON(path.Join("runs", id, acceptedName), &accepted); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // uncommitted input files are never accepted or dispatched
			}
			return nil, fmt.Errorf("read acceptance record: %w", err)
		}
		if err := validateAcceptance(id, accepted); err != nil {
			return nil, err
		}
		var frozenPlan plan.Plan
		if err := ws.ReadJSON(accepted.PlanPath, &frozenPlan); err != nil {
			return nil, fmt.Errorf("read frozen plan for %s: %w", id, err)
		}
		var config RunConfig
		if err := ws.ReadJSON(accepted.ConfigPath, &config); err != nil {
			return nil, fmt.Errorf("read frozen config for %s: %w", id, err)
		}
		frozenJSON, err := json.Marshal(frozenPlan)
		if err != nil {
			return nil, fmt.Errorf("encode frozen plan for %s", id)
		}
		validatedPlan, err := plan.Load(bytes.NewReader(frozenJSON))
		if err != nil {
			return nil, fmt.Errorf("frozen Plan %s no longer validates", id)
		}
		if config.PlanDigest != accepted.PlanDigest || frozenPlan.Digest != accepted.PlanDigest || validatedPlan.Digest != accepted.PlanDigest {
			return nil, fmt.Errorf("accepted Run %s plan digest does not match frozen config", id)
		}
		frozenPlan = *validatedPlan
		run := &Run{Acceptance: accepted, Plan: &frozenPlan, Config: config, Status: "PENDING", Events: []Event{}}
		if err := s.replay(run); err != nil {
			run.RecoveryError = "RUN_STORAGE_ERROR"
			run.Status = "FAILED"
		}
		if prior, exists := s.idempotency[accepted.IdempotencyKey]; exists && prior != id {
			return nil, fmt.Errorf("duplicate idempotency key in accepted records")
		}
		s.idempotency[accepted.IdempotencyKey] = id
		s.runs[id] = run
	}
	if err := s.recoverAcceptedRuns(); err != nil {
		return nil, err
	}
	return s, nil
}

func validateAcceptance(id string, accepted Acceptance) error {
	if accepted.SchemaVersion != "readyspec.run-acceptance/v1" || accepted.RunID != id ||
		accepted.IdempotencyKey == "" || accepted.SemanticDigest == "" || accepted.PlanDigest == "" || accepted.AcceptedAt.IsZero() ||
		accepted.InitialStatus != "PENDING" || accepted.PlanPath != path.Join("runs", id, "plan.json") ||
		accepted.ConfigPath != path.Join("runs", id, "config.json") || !safeID(id) {
		return fmt.Errorf("invalid Run acceptance record %q", id)
	}
	return nil
}

func safeID(id string) bool {
	if !strings.HasPrefix(id, "run-") || len(id) != len("run-")+26 {
		return false
	}
	for _, r := range id[len("run-"):] {
		if !(r >= 'A' && r <= 'Z' || r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}

func (s *Store) Accept(req Request) (*Run, bool, error) {
	return s.AcceptWithCommit(req, nil)
}

// AcceptWithCommit installs volatile capabilities after validation and capacity
// checks but before any durable acceptance record can be committed.
func (s *Store) AcceptWithCommit(req Request, beforeCommit func(string) (func(), error)) (*Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Plan == nil || !validIdempotencyKey(req.IdempotencyKey) {
		return nil, false, fmt.Errorf("PLAN_ERROR: invalid Run submission")
	}
	if req.Config.PlanDigest != req.Plan.Digest {
		return nil, false, fmt.Errorf("PLAN_DIGEST_MISMATCH: run config does not match plan")
	}
	if err := validateSubmission(req.Plan, req.Config); err != nil {
		return nil, false, err
	}
	req.Config.Budgets.withDefaults()
	if err := validateConfig(req.Config); err != nil {
		return nil, false, err
	}
	digest, err := semantic.Digest(struct {
		Plan   *plan.Plan `json:"plan"`
		Config RunConfig  `json:"config"`
	}{req.Plan, req.Config})
	if err != nil {
		return nil, false, fmt.Errorf("PLAN_ERROR: cannot digest Run input")
	}
	if id, exists := s.idempotency[req.IdempotencyKey]; exists {
		run := s.runs[id]
		if run.Acceptance.SemanticDigest != digest {
			return nil, false, ErrIdempotencyConflict
		}
		copy := cloneRun(run)
		return &copy, false, nil
	}
	if len(s.queue) >= maxWaitingRuns {
		return nil, false, ErrQueueFull
	}
	rawPlan, err := json.Marshal(req.Plan)
	if err != nil {
		return nil, false, fmt.Errorf("PLAN_ERROR: cannot freeze Plan")
	}
	rawConfig, err := json.Marshal(req.Config)
	if err != nil {
		return nil, false, fmt.Errorf("PLAN_ERROR: cannot freeze RunConfig")
	}
	id := "run-" + randomID()
	rollback := func() {}
	if beforeCommit != nil {
		rollback, err = beforeCommit(id)
		if err != nil {
			return nil, false, err
		}
		if rollback == nil {
			rollback = func() {}
		}
	}
	committed := false
	defer func() {
		if !committed {
			rollback()
		}
	}()
	base := path.Join("runs", id)
	planPath, configPath := path.Join(base, "plan.json"), path.Join(base, "config.json")
	if err := s.workspace.WriteJSON(planPath, req.Plan); err != nil {
		return nil, false, fmt.Errorf("INFRA_ERROR: persist frozen Plan: %w", err)
	}
	if err := s.workspace.WriteJSON(configPath, json.RawMessage(rawConfig)); err != nil {
		return nil, false, fmt.Errorf("INFRA_ERROR: persist frozen RunConfig: %w", err)
	}
	accepted := Acceptance{
		SchemaVersion: "readyspec.run-acceptance/v1", RunID: id,
		IdempotencyKey: req.IdempotencyKey, SemanticDigest: digest,
		PlanDigest: req.Config.PlanDigest,
		AcceptedAt: time.Now().UTC(), InitialStatus: "PENDING",
		PlanPath: planPath, ConfigPath: configPath,
	}
	if err := s.workspace.WriteJSON(path.Join(base, acceptedName), accepted); err != nil {
		return nil, false, fmt.Errorf("INFRA_ERROR: commit Run acceptance: %w", err)
	}
	committed = true
	run := &Run{Acceptance: accepted, Plan: req.Plan, Config: req.Config, Status: "PENDING", Events: []Event{}}
	s.runs[id] = run
	s.idempotency[req.IdempotencyKey] = id
	s.queue = append(s.queue, id)
	_ = rawPlan // retained above as a validation boundary before committing acceptance
	copy := cloneRun(run)
	return &copy, true, nil
}

func validateSubmission(p *plan.Plan, config RunConfig) error {
	input := preview.Input{Hosts: config.Inventory, RoleMap: config.RoleMap, Overrides: config.Overrides}
	// Build validates host inventory, role references and override semantics, but
	// also enforces FullRun node counts and colocation. Those are topology results
	// and must remain reportable for PartialRun, so validate those independently.
	probe := *p
	probe.Roles = make([]plan.Role, len(p.Roles))
	for i, role := range p.Roles {
		probe.Roles[i] = role
		probe.Roles[i].MinNodes = 0
		probe.Roles[i].MaxNodes = nil
		probe.Roles[i].AllowColocation = true
	}
	if _, err := preview.Build(&probe, input); err != nil {
		return err
	}
	return nil
}

func validateConfig(config RunConfig) error {
	if config.Mode != "GATE" && config.Mode != "TRIAL" || config.Scope != "FULL" && config.Scope != "PARTIAL" ||
		config.Backend != "native_ssh" && config.Backend != "mock_simulator" || config.Auth.Method == "" ||
		config.Budgets.QueueSeconds <= 0 || config.Budgets.RunSeconds <= 0 || config.Budgets.HostSeconds <= 0 {
		return fmt.Errorf("PLAN_ERROR: invalid execution configuration")
	}
	if (config.Auth.Method == "private_key" || config.Auth.Method == "agent") && config.Auth.PublicKeyFingerprint == "" {
		return fmt.Errorf("PLAN_ERROR: private key identity is missing")
	}
	return nil
}

func validIdempotencyKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func (s *Store) EnqueueNext() (*Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" {
		return nil, false, nil
	}
	for len(s.queue) > 0 {
		id := s.queue[0]
		s.queue = s.queue[1:]
		run := s.runs[id]
		if run == nil || run.Status != "PENDING" {
			continue
		}
		if _, err := s.appendLocked(run, Event{Kind: "STATE", Status: "RUNNING"}); err != nil {
			s.queue = append([]string{id}, s.queue...)
			return nil, false, err
		}
		s.active = id
		copy := cloneRun(run)
		return &copy, true, nil
	}
	return nil, false, nil
}

func (s *Store) Cancel(id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return nil, ErrRunNotFound
	}
	if run.Report != nil || run.finalizing || run.Status == "COMPLETED" || run.Status == "PARTIAL" || run.Status == "FAILED" || run.Status == "CANCELLED" {
		return nil, ErrRunFinalizing
	}
	if run.Status == "CANCELLING" {
		copy := cloneRun(run)
		return &copy, nil
	}
	if _, err := s.appendLocked(run, Event{Kind: "CANCEL_ACCEPTED", Status: "CANCELLING"}); err != nil {
		return nil, err
	}
	if s.active != id {
		for i, queued := range s.queue {
			if queued == id {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				break
			}
		}
	}
	copy := cloneRun(run)
	return &copy, nil
}

func (s *Store) AppendHostOutcome(id string, host report.HostResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return ErrRunNotFound
	}
	if run.Report != nil || run.finalizing {
		return ErrRunFinalizing
	}
	data, err := json.Marshal(host)
	if err != nil {
		return err
	}
	_, err = s.appendLocked(run, Event{Kind: "HOST_OUTCOME", HostID: host.HostID, Data: data})
	return err
}

func (s *Store) AppendTopologyResults(id string, results []topology.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return ErrRunNotFound
	}
	if run.Report != nil || run.finalizing {
		return ErrRunFinalizing
	}
	data, err := json.Marshal(results)
	if err != nil {
		return err
	}
	_, err = s.appendLocked(run, Event{Kind: "TOPOLOGY_RESULTS", Data: data})
	return err
}

func (s *Store) ExecutionStarted(_ context.Context, runID, hostID, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		return ErrRunNotFound
	}
	if run.Report != nil || run.finalizing {
		return ErrRunFinalizing
	}
	for _, event := range run.Events {
		if event.Kind == "EXECUTION_STARTED" && event.HostID == hostID && event.Mode == mode {
			return fmt.Errorf("EXECUTION_ALREADY_STARTED: mode cannot be retried")
		}
	}
	_, err := s.appendLocked(run, Event{Kind: "EXECUTION_STARTED", HostID: hostID, Mode: mode})
	return err
}

func (s *Store) Phase(_ context.Context, runID, hostID, phase string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		return ErrRunNotFound
	}
	if run.Report != nil || run.finalizing {
		return ErrRunFinalizing
	}
	if hostID == "" || phase == "" {
		return fmt.Errorf("invalid execution phase event")
	}
	_, err := s.appendLocked(run, Event{Kind: "PHASE", HostID: hostID, Status: phase})
	return err
}

func (s *Store) CleanupRegistered(_ context.Context, runID string, item report.CleanupItem) error {
	return s.appendCleanup("CLEANUP_REGISTERED", runID, item)
}

func (s *Store) CleanupUpdated(_ context.Context, runID string, item report.CleanupItem) error {
	return s.appendCleanup("CLEANUP_UPDATED", runID, item)
}

func (s *Store) appendCleanup(kind, runID string, item report.CleanupItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		return ErrRunNotFound
	}
	if run.Report != nil || run.finalizing {
		return ErrRunFinalizing
	}
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = s.appendLocked(run, Event{Kind: kind, HostID: item.HostID, Data: data})
	return err
}

func (s *Store) Finalize(id string, value report.RunReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return ErrRunNotFound
	}
	if run.Report != nil || run.finalizing {
		return ErrRunFinalizing
	}
	if value.RunID != id {
		return fmt.Errorf("REPORT_ERROR: report Run ID mismatch")
	}
	// Ownership tokens are retained only in the private journal for later
	// operator cleanup. They are never part of the public archived report.
	for i := range value.CleanupItems {
		value.CleanupItems[i].OwnerToken = ""
	}
	if run.Status == "CANCELLING" && value.RunStatus != "CANCELLED" {
		return fmt.Errorf("RUN_CANCELLATION_WINS: cancellation was durably accepted before finalization")
	}
	if err := report.Validate(value); err != nil {
		return err
	}
	run.finalizing = true
	if err := s.workspace.WriteJSON(path.Join("runs", id, reportName), value); err != nil {
		run.finalizing = false
		return fmt.Errorf("INFRA_ERROR: persist RunReport: %w", err)
	}
	run.Report = &value
	run.Status = value.RunStatus
	if s.active == id {
		s.active = ""
	}
	if _, err := s.appendLocked(run, Event{Kind: "TERMINAL_ARCHIVED", Status: value.RunStatus}); err != nil {
		return fmt.Errorf("INFRA_ERROR: report committed but terminal journal update failed: %w", err)
	}
	return nil
}

func (s *Store) Get(id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return nil, ErrRunNotFound
	}
	copy := cloneRun(run)
	return &copy, nil
}

func (s *Store) ReadEvents(id string, after int64) (*Run, []Event, int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[id]
	if run == nil {
		return nil, nil, 0, false, ErrRunNotFound
	}
	copy := cloneRun(run)
	latest := int64(len(run.Events))
	reset := after > latest || after < 0
	events := []Event{}
	if !reset {
		for _, event := range run.Events {
			if event.Sequence > after {
				events = append(events, event)
			}
		}
	}
	return &copy, events, latest, reset, nil
}

func (s *Store) List() []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.runs))
	for id := range s.runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]Run, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneRun(s.runs[id]))
	}
	return result
}

func (s *Store) CleanupItems(runID string) []report.CleanupItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	latest := map[string]report.CleanupItem{}
	if run == nil {
		return []report.CleanupItem{}
	}
	for _, event := range run.Events {
		if event.Kind != "CLEANUP_REGISTERED" && event.Kind != "CLEANUP_UPDATED" {
			continue
		}
		var item report.CleanupItem
		if json.Unmarshal(event.Data, &item) == nil && item.CleanupID != "" {
			latest[item.CleanupID] = item
		}
	}
	items := make([]report.CleanupItem, 0, len(latest))
	for _, item := range latest {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CleanupID < items[j].CleanupID })
	return items
}

// ResolveCleanup appends an operator decision after report archival. The RunReport
// remains immutable; the serialized journal is the audit source for later cleanup.
func (s *Store) ResolveCleanup(runID, cleanupID string, status, reason string) (report.CleanupItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		return report.CleanupItem{}, ErrRunNotFound
	}
	if run.Report == nil {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_RUN_NOT_ARCHIVED")
	}
	var item report.CleanupItem
	for _, event := range run.Events {
		if event.Kind != "CLEANUP_REGISTERED" && event.Kind != "CLEANUP_UPDATED" {
			continue
		}
		var candidate report.CleanupItem
		if json.Unmarshal(event.Data, &candidate) == nil && candidate.CleanupID == cleanupID {
			item = candidate
		}
	}
	if item.CleanupID == "" {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_NOT_FOUND")
	}
	if item.Status == "CLEAN" || item.Status == "IGNORED" {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_ALREADY_RESOLVED")
	}
	if status != "CLEAN" && status != "IGNORED" && status != "UNKNOWN" {
		return report.CleanupItem{}, fmt.Errorf("invalid cleanup resolution")
	}
	if status == "IGNORED" && strings.TrimSpace(reason) == "" {
		return report.CleanupItem{}, fmt.Errorf("CLEANUP_AUDIT_REASON_REQUIRED")
	}
	item.Status = status
	item.Reason = ""
	item.AuditReason = strings.TrimSpace(reason)
	data, err := json.Marshal(item)
	if err != nil {
		return report.CleanupItem{}, err
	}
	if _, err := s.appendLocked(run, Event{Kind: "CLEANUP_UPDATED", HostID: item.HostID, Data: data}); err != nil {
		return report.CleanupItem{}, err
	}
	return item, nil
}

func (s *Store) appendLocked(run *Run, event Event) (Event, error) {
	event.Sequence = int64(len(run.Events) + 1)
	event.At = time.Now().UTC()
	if err := s.workspace.AppendJSONLine(path.Join("runs", run.Acceptance.RunID, journalName), event); err != nil {
		return Event{}, fmt.Errorf("INFRA_ERROR: append Run journal: %w", err)
	}
	run.Events = append(run.Events, event)
	switch event.Kind {
	case "STATE", "CANCEL_ACCEPTED", "TERMINAL_ARCHIVED", "ARCHIVE_RECOVERED":
		run.Status = event.Status
	}
	return event, nil
}

func (s *Store) replay(run *Run) error {
	raw, _, err := s.workspace.RecoverJSONLines(path.Join("runs", run.Acceptance.RunID, journalName))
	if err != nil {
		return fmt.Errorf("recover Run journal %s: %w", run.Acceptance.RunID, err)
	}
	for index, line := range raw {
		var event Event
		if err := json.Unmarshal(line, &event); err != nil || event.Sequence != int64(index+1) || event.Kind == "" || event.At.IsZero() || !validEvent(event) {
			return fmt.Errorf("invalid Run journal record %s/%d", run.Acceptance.RunID, index+1)
		}
		run.Events = append(run.Events, event)
		switch event.Kind {
		case "STATE", "CANCEL_ACCEPTED", "TERMINAL_ARCHIVED", "ARCHIVE_RECOVERED":
			run.Status = event.Status
		}
	}
	var archived report.RunReport
	err = s.workspace.ReadJSON(path.Join("runs", run.Acceptance.RunID, reportName), &archived)
	if err == nil {
		if archived.RunID != run.Acceptance.RunID || report.Validate(archived) != nil {
			return fmt.Errorf("invalid committed RunReport for %s", run.Acceptance.RunID)
		}
		run.Report = &archived
		run.Status = archived.RunStatus
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read committed RunReport %s: %w", run.Acceptance.RunID, err)
	}
	return nil
}

func validEvent(event Event) bool {
	switch event.Kind {
	case "STATE":
		return event.Status == "RUNNING"
	case "CANCEL_ACCEPTED":
		return event.Status == "CANCELLING"
	case "TERMINAL_ARCHIVED", "ARCHIVE_RECOVERED":
		return event.Status == "COMPLETED" || event.Status == "PARTIAL" || event.Status == "FAILED" || event.Status == "CANCELLED"
	case "HOST_OUTCOME":
		var value report.HostResult
		return event.HostID != "" && json.Unmarshal(event.Data, &value) == nil && value.HostID == event.HostID
	case "TOPOLOGY_RESULTS":
		var value []topology.Result
		return json.Unmarshal(event.Data, &value) == nil
	case "EXECUTION_STARTED":
		return event.HostID != "" && (event.Mode == "STANDARD" || event.Mode == "PRIVILEGED")
	case "CLEANUP_REGISTERED", "CLEANUP_UPDATED":
		var value report.CleanupItem
		return json.Unmarshal(event.Data, &value) == nil && value.CleanupID != "" && value.HostID == event.HostID
	case "PHASE":
		return event.HostID != "" && event.Status != ""
	default:
		return false
	}
}

func (s *Store) recoverAcceptedRuns() error {
	ids := make([]string, 0, len(s.runs))
	for id := range s.runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		run := s.runs[id]
		if run.RecoveryError != "" {
			continue
		}
		if run.Report != nil {
			if run.Status != run.Report.RunStatus || len(run.Events) == 0 || run.Events[len(run.Events)-1].Kind != "TERMINAL_ARCHIVED" {
				if _, err := s.appendLocked(run, Event{Kind: "ARCHIVE_RECOVERED", Status: run.Report.RunStatus}); err != nil {
					return err
				}
			}
			continue
		}
		cancelled := run.Status == "CANCELLING"
		for _, event := range run.Events {
			if event.Kind == "CANCEL_ACCEPTED" {
				cancelled = true
			}
		}
		status, reason := "FAILED", "CONTROLLER_INTERRUPTED"
		if cancelled {
			status, reason = "CANCELLED", "CANCEL_ACCEPTED"
		}
		now := time.Now().UTC()
		value := report.RunReport{
			SchemaVersion: "readyspec.report/v1", RunID: id, PlanDigest: run.Config.PlanDigest,
			RunMode: run.Config.Mode, RunScope: run.Config.Scope, ExecutorBackend: run.Config.Backend,
			RunStatus: status, Verdict: s.recoveredVerdict(run, status), TerminationReason: reason,
			VerdictReasons: []string{reason}, StartedAt: run.Acceptance.AcceptedAt, FinishedAt: now,
			TopologyResults: recoveredTopology(run), Hosts: recoveredHosts(run), CleanupItems: recoveredCleanups(run),
			Failures: []report.Failure{{Code: reason, Phase: "RECOVERY", Message: "Run was interrupted before a final report was committed"}},
		}
		if status == "FAILED" && value.Verdict == "NOT_READY" {
			value.VerdictReasons = append(value.VerdictReasons, "observed required or topology failure is preserved")
		}
		if err := report.Validate(value); err != nil {
			return fmt.Errorf("build recovery report for %s: %w", id, err)
		}
		if err := s.workspace.WriteJSON(path.Join("runs", id, reportName), value); err != nil {
			return fmt.Errorf("persist recovery report for %s: %w", id, err)
		}
		run.Report = &value
		run.Status = value.RunStatus
		if _, err := s.appendLocked(run, Event{Kind: "TERMINAL_ARCHIVED", Status: status}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recoveredVerdict(run *Run, status string) string {
	hosts := recoveredHosts(run)
	return report.EvaluateVerdict(run.Plan, run.Config.Mode, run.Config.Scope, run.Config.Backend, status, hosts, recoveredTopology(run))
}

func recoveredTopology(run *Run) []topology.Result {
	results := []topology.Result{}
	for _, event := range run.Events {
		if event.Kind == "TOPOLOGY_RESULTS" {
			var current []topology.Result
			if json.Unmarshal(event.Data, &current) == nil {
				results = append(results, current...)
			}
		}
	}
	return results
}

func recoveredHosts(run *Run) []report.HostResult {
	hosts := []report.HostResult{}
	for _, event := range run.Events {
		if event.Kind == "HOST_OUTCOME" {
			var host report.HostResult
			if json.Unmarshal(event.Data, &host) == nil {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

func recoveredCleanups(run *Run) []report.CleanupItem {
	items := []report.CleanupItem{}
	for _, event := range run.Events {
		if event.Kind == "CLEANUP_REGISTERED" || event.Kind == "CLEANUP_UPDATED" {
			var item report.CleanupItem
			if json.Unmarshal(event.Data, &item) == nil {
				items = append(items, item)
			}
		}
	}
	return items
}

func cloneRun(run *Run) Run {
	copy := *run
	copy.Events = append([]Event(nil), run.Events...)
	copy.Config.Inventory = append([]inventory.Host(nil), run.Config.Inventory...)
	copy.Config.Overrides = append([]preview.Override(nil), run.Config.Overrides...)
	copy.Config.RoleMap = make(map[string][]string, len(run.Config.RoleMap))
	for role, hosts := range run.Config.RoleMap {
		copy.Config.RoleMap[role] = append([]string(nil), hosts...)
	}
	if run.Report != nil {
		reportCopy := *run.Report
		copy.Report = &reportCopy
	}
	return copy
}

func randomID() string { return rand.Text() }
