package webui

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fleeto/readyspec/internal/diagnostic"
	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/planstore"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/publicview"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"golang.org/x/crypto/ssh"
)

type runSubmission struct {
	Plan               string                    `json:"plan"`
	PlanRecordKey      string                    `json:"plan_record_key,omitempty"`
	Inventory          inventoryInput            `json:"inventory"`
	RoleMap            map[string][]string       `json:"role_map"`
	Overrides          []preview.Override        `json:"field_overrides"`
	Mode               string                    `json:"run_mode"`
	Scope              string                    `json:"run_scope"`
	Backend            string                    `json:"executor_backend"`
	IdempotencyKey     string                    `json:"idempotency_key"`
	Auth               runstore.AuthIdentity     `json:"auth_identity"`
	Credentials        map[string]credentialBody `json:"credentials,omitempty"`
	Simulator          executor.Profiles         `json:"simulator_profiles,omitempty"`
	Budgets            runstore.Budgets          `json:"budgets,omitempty"`
	PartialSourceRunID string                    `json:"partial_source_run_id,omitempty"`
	PartialHostIDs     []string                  `json:"partial_host_ids,omitempty"`
}

type inventoryInput struct {
	Source string `json:"source"`
	Format string `json:"format"`
}

type credentialBody struct {
	Password     string `json:"password,omitempty"`
	SudoPassword string `json:"sudo_password,omitempty"`
	PrivateKey   string `json:"private_key,omitempty"`
	Passphrase   string `json:"passphrase,omitempty"`
	AgentSocket  string `json:"agent_socket,omitempty"`
}

type cleanupResolution struct {
	Action           string         `json:"action"`
	AuditReason      string         `json:"audit_reason,omitempty"`
	ConfirmTerminate bool           `json:"confirm_terminate,omitempty"`
	Credential       credentialBody `json:"credential"`
}

type runSummary struct {
	RunID       string    `json:"run_id"`
	PlanID      string    `json:"plan_id"`
	PlanName    string    `json:"plan_name"`
	PlanVersion string    `json:"plan_version"`
	Status      string    `json:"status"`
	AcceptedAt  time.Time `json:"accepted_at"`
	PlanDigest  string    `json:"plan_digest"`
	Verdict     string    `json:"verdict,omitempty"`
	RunMode     string    `json:"run_mode"`
	RunScope    string    `json:"run_scope"`
	Backend     string    `json:"executor_backend"`
}

func (s *Server) submitRun(w http.ResponseWriter, r *http.Request) {
	submissionID, idErr := diagnostic.NewID()
	if idErr != nil {
		writeError(w, http.StatusInternalServerError, "DIAGNOSTIC_ID_UNAVAILABLE")
		return
	}
	if s.runs == nil {
		writeSubmissionError(w, http.StatusServiceUnavailable, submissionID, "RUN_MANAGER_UNAVAILABLE", diagnostic.ForCode("RUN_WORKER_STOPPED", ""))
		return
	}
	if !s.AcceptingRuns() {
		writeSubmissionError(w, http.StatusServiceUnavailable, submissionID, "RUN_WORKER_STOPPED", diagnostic.ForCode("RUN_WORKER_STOPPED", ""))
		return
	}
	var body runSubmission
	if err := readJSON(w, r, &body); err != nil {
		writeSubmissionError(w, requestBodyStatus(err), submissionID, requestBodyCode(err), diagnostic.Item{})
		return
	}
	p, err := plan.Load(strings.NewReader(body.Plan))
	if err != nil {
		writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "PLAN_ERROR", diagnostic.Item{})
		return
	}
	if p.State == "ARCHIVED" || body.Mode == "GATE" && p.State != "RELEASED" {
		writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "PLAN_STATE_NOT_EXECUTABLE", diagnostic.Item{})
		return
	}
	if body.Mode == "GATE" {
		document, marshalErr := plan.DocumentJSON(p)
		var value map[string]any
		if marshalErr != nil || json.Unmarshal(document, &value) != nil || planstore.VerifyManifest(value) != nil {
			writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "RELEASE_MANIFEST_UNVERIFIABLE", diagnostic.Item{})
			return
		}
	}
	if body.PlanRecordKey != "" {
		s.plansMu.Lock()
		defer s.plansMu.Unlock()
		record, recordErr := s.plans.Get(body.PlanRecordKey)
		if recordErr != nil || record.ArchiveSnapshot != nil || record.ReleaseSnapshot == nil || record.ReleaseFingerprint != p.Digest {
			writeSubmissionError(w, http.StatusConflict, submissionID, "PLAN_RECORD_NOT_ACTIVE", diagnostic.Item{})
			return
		}
	}
	format := body.Inventory.Format
	if format == "" {
		format = "quick"
	}
	hosts, err := inventory.Parse(body.Inventory.Source, format)
	if err != nil {
		writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "PLAN_ERROR", diagnostic.Item{})
		return
	}
	if body.Scope == "PARTIAL" {
		parent, parentErr := s.runs.Get(body.PartialSourceRunID)
		if parentErr != nil || parent.Report == nil || parent.Config.PlanDigest != p.Digest {
			writeSubmissionError(w, http.StatusConflict, submissionID, "PARTIAL_SOURCE_RUN_UNAVAILABLE", diagnostic.Item{})
			return
		}
		selected := make(map[string]bool, len(body.PartialHostIDs))
		filtered, validationError := validatePartialSelection(*parent, p.Digest, hosts, body.PartialHostIDs, selected)
		if validationError != "" {
			status := http.StatusUnprocessableEntity
			if validationError == "PARTIAL_SOURCE_RUN_UNAVAILABLE" || validationError == "PARTIAL_INVENTORY_CHANGED" {
				status = http.StatusConflict
			}
			writeSubmissionError(w, status, submissionID, validationError, diagnostic.Item{})
			return
		}
		hosts = filtered
		for roleID, hostIDs := range body.RoleMap {
			kept := hostIDs[:0]
			for _, hostID := range hostIDs {
				if selected[hostID] {
					kept = append(kept, hostID)
				}
			}
			if len(kept) == 0 {
				delete(body.RoleMap, roleID)
			} else {
				body.RoleMap[roleID] = kept
			}
		}
		groupSelected := map[string]bool{}
		for _, host := range hosts {
			for _, group := range host.Groups {
				groupSelected[group] = true
			}
		}
		keptOverrides := body.Overrides[:0]
		for _, override := range body.Overrides {
			if override.Scope == "host" && selected[override.Target] || override.Scope == "group" && groupSelected[override.Target] {
				keptOverrides = append(keptOverrides, override)
			}
		}
		body.Overrides = keptOverrides
		if body.Simulator.Hosts != nil {
			profiles := make(map[string]executor.Profile, len(selected))
			for hostID, profile := range body.Simulator.Hosts {
				if selected[hostID] {
					profiles[hostID] = profile
				}
			}
			body.Simulator.Hosts = profiles
		}
		for hostID := range body.Credentials {
			if !selected[hostID] {
				delete(body.Credentials, hostID)
			}
		}
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = "web-" + rand.Text()
	}
	boundHostIDs := map[string]bool{}
	for _, targets := range body.RoleMap {
		for _, hostID := range targets {
			boundHostIDs[hostID] = true
		}
	}
	for hostID := range body.Credentials {
		if !boundHostIDs[hostID] {
			delete(body.Credentials, hostID)
		}
	}
	credentials := make(map[string]executor.Credential, len(body.Credentials))
	for hostID, input := range body.Credentials {
		if body.Backend == "mock_simulator" {
			continue
		}
		switch body.Auth.Method {
		case "password":
			credentials[hostID] = executor.Credential{Method: "password", Password: []byte(input.Password), SudoPassword: []byte(input.SudoPassword)}
		case "private_key":
			key := []byte(input.PrivateKey)
			signer, parseErr := ssh.ParsePrivateKey(key)
			if parseErr != nil && input.Passphrase != "" {
				passphrase := []byte(input.Passphrase)
				signer, parseErr = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
				for i := range passphrase {
					passphrase[i] = 0
				}
			}
			for i := range key {
				key[i] = 0
			}
			input.PrivateKey, input.Passphrase = "", ""
			body.Credentials[hostID] = input
			if parseErr != nil {
				wipeCredentials(credentials)
				writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "AUTH_ERROR", diagnostic.ForCode("PRIVATE_KEY_PARSE_FAILED", hostID))
				return
			}
			fingerprint := ssh.FingerprintSHA256(signer.PublicKey())
			if body.Auth.PublicKeyFingerprint != "" && body.Auth.PublicKeyFingerprint != fingerprint {
				wipeCredentials(credentials)
				writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "AUTH_ERROR", diagnostic.ForCode("PRIVATE_KEY_IDENTITY_MISMATCH", hostID))
				return
			}
			body.Auth.PublicKeyFingerprint = fingerprint
			credentials[hostID] = executor.Credential{Method: "private_key", Signer: signer, SudoPassword: []byte(input.SudoPassword)}
		case "agent":
			credentials[hostID] = executor.Credential{Method: "agent", AgentSocket: input.AgentSocket, SudoPassword: []byte(input.SudoPassword)}
		default:
			wipeCredentials(credentials)
			writeSubmissionError(w, http.StatusUnprocessableEntity, submissionID, "AUTH_ERROR", diagnostic.ForCode("CREDENTIAL_METHOD_MISMATCH", hostID))
			return
		}
		input.Password = ""
		input.SudoPassword = ""
		input.PrivateKey = ""
		input.Passphrase = ""
		input.AgentSocket = ""
		body.Credentials[hostID] = input
	}
	if body.Backend == "mock_simulator" {
		body.Auth = runstore.AuthIdentity{Method: "offline"}
		wipeCredentials(credentials)
	}
	config := runstore.RunConfig{
		PlanDigest: p.Digest, PlanRecordKey: body.PlanRecordKey,
		InventorySource: body.Inventory.Source, InventoryFormat: format,
		Mode: body.Mode, Scope: body.Scope, Backend: body.Backend,
		Inventory: hosts, RoleMap: body.RoleMap, Overrides: body.Overrides,
		Auth: body.Auth, Simulator: body.Simulator, Budgets: body.Budgets,
		PartialSourceRunID: body.PartialSourceRunID, PartialHostIDs: append([]string(nil), body.PartialHostIDs...),
	}
	s.acceptMu.RLock()
	if !s.accepting {
		s.acceptMu.RUnlock()
		wipeCredentials(credentials)
		writeSubmissionError(w, http.StatusServiceUnavailable, submissionID, "RUN_WORKER_STOPPED", diagnostic.ForCode("RUN_WORKER_STOPPED", ""))
		return
	}
	accepted, created, err := s.runs.Submit(runstore.Request{IdempotencyKey: body.IdempotencyKey, RequestID: w.Header().Get("X-Request-ID"), SubmissionID: submissionID, Plan: p, Config: config}, credentials)
	s.acceptMu.RUnlock()
	if err != nil {
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "WORKSPACE_BUSY") {
			status = http.StatusTooManyRequests
		} else if strings.Contains(err.Error(), "INFRA_ERROR") {
			status = http.StatusInternalServerError
		}
		var diagnosticErr *diagnostic.Error
		if errors.As(err, &diagnosticErr) {
			writeSubmissionError(w, status, submissionID, "AUTH_ERROR", diagnostic.ForCode(diagnosticErr.Code, diagnosticErr.HostID))
		} else {
			writeSubmissionError(w, status, submissionID, safeRunError(err), diagnostic.Item{})
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": accepted.Acceptance.RunID, "created": created, "status": accepted.Status, "request_id": w.Header().Get("X-Request-ID"), "submission_id": submissionID, "acceptance": "accepted"})
}

func writeSubmissionError(w http.ResponseWriter, status int, submissionID, code string, item diagnostic.Item) {
	requestID := w.Header().Get("X-Request-ID")
	body := map[string]any{"error": code, "request_id": requestID, "submission_id": submissionID, "acceptance": "rejected"}
	if item.Code == "" {
		item = diagnostic.ForCode(code, "")
	}
	body["diagnostics"] = []diagnostic.Item{item}
	writeJSON(w, status, body)
}

func (s *Server) submissionStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := readJSON(w, r, &body); err != nil || !validSubmissionKey(body.IdempotencyKey) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"acceptance": "unknown", "request_id": w.Header().Get("X-Request-ID"), "error": "SUBMISSION_KEY_INVALID"})
		return
	}
	if s.runs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"acceptance": "unknown", "request_id": w.Header().Get("X-Request-ID"), "error": "RUN_MANAGER_UNAVAILABLE"})
		return
	}
	accepted, found, err := s.runs.LookupSubmission(body.IdempotencyKey)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"acceptance": "unknown", "request_id": w.Header().Get("X-Request-ID"), "error": "RUN_STORAGE_ERROR"})
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"acceptance": "rejected", "request_id": w.Header().Get("X-Request-ID")})
		return
	}
	response := map[string]any{"acceptance": "accepted", "run_id": accepted.RunID, "request_id": w.Header().Get("X-Request-ID"), "submission_id": accepted.SubmissionID}
	if run, getErr := s.runs.Get(accepted.RunID); getErr != nil {
		response["recovery_error"] = "RUN_STORAGE_ERROR"
	} else {
		response["status"] = run.Status
		if run.RecoveryError != "" {
			response["recovery_error"] = run.RecoveryError
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func validSubmissionKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, value := range key {
		if value < 0x21 || value > 0x7e {
			return false
		}
	}
	return true
}

func wipeCredentials(credentials map[string]executor.Credential) {
	for hostID, value := range credentials {
		for i := range value.Password {
			value.Password[i] = 0
		}
		for i := range value.SudoPassword {
			value.SudoPassword[i] = 0
		}
		delete(credentials, hostID)
	}
}

func safeRunError(err error) string {
	message := err.Error()
	for _, detail := range []string{
		"AUTH_ERROR: required host credential is missing",
		"AUTH_ERROR: required host credential is invalid",
		"AUTH_ERROR: selected local credential is unavailable",
	} {
		if strings.Contains(message, detail) {
			return detail
		}
	}
	for _, marker := range []string{"AUTH_ERROR", "PLAN_ERROR", "PLAN_DIGEST_MISMATCH", "RUN_IDEMPOTENCY_CONFLICT", "WORKSPACE_BUSY", "INFRA_ERROR", "CLEANUP_AUDIT_REASON_REQUIRED", "CLEANUP_ALREADY_RESOLVED", "CLEANUP_NOT_FOUND", "CLEANUP_PROCESSES_REMAIN_ACTIVE"} {
		if strings.Contains(message, marker) {
			return marker
		}
	}
	return "RUN_SUBMISSION_REJECTED"
}

func (s *Server) listRuns(w http.ResponseWriter, _ *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	items := s.runs.List()
	sort.Slice(items, func(i, j int) bool { return items[i].Acceptance.AcceptedAt.After(items[j].Acceptance.AcceptedAt) })
	result := make([]runSummary, 0, len(items))
	for _, run := range items {
		result = append(result, summarize(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) listCleanup(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	type cleanupRow struct {
		RunID      string             `json:"run_id"`
		AuthMethod string             `json:"auth_method"`
		Item       report.CleanupItem `json:"item"`
	}
	rows := []cleanupRow{}
	for _, run := range s.runs.List() {
		for _, item := range s.runs.CleanupItems(run.Acceptance.RunID) {
			if r.URL.Query().Get("state") != "all" && (item.Status == "CLEAN" || item.Status == "IGNORED") {
				continue
			}
			item.OwnerToken = ""
			rows = append(rows, cleanupRow{RunID: run.Acceptance.RunID, AuthMethod: run.Config.Auth.Method, Item: item})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) rerunConfig(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunOrWriteError(w, r.PathValue("id"))
	if err != nil {
		return
	}
	planJSON, err := plan.DocumentJSON(run.Plan)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RUN_CONFIG_UNAVAILABLE")
		return
	}
	partialHosts := []string{}
	if run.Report != nil {
		for hostID := range anomalousHosts(*run.Report) {
			partialHosts = append(partialHosts, hostID)
		}
		sort.Strings(partialHosts)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plan": string(planJSON), "plan_digest": run.Config.PlanDigest,
		"plan_record_key": run.Config.PlanRecordKey,
		"inventory_input": map[string]string{"source": run.Config.InventorySource, "format": run.Config.InventoryFormat},
		"inventory":       run.Config.Inventory, "role_map": run.Config.RoleMap,
		"field_overrides": run.Config.Overrides, "run_mode": run.Config.Mode,
		"run_scope": run.Config.Scope, "executor_backend": run.Config.Backend,
		"simulator_profiles": run.Config.Simulator, "budgets": run.Config.Budgets,
		"auth_identity":         run.Config.Auth,
		"partial_hosts":         partialHosts,
		"partial_source_run_id": run.Config.PartialSourceRunID,
		"partial_host_ids":      run.Config.PartialHostIDs,
	})
}

func anomalousHosts(value report.RunReport) map[string]bool {
	result := make(map[string]bool)
	for _, host := range value.Hosts {
		if len(host.Failures) > 0 {
			result[host.HostID] = true
			continue
		}
		for _, item := range host.ItemResults {
			if item.Status == "FAIL" || item.Status == "ERROR" || item.Status == "UNSUPPORTED" {
				result[host.HostID] = true
				break
			}
		}
	}
	return result
}

func validatePartialSelection(parent runstore.Run, planDigest string, hosts []inventory.Host, requested []string, selected map[string]bool) ([]inventory.Host, string) {
	if parent.Report == nil || parent.Config.PlanDigest != planDigest {
		return nil, "PARTIAL_SOURCE_RUN_UNAVAILABLE"
	}
	allowed := anomalousHosts(*parent.Report)
	for _, hostID := range requested {
		if !allowed[hostID] || selected[hostID] {
			return nil, "PARTIAL_HOST_NOT_ELIGIBLE"
		}
		selected[hostID] = true
	}
	if len(selected) == 0 {
		return nil, "PARTIAL_HOSTS_REQUIRED"
	}
	original := make(map[string]inventory.Host, len(parent.Config.Inventory))
	for _, host := range parent.Config.Inventory {
		original[host.ID] = host
	}
	filtered := make([]inventory.Host, 0, len(selected))
	for _, host := range hosts {
		if !selected[host.ID] {
			continue
		}
		if old, ok := original[host.ID]; !ok || !reflect.DeepEqual(old, host) {
			return nil, "PARTIAL_INVENTORY_CHANGED"
		}
		filtered = append(filtered, host)
	}
	if len(filtered) != len(selected) {
		return nil, "PARTIAL_INVENTORY_CHANGED"
	}
	return filtered, ""
}

func filterPartialInputs(hosts []inventory.Host, roleMap map[string][]string, overrides []preview.Override, selected map[string]bool) ([]inventory.Host, map[string][]string, []preview.Override) {
	filteredHosts := make([]inventory.Host, 0, len(selected))
	groups := make(map[string]bool)
	for _, host := range hosts {
		if !selected[host.ID] {
			continue
		}
		filteredHosts = append(filteredHosts, host)
		for _, group := range host.Groups {
			groups[group] = true
		}
	}
	filteredRoles := make(map[string][]string)
	for roleID, ids := range roleMap {
		for _, id := range ids {
			if selected[id] {
				filteredRoles[roleID] = append(filteredRoles[roleID], id)
			}
		}
	}
	filteredOverrides := make([]preview.Override, 0, len(overrides))
	for _, override := range overrides {
		if override.Scope == "host" && selected[override.Target] || override.Scope == "group" && groups[override.Target] {
			filteredOverrides = append(filteredOverrides, override)
		}
	}
	return filteredHosts, filteredRoles, filteredOverrides
}

func (s *Server) resolveCleanupGlobal(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	cleanupID := r.PathValue("cleanupID")
	var runID string
	for _, run := range s.runs.List() {
		for _, item := range s.runs.CleanupItems(run.Acceptance.RunID) {
			if item.CleanupID == cleanupID {
				if runID != "" {
					writeError(w, http.StatusConflict, "CLEANUP_ID_AMBIGUOUS")
					return
				}
				runID = run.Acceptance.RunID
			}
		}
	}
	if runID == "" {
		writeError(w, http.StatusNotFound, "CLEANUP_NOT_FOUND")
		return
	}
	r.SetPathValue("id", runID)
	s.resolveCleanup(w, r)
}

func (s *Server) parseInventory(w http.ResponseWriter, r *http.Request) {
	var body inventoryInput
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	format := body.Format
	if format == "" {
		format = "quick"
	}
	hosts, err := inventory.Parse(body.Source, format)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "INVENTORY_INVALID")
		return
	}
	groups := map[string][]string{}
	for _, host := range hosts {
		for _, group := range host.Groups {
			groups[group] = append(groups[group], host.ID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts, "groups": groups})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunOrWriteError(w, r.PathValue("id"))
	if err == nil {
		projection := projectionWithEvents(*run, int64(len(run.Events)))
		if run.RecoveryError != "" {
			projection["recovery_error"] = run.RecoveryError
		}
		if run.RecoveryWarning != "" {
			projection["recovery_warning"] = run.RecoveryWarning
		}
		writeJSON(w, http.StatusOK, projection)
	}
}

func (s *Server) getRunReport(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunOrWriteError(w, r.PathValue("id"))
	if err != nil {
		return
	}
	if run.Report == nil {
		if run.RecoveryError != "" {
			writeError(w, http.StatusInternalServerError, "RUN_STORAGE_ERROR")
			return
		}
		writeError(w, http.StatusConflict, "RUN_REPORT_NOT_READY")
		return
	}
	copy := *run.Report
	copy.CleanupItems = append([]report.CleanupItem{}, run.Report.CleanupItems...)
	for i := range copy.CleanupItems {
		copy.CleanupItems[i].OwnerToken = ""
	}
	writeJSON(w, http.StatusOK, copy)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	run, err := s.runs.Cancel(r.PathValue("id"))
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, runstore.ErrRunNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, safeRunError(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": run.Acceptance.RunID, "status": run.Status})
}

func (s *Server) resolveCleanup(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	var body cleanupResolution
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	runID, cleanupID := r.PathValue("id"), r.PathValue("cleanupID")
	run, err := s.runs.Get(runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND")
		return
	}
	if body.Action == "IGNORE" {
		if strings.TrimSpace(body.AuditReason) == "" {
			writeError(w, http.StatusUnprocessableEntity, "CLEANUP_AUDIT_REASON_REQUIRED")
			return
		}
		item, err := s.runs.ResolveCleanup(r.Context(), runID, cleanupID, executor.Credential{}, false, body.AuditReason)
		if err != nil {
			writeError(w, http.StatusConflict, safeRunError(err))
			return
		}
		item.OwnerToken = ""
		writeJSON(w, http.StatusOK, item)
		return
	}
	if body.Action != "CLEAN" || run.Config.Auth.Method == "offline" {
		writeError(w, http.StatusUnprocessableEntity, "CLEANUP_ACTION_INVALID")
		return
	}
	credential := executor.Credential{Method: run.Config.Auth.Method}
	credential.SudoPassword = []byte(body.Credential.SudoPassword)
	defer func() {
		for i := range credential.Password {
			credential.Password[i] = 0
		}
		credential.Password = nil
		credential.Signer = nil
		credential.AgentSocket = ""
		for i := range credential.SudoPassword {
			credential.SudoPassword[i] = 0
		}
		credential.SudoPassword = nil
		body.Credential = credentialBody{}
	}()
	switch run.Config.Auth.Method {
	case "password":
		credential.Password = []byte(body.Credential.Password)
	case "private_key":
		key := []byte(body.Credential.PrivateKey)
		signer, parseErr := ssh.ParsePrivateKey(key)
		if parseErr != nil && body.Credential.Passphrase != "" {
			passphrase := []byte(body.Credential.Passphrase)
			signer, parseErr = ssh.ParsePrivateKeyWithPassphrase(key, passphrase)
			for i := range passphrase {
				passphrase[i] = 0
			}
		}
		for i := range key {
			key[i] = 0
		}
		body.Credential.PrivateKey, body.Credential.Passphrase = "", ""
		if parseErr != nil {
			writeError(w, http.StatusUnprocessableEntity, "AUTH_ERROR")
			return
		}
		credential.Signer = signer
	case "agent":
		credential.AgentSocket = body.Credential.AgentSocket
	default:
		writeError(w, http.StatusUnprocessableEntity, "AUTH_ERROR")
		return
	}
	item, err := s.runs.ResolveCleanup(r.Context(), runID, cleanupID, credential, body.ConfirmTerminate, "")
	if err != nil {
		status := http.StatusConflict
		if strings.Contains(err.Error(), "AUTH_ERROR") {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, safeRunError(err))
		return
	}
	item.OwnerToken = ""
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) getRunOrWriteError(w http.ResponseWriter, id string) (*runstore.Run, error) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return nil, errors.New("manager unavailable")
	}
	run, err := s.runs.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND")
		return nil, err
	}
	return run, nil
}

func summarize(run runstore.Run) runSummary {
	result := runSummary{
		RunID: run.Acceptance.RunID, PlanID: run.Plan.PlanID, PlanName: run.Plan.Name, PlanVersion: run.Plan.Version,
		Status: run.Status, AcceptedAt: run.Acceptance.AcceptedAt,
		PlanDigest: run.Config.PlanDigest, RunMode: run.Config.Mode, RunScope: run.Config.Scope,
		Backend: run.Config.Backend,
	}
	if run.Report != nil {
		result.Verdict = run.Report.Verdict
	}
	return result
}

func sanitizeEvents(events []runstore.Event) []runstore.Event {
	result := append([]runstore.Event(nil), events...)
	for i := range result {
		result[i] = sanitizeEvent(result[i])
	}
	return result
}

func sanitizeEvent(event runstore.Event) runstore.Event {
	return publicview.EventValue(event)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		writeError(w, http.StatusInternalServerError, "SSE_UNAVAILABLE")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "SSE_UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	var cursor int64
	lastID, afterSeq := r.Header.Get("Last-Event-ID"), r.URL.Query().Get("after_seq")
	if lastID != "" && afterSeq != "" && lastID != afterSeq {
		writeError(w, http.StatusBadRequest, "EVENT_CURSOR_CONFLICT")
		return
	}
	if lastID == "" && afterSeq == "" {
		run, _, sequence, _, err := s.runs.ReadEvents(r.PathValue("id"), 0)
		if err != nil {
			writeError(w, http.StatusNotFound, "RUN_NOT_FOUND")
			return
		}
		cursor = sequence
		snapshot := projectRun(*run, sequence)
		if err := writeSSE(w, flusher, "snapshot", sequence, snapshot); err != nil {
			return
		}
	} else {
		valueText := lastID
		if valueText == "" {
			valueText = afterSeq
		}
		value, err := strconv.ParseInt(valueText, 10, 64)
		if err != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_EVENT_CURSOR")
			return
		}
		cursor = value
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		run, events, sequence, reset, err := s.runs.ReadEvents(r.PathValue("id"), cursor)
		if err != nil {
			return
		}
		if reset {
			cursor = sequence
			snapshot := projectRun(*run, sequence)
			if err := writeSSE(w, flusher, "reset", sequence, snapshot); err != nil {
				return
			}
		} else {
			for _, event := range events {
				payload, _ := json.Marshal(sanitizeEvent(event))
				var delta map[string]any
				_ = json.Unmarshal(payload, &delta)
				delta["projection"] = projectRun(*run, event.Sequence)
				if err := writeSSE(w, flusher, event.Kind, event.Sequence, delta); err != nil {
					return
				}
				cursor = event.Sequence
			}
		}
		if run.Report != nil {
			return
		}
		if run.RecoveryError != "" {
			_ = writeSSE(w, flusher, "recovery_error", sequence, map[string]any{"code": run.RecoveryError, "run_id": run.Acceptance.RunID})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
		}
	}
}

type hostPhaseProjection struct {
	HostID string    `json:"host_id"`
	Mode   string    `json:"execution_mode,omitempty"`
	Phase  string    `json:"phase"`
	At     time.Time `json:"at"`
}

type stageTransitionProjection struct {
	Sequence  int64      `json:"sequence"`
	HostID    string     `json:"host_id"`
	Mode      string     `json:"execution_mode,omitempty"`
	Phase     string     `json:"phase"`
	Attempt   int        `json:"attempt"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Omitted   bool       `json:"omitted,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}

func projectRun(run runstore.Run, sequence int64) map[string]any {
	latest := map[string]hostPhaseProjection{}
	transitions := make([]stageTransitionProjection, 0)
	phaseEvents := make([]runstore.Event, 0)
	for _, event := range run.Events {
		if event.Sequence > sequence {
			continue
		}
		if event.Kind != "PHASE" || event.HostID == "" || event.Status == "" {
			continue
		}
		phaseEvents = append(phaseEvents, event)
		// FINISHED is a boundary written when host processing ends. It closes
		// the preceding stage; it is not a stage that began at that timestamp.
		if event.Status == "FINISHED" {
			continue
		}
		key := event.HostID + "\x00" + event.Mode
		phase := event.Status
		if event.Mode != "" {
			phase = strings.TrimSuffix(phase, "_"+event.Mode)
		}
		latest[key] = hostPhaseProjection{HostID: event.HostID, Mode: event.Mode, Phase: phase, At: event.At}
	}
	for index, event := range phaseEvents {
		if event.Status == "FINISHED" {
			continue
		}
		phase := event.Status
		if event.Mode != "" {
			phase = strings.TrimSuffix(phase, "_"+event.Mode)
		}
		transition := stageTransitionProjection{
			Sequence: event.Sequence, HostID: event.HostID, Mode: event.Mode, Phase: phase,
			Attempt: event.Attempt, StartedAt: &event.At,
		}
		if transition.Attempt < 1 {
			transition.Attempt = 1
		}
		for next := index + 1; next < len(phaseEvents); next++ {
			if phaseEvents[next].HostID == event.HostID {
				endedAt := phaseEvents[next].At
				transition.EndedAt = &endedAt
				break
			}
		}
		transitions = append(transitions, transition)
	}
	boundHosts := map[string]bool{}
	for _, hostIDs := range run.Config.RoleMap {
		for _, hostID := range hostIDs {
			boundHosts[hostID] = true
		}
	}
	for _, host := range run.Config.Inventory {
		if !boundHosts[host.ID] {
			continue
		}
		plannedModes := plannedExecutionModes(run.Plan, run.Config.RoleMap, host.ID)
		for _, mode := range []string{"STANDARD", "PRIVILEGED"} {
			if plannedModes[mode] {
				continue
			}
			transitions = append(transitions, stageTransitionProjection{
				HostID: host.ID, Mode: mode, Phase: "OMITTED", Attempt: 1,
				Omitted: true, Reason: "NO_ENABLED_ITEMS",
			})
		}
	}
	sort.Slice(transitions, func(i, j int) bool {
		if transitions[i].HostID != transitions[j].HostID {
			return transitions[i].HostID < transitions[j].HostID
		}
		if transitions[i].Sequence != transitions[j].Sequence {
			return transitions[i].Sequence < transitions[j].Sequence
		}
		if transitions[i].Mode != transitions[j].Mode {
			return transitions[i].Mode < transitions[j].Mode
		}
		return transitions[i].Phase < transitions[j].Phase
	})
	phases := make([]hostPhaseProjection, 0, len(latest))
	for _, phase := range latest {
		phases = append(phases, phase)
	}
	sort.Slice(phases, func(i, j int) bool {
		if phases[i].HostID == phases[j].HostID {
			return phases[i].Mode < phases[j].Mode
		}
		return phases[i].HostID < phases[j].HostID
	})
	projection := map[string]any{"run": summarize(run), "sequence": sequence, "host_phases": phases, "stage_transitions": transitions}
	if run.RecoveryError != "" {
		projection["recovery_error"] = run.RecoveryError
	}
	if run.RecoveryWarning != "" {
		projection["recovery_warning"] = run.RecoveryWarning
	}
	return projection
}

func plannedExecutionModes(value *plan.Plan, roleMap map[string][]string, hostID string) map[string]bool {
	modes := map[string]bool{}
	if value == nil {
		return modes
	}
	boundRoles := map[string]bool{}
	for roleID, hostIDs := range roleMap {
		for _, candidate := range hostIDs {
			if candidate == hostID {
				boundRoles[roleID] = true
				break
			}
		}
	}
	templateModes := map[string]string{}
	for _, snapshot := range value.EmbeddedTemplates {
		templateModes[snapshot.TemplateID+"/"+snapshot.Version] = snapshot.ExecutionMode
	}
	for _, role := range value.Roles {
		if !boundRoles[role.RoleID] {
			continue
		}
		for _, item := range role.Items {
			if !item.Enabled {
				continue
			}
			mode := templateModes[item.TemplateID+"/"+item.TemplateVersion]
			if mode != "" {
				modes[mode] = true
			}
		}
	}
	return modes
}

func projectionWithEvents(run runstore.Run, sequence int64) map[string]any {
	projection := projectRun(run, sequence)
	projection["events"] = sanitizeEvents(run.Events)
	return projection
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, name string, sequence int64, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", sequence, name, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

var _ = context.Background
