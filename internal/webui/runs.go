package webui

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runstore"
	"golang.org/x/crypto/ssh"
)

type runSubmission struct {
	Plan           string                    `json:"plan"`
	Inventory      inventoryInput            `json:"inventory"`
	RoleMap        map[string][]string       `json:"role_map"`
	Overrides      []preview.Override        `json:"field_overrides"`
	Mode           string                    `json:"run_mode"`
	Scope          string                    `json:"run_scope"`
	Backend        string                    `json:"executor_backend"`
	IdempotencyKey string                    `json:"idempotency_key"`
	Auth           runstore.AuthIdentity     `json:"auth_identity"`
	Credentials    map[string]credentialBody `json:"credentials,omitempty"`
	Simulator      executor.Profiles         `json:"simulator_profiles,omitempty"`
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
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	AcceptedAt time.Time `json:"accepted_at"`
	PlanDigest string    `json:"plan_digest"`
	Verdict    string    `json:"verdict,omitempty"`
	RunMode    string    `json:"run_mode"`
	RunScope   string    `json:"run_scope"`
	Backend    string    `json:"executor_backend"`
}

func (s *Server) submitRun(w http.ResponseWriter, r *http.Request) {
	if s.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "RUN_MANAGER_UNAVAILABLE")
		return
	}
	var body runSubmission
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	p, err := plan.Load(strings.NewReader(body.Plan))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "PLAN_ERROR: invalid plan")
		return
	}
	format := body.Inventory.Format
	if format == "" {
		format = "quick"
	}
	hosts, err := inventory.Parse(body.Inventory.Source, format)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "PLAN_ERROR: invalid inventory")
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = "web-" + rand.Text()
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
				writeError(w, http.StatusUnprocessableEntity, "AUTH_ERROR: invalid private key")
				return
			}
			fingerprint := ssh.FingerprintSHA256(signer.PublicKey())
			if body.Auth.PublicKeyFingerprint != "" && body.Auth.PublicKeyFingerprint != fingerprint {
				wipeCredentials(credentials)
				writeError(w, http.StatusUnprocessableEntity, "AUTH_ERROR: selected public key identity does not match")
				return
			}
			body.Auth.PublicKeyFingerprint = fingerprint
			credentials[hostID] = executor.Credential{Method: "private_key", Signer: signer, SudoPassword: []byte(input.SudoPassword)}
		case "agent":
			credentials[hostID] = executor.Credential{Method: "agent", AgentSocket: input.AgentSocket, SudoPassword: []byte(input.SudoPassword)}
		default:
			wipeCredentials(credentials)
			writeError(w, http.StatusUnprocessableEntity, "AUTH_ERROR: unsupported authentication method")
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
		PlanDigest: p.Digest, Mode: body.Mode, Scope: body.Scope, Backend: body.Backend,
		Inventory: hosts, RoleMap: body.RoleMap, Overrides: body.Overrides,
		Auth: body.Auth, Simulator: body.Simulator,
	}
	accepted, created, err := s.runs.Submit(runstore.Request{IdempotencyKey: body.IdempotencyKey, Plan: p, Config: config}, credentials)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if strings.Contains(err.Error(), "WORKSPACE_BUSY") {
			status = http.StatusTooManyRequests
		} else if strings.Contains(err.Error(), "INFRA_ERROR") {
			status = http.StatusInternalServerError
		}
		writeError(w, status, safeRunError(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": accepted.Acceptance.RunID, "created": created, "status": accepted.Status})
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
	result := make([]runSummary, 0, len(items))
	for _, run := range items {
		result = append(result, summarize(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) listCleanup(w http.ResponseWriter, _ *http.Request) {
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
			if item.Status == "CLEAN" || item.Status == "IGNORED" {
				continue
			}
			item.OwnerToken = ""
			rows = append(rows, cleanupRow{RunID: run.Acceptance.RunID, AuthMethod: run.Config.Auth.Method, Item: item})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunOrWriteError(w, r.PathValue("id"))
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"run": summarize(*run), "events": sanitizeEvents(run.Events)})
	}
}

func (s *Server) getRunReport(w http.ResponseWriter, r *http.Request) {
	run, err := s.getRunOrWriteError(w, r.PathValue("id"))
	if err != nil {
		return
	}
	if run.Report == nil {
		writeError(w, http.StatusConflict, "RUN_REPORT_NOT_READY")
		return
	}
	copy := *run.Report
	copy.CleanupItems = append([]report.CleanupItem(nil), run.Report.CleanupItems...)
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
			status = http.StatusUnauthorized
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
		RunID: run.Acceptance.RunID, Status: run.Status, AcceptedAt: run.Acceptance.AcceptedAt,
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
	if event.Kind == "CLEANUP_REGISTERED" || event.Kind == "CLEANUP_UPDATED" {
		var item report.CleanupItem
		if json.Unmarshal(event.Data, &item) == nil {
			item.OwnerToken = ""
			event.Data, _ = json.Marshal(item)
		}
	}
	return event
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
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		run, _, sequence, _, err := s.runs.ReadEvents(r.PathValue("id"), 0)
		if err != nil {
			writeError(w, http.StatusNotFound, "RUN_NOT_FOUND")
			return
		}
		cursor = sequence
		snapshot := map[string]any{"run": summarize(*run), "sequence": sequence}
		if err := writeSSE(w, flusher, "snapshot", sequence, snapshot); err != nil {
			return
		}
	} else {
		value, err := strconv.ParseInt(lastID, 10, 64)
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
			snapshot := map[string]any{"run": summarize(*run), "sequence": sequence}
			if err := writeSSE(w, flusher, "reset", sequence, snapshot); err != nil {
				return
			}
		} else {
			for _, event := range events {
				if err := writeSSE(w, flusher, event.Kind, event.Sequence, sanitizeEvent(event)); err != nil {
					return
				}
				cursor = event.Sequence
			}
		}
		if run.Report != nil {
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
