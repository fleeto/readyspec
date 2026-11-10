package webui_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/template"
	"github.com/fleeto/readyspec/internal/webui"
	"github.com/fleeto/readyspec/internal/workspace"
	"golang.org/x/crypto/ssh"
)

const address = "127.0.0.1:18480"
const token = "test-access-token-with-at-least-32-characters"

func newServer(t *testing.T) *webui.Server {
	s, _ := newServerWithStore(t)
	return s
}

func newServerWithStore(t *testing.T) (*webui.Server, *workspace.Store) {
	t.Helper()
	store, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	s, err := webui.New(webui.Options{Address: address, Token: token, Store: store, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return s, store
}

func TestRejectsCrossOriginHostAndCSRFBypasses(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	for _, tt := range []struct{ name, host, origin, csrf, site string }{
		{"host rebinding", "attacker.invalid:18480", "http://attacker.invalid:18480", csrf, ""},
		{"cross origin", address, "https://attacker.invalid", csrf, ""},
		{"missing csrf", address, "http://" + address, "", ""},
		{"same site sibling", address, "http://" + address, csrf, "same-site"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+address+"/api/v1/plans", nil)
			r.Host = tt.host
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("X-CSRF-Token", tt.csrf)
			r.Header.Set("Sec-Fetch-Site", tt.site)
			r.AddCookie(cookie)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("bypass returned %d", w.Code)
			}
		})
	}
	if w := request(s, "POST", "/api/v1/auth/session", `{"token":"wrong"}`, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("incorrect token returned %d", w.Code)
	}
	w := request(s, "POST", "/api/v1/auth/session", `{}`, cookie, "")
	if w.Code != http.StatusOK {
		t.Fatalf("cookie session cannot resume after page reload: %d", w.Code)
	}
}

func TestEveryHTTPRejectionHasRequestID(t *testing.T) {
	s := newServer(t)
	r := httptest.NewRequest(http.MethodGet, "http://"+address+"/api/v1/plans", nil)
	r.Host = "attacker.invalid:18480"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	id := w.Header().Get("X-Request-ID")
	if len(id) != 32 || w.Code != http.StatusForbidden {
		t.Fatalf("missing request correlation header: id=%q status=%d", id, w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["request_id"] != id {
		t.Fatalf("response request_id=%v, header=%q", body["request_id"], id)
	}
}

func TestSystemInfoReportsActiveSSHHostKeyPolicy(t *testing.T) {
	_, ws := newServerWithStore(t)
	for _, policy := range []string{"", "strict"} {
		s, err := webui.New(webui.Options{Address: address, Token: token, Store: ws, HostKeyPolicy: policy})
		if err != nil {
			t.Fatal(err)
		}
		cookie, csrf := login(t, s)
		response := request(s, "GET", "/api/v1/system/info", "", cookie, csrf)
		want := policy
		if want == "" {
			want = "record-only"
		}
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ssh_host_key_policy":"`+want+`"`) {
			t.Fatalf("policy not exposed: %s", response.Body.String())
		}
	}
}

func TestLoopbackDelegationTokenIsBoundToLocalPeerAndServerIdentity(t *testing.T) {
	_, ws := newServerWithStore(t)
	server, err := webui.New(webui.Options{Address: address, Token: token, LocalToken: "local-delegation-secret-value-012345", ServerID: "instance-1", Store: ws, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, remote, localToken string
		want                     int
	}{
		{"loopback token", "127.0.0.1:1234", "local-delegation-secret-value-012345", http.StatusOK},
		{"remote peer", "192.0.2.8:1234", "local-delegation-secret-value-012345", http.StatusUnauthorized},
		{"wrong token", "127.0.0.1:1234", "wrong", http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+address+"/api/v1/system/info", nil)
			r.RemoteAddr = tt.remote
			r.Header.Set("Origin", "http://"+address)
			r.Header.Set("X-ReadySpec-Local-Token", tt.localToken)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status %d, body %s", w.Code, w.Body.String())
			}
			if tt.want == http.StatusOK && !strings.Contains(w.Body.String(), `"server_id":"instance-1"`) {
				t.Fatalf("missing instance identity: %s", w.Body.String())
			}
		})
	}
}

func TestRunSubmitReportAndSSEUseRunManager(t *testing.T) {
	_, ws := newServerWithStore(t)
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	profiles := executor.Profiles{Hosts: map[string]executor.Profile{
		"node": {Preflight: "READY", MachineID: "machine-1", BootID: "boot-1", HostKey: "SHA256:test", Arch: "x86_64", UID: 1000},
	}}
	manager, err := runmanager.New(store, executor.Simulator{Profiles: profiles})
	if err != nil {
		t.Fatal(err)
	}
	serverWithRuns, err := webui.New(webui.Options{Address: address, Token: token, Store: ws, Runs: manager, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf := login(t, serverWithRuns)
	source := `schema_version: readyspec.plan/v1
plan_id: web-run
name: Web Run
software_name: demo
software_version: '1'
version: '1.0.0'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: 1, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`
	body, err := json.Marshal(map[string]any{
		"plan": source, "inventory": map[string]string{"source": "node=127.0.0.1", "format": "quick"},
		"role_map": map[string][]string{"app": {"node"}}, "field_overrides": []any{},
		"run_mode": "TRIAL", "run_scope": "FULL", "executor_backend": "mock_simulator",
		"idempotency_key": "web-api-test", "auth_identity": map[string]string{"method": "offline"},
		"simulator_profiles": profiles, "budgets": map[string]any{"run_seconds": 300},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := request(serverWithRuns, "POST", "/api/v1/runs", string(body), cookie, csrf)
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit returned %d: %s", w.Code, w.Body.String())
	}
	var accepted struct {
		RunID        string `json:"run_id"`
		RequestID    string `json:"request_id"`
		SubmissionID string `json:"submission_id"`
		Acceptance   string `json:"acceptance"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil || accepted.RunID == "" {
		t.Fatalf("invalid submit response: %v %s", err, w.Body.String())
	}
	if accepted.Acceptance != "accepted" || len(accepted.RequestID) != 32 || len(accepted.SubmissionID) != 32 || w.Header().Get("X-Request-ID") != accepted.RequestID {
		t.Fatalf("submit response omitted correlation/acceptance metadata: %s", w.Body.String())
	}
	retryAccepted := request(serverWithRuns, "POST", "/api/v1/runs", string(body), cookie, csrf)
	var retry struct {
		RunID        string `json:"run_id"`
		Created      bool   `json:"created"`
		RequestID    string `json:"request_id"`
		SubmissionID string `json:"submission_id"`
		Acceptance   string `json:"acceptance"`
	}
	if retryAccepted.Code != http.StatusAccepted || json.Unmarshal(retryAccepted.Body.Bytes(), &retry) != nil || retry.RunID != accepted.RunID || retry.Created || retry.Acceptance != "accepted" || len(retry.RequestID) != 32 || len(retry.SubmissionID) != 32 || retry.RequestID == accepted.RequestID || retry.SubmissionID == accepted.SubmissionID {
		t.Fatalf("idempotent retry did not report this attempt and original Run: status=%d body=%s", retryAccepted.Code, retryAccepted.Body.String())
	}
	statusBody, _ := json.Marshal(map[string]string{"idempotency_key": "web-api-test"})
	metadata := request(serverWithRuns, "GET", "/api/v1/runs/"+accepted.RunID+"/display-metadata", "", cookie, csrf)
	if metadata.Code != http.StatusOK || !strings.Contains(metadata.Body.String(), `"run_id":"`+accepted.RunID+`"`) || !strings.Contains(metadata.Body.String(), `"items":[]`) || strings.Contains(metadata.Body.String(), "idempotency") {
		t.Fatalf("display metadata: %d %s", metadata.Code, metadata.Body.String())
	}
	unauthorized := request(serverWithRuns, "GET", "/api/v1/runs/"+accepted.RunID+"/display-metadata", "", nil, "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("metadata exposed without session: %d", unauthorized.Code)
	}
	statusResponse := request(serverWithRuns, "POST", "/api/v1/runs/submission-status", string(statusBody), cookie, csrf)
	var statusResult struct {
		Acceptance string `json:"acceptance"`
		RunID      string `json:"run_id"`
	}
	if statusResponse.Code != http.StatusOK || json.Unmarshal(statusResponse.Body.Bytes(), &statusResult) != nil || statusResult.Acceptance != "accepted" || statusResult.RunID != accepted.RunID {
		t.Fatalf("submission recovery lookup failed: status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	failureBody := map[string]any{
		"plan": source, "inventory": map[string]string{"source": "node=127.0.0.1", "format": "quick"},
		"role_map": map[string][]string{"app": {"node"}}, "field_overrides": []any{}, "run_mode": "TRIAL", "run_scope": "FULL",
		"executor_backend": "native_ssh", "idempotency_key": "invalid-private-key", "auth_identity": map[string]string{"method": "private_key"},
		"credentials": map[string]any{"node": map[string]string{"private_key": "not a private key"}},
	}
	failureJSON, err := json.Marshal(failureBody)
	if err != nil {
		t.Fatal(err)
	}
	rejected := request(serverWithRuns, "POST", "/api/v1/runs", string(failureJSON), cookie, csrf)
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid credential returned %d: %s", rejected.Code, rejected.Body.String())
	}
	var rejectedBody struct {
		Acceptance   string `json:"acceptance"`
		RequestID    string `json:"request_id"`
		SubmissionID string `json:"submission_id"`
		Diagnostics  []struct {
			Code   string `json:"code"`
			HostID string `json:"host_id"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(rejected.Body.Bytes(), &rejectedBody); err != nil {
		t.Fatal(err)
	}
	if rejectedBody.Acceptance != "rejected" || len(rejectedBody.RequestID) != 32 || len(rejectedBody.SubmissionID) != 32 || len(rejectedBody.Diagnostics) != 1 || rejectedBody.Diagnostics[0].Code != "PRIVATE_KEY_PARSE_FAILED" || rejectedBody.Diagnostics[0].HostID != "node" {
		t.Fatalf("credential rejection lacks safe structured diagnosis: %s", rejected.Body.String())
	}
	acceptedCount := len(store.List())
	for _, tc := range []struct {
		name        string
		method      string
		fingerprint string
		privateKey  string
		wantCode    string
	}{
		{name: "unknown authentication method", method: "unknown", wantCode: "CREDENTIAL_METHOD_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input map[string]any
			encoded, _ := json.Marshal(failureBody)
			if err := json.Unmarshal(encoded, &input); err != nil {
				t.Fatal(err)
			}
			input["idempotency_key"] = "early-reject-" + strings.ReplaceAll(tc.name, " ", "-")
			input["auth_identity"].(map[string]any)["method"] = tc.method
			body, _ := json.Marshal(input)
			response := request(serverWithRuns, "POST", "/api/v1/runs", string(body), cookie, csrf)
			var responseBody struct {
				Acceptance   string `json:"acceptance"`
				RequestID    string `json:"request_id"`
				SubmissionID string `json:"submission_id"`
				Diagnostics  []struct {
					Code string `json:"code"`
				} `json:"diagnostics"`
			}
			if response.Code != http.StatusUnprocessableEntity || json.Unmarshal(response.Body.Bytes(), &responseBody) != nil || responseBody.Acceptance != "rejected" || len(responseBody.RequestID) != 32 || len(responseBody.SubmissionID) != 32 || len(responseBody.Diagnostics) != 1 || responseBody.Diagnostics[0].Code != tc.wantCode {
				t.Fatalf("early credential rejection lacked safe correlation/diagnostic: status=%d body=%s", response.Code, response.Body.String())
			}
			if len(store.List()) != acceptedCount {
				t.Fatal("early credential rejection persisted a Run")
			}
		})
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyBlock, err := ssh.MarshalPrivateKey(privateKey, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherSigner, err := ssh.NewSignerFromKey(otherPrivate)
	if err != nil {
		t.Fatal(err)
	}
	var mismatch map[string]any
	mismatchEncoded, _ := json.Marshal(failureBody)
	if err := json.Unmarshal(mismatchEncoded, &mismatch); err != nil {
		t.Fatal(err)
	}
	mismatch["idempotency_key"] = "private-key-identity-mismatch"
	mismatch["auth_identity"].(map[string]any)["method"] = "private_key"
	mismatch["auth_identity"].(map[string]any)["public_key_fingerprint"] = ssh.FingerprintSHA256(otherSigner.PublicKey())
	mismatch["credentials"].(map[string]any)["node"].(map[string]any)["private_key"] = string(pem.EncodeToMemory(keyBlock))
	mismatchBody, _ := json.Marshal(mismatch)
	mismatchResponse := request(serverWithRuns, "POST", "/api/v1/runs", string(mismatchBody), cookie, csrf)
	var mismatchResult struct {
		Acceptance  string `json:"acceptance"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if mismatchResponse.Code != http.StatusUnprocessableEntity || json.Unmarshal(mismatchResponse.Body.Bytes(), &mismatchResult) != nil || mismatchResult.Acceptance != "rejected" || len(mismatchResult.Diagnostics) != 1 || mismatchResult.Diagnostics[0].Code != "PRIVATE_KEY_IDENTITY_MISMATCH" {
		t.Fatalf("private key identity mismatch was not rejected safely: status=%d body=%s", mismatchResponse.Code, mismatchResponse.Body.String())
	}
	if len(store.List()) != acceptedCount {
		t.Fatal("private key identity mismatch persisted a Run")
	}
	rerunResponse := request(serverWithRuns, "GET", "/api/v1/runs/"+accepted.RunID+"/rerun-config", "", cookie, csrf)
	if rerunResponse.Code != http.StatusOK {
		t.Fatalf("rerun config returned %d: %s", rerunResponse.Code, rerunResponse.Body.String())
	}
	var rerun struct {
		InventoryInput map[string]string `json:"inventory_input"`
		Simulator      executor.Profiles `json:"simulator_profiles"`
		Budgets        runstore.Budgets  `json:"budgets"`
	}
	if err := json.Unmarshal(rerunResponse.Body.Bytes(), &rerun); err != nil {
		t.Fatal(err)
	}
	if rerun.InventoryInput["source"] != "node=127.0.0.1" || rerun.InventoryInput["format"] != "quick" {
		t.Fatalf("rerun lost original inventory input: %+v", rerun.InventoryInput)
	}
	if rerun.Simulator.Hosts["node"].MachineID != "machine-1" || rerun.Budgets.RunSeconds != 300 {
		t.Fatalf("rerun lost simulator/budget inputs: simulator=%+v budgets=%+v", rerun.Simulator, rerun.Budgets)
	}
	cleanup := report.CleanupItem{CleanupID: "web-cleanup-test", HostID: "node", MachineID: "machine-1", BootID: "boot-1", Status: "PENDING", RemotePath: "/tmp/readyspec-test-owner", OwnerToken: "0123456789abcdef0123456789abcdef"}
	if err := store.CleanupRegistered(context.Background(), accepted.RunID, cleanup); err != nil {
		t.Fatal(err)
	}
	slowWriter := &blockingSSEWriter{header: make(http.Header), entered: make(chan struct{}), release: make(chan struct{})}
	slowRequest := httptest.NewRequest("GET", "http://"+address+"/api/v1/runs/"+accepted.RunID+"/events", nil)
	slowRequest.Header.Set("Origin", "http://"+address)
	slowRequest.Header.Set("X-CSRF-Token", csrf)
	slowRequest.AddCookie(cookie)
	slowDone := make(chan struct{})
	go func() { defer close(slowDone); serverWithRuns.ServeHTTP(slowWriter, slowRequest) }()
	select {
	case <-slowWriter.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow SSE client did not reach snapshot write")
	}
	if ok, err := manager.ProcessNext(context.Background()); !ok || err != nil {
		t.Fatalf("process accepted Run: ok=%v err=%v", ok, err)
	}
	close(slowWriter.release)
	select {
	case <-slowDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE stream did not finish after the client resumed reading")
	}
	if !strings.Contains(slowWriter.body.String(), "event: STATE") {
		t.Fatal("event created while the snapshot was blocked was lost")
	}
	reportResponse := request(serverWithRuns, "GET", "/api/v1/runs/"+accepted.RunID+"/report", "", cookie, csrf)
	if reportResponse.Code != http.StatusOK || !strings.Contains(reportResponse.Body.String(), `"verdict":"UNKNOWN"`) || !strings.Contains(reportResponse.Body.String(), `"auth_method":"offline"`) || !strings.Contains(reportResponse.Body.String(), `"login_uid":1000`) || strings.Contains(reportResponse.Body.String(), `"cleanup_items":null`) {
		t.Fatalf("report response %d: %s", reportResponse.Code, reportResponse.Body.String())
	}
	if strings.Contains(reportResponse.Body.String(), cleanup.OwnerToken) {
		t.Fatal("cleanup owner token leaked through report API")
	}
	cleanupList := request(serverWithRuns, "GET", "/api/v1/cleanup", "", cookie, csrf)
	if cleanupList.Code != http.StatusOK || strings.Contains(cleanupList.Body.String(), cleanup.OwnerToken) || !strings.Contains(cleanupList.Body.String(), cleanup.CleanupID) {
		t.Fatalf("cleanup listing %d: %s", cleanupList.Code, cleanupList.Body.String())
	}
	ignoreBody, _ := json.Marshal(map[string]any{"action": "IGNORE", "audit_reason": "operator verified that no cleanup is required"})
	resolved := request(serverWithRuns, "POST", "/api/v1/runs/"+accepted.RunID+"/cleanup/"+cleanup.CleanupID, string(ignoreBody), cookie, csrf)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"status":"IGNORED"`) || strings.Contains(resolved.Body.String(), cleanup.OwnerToken) {
		t.Fatalf("cleanup resolution %d: %s", resolved.Code, resolved.Body.String())
	}
	cleanupList = request(serverWithRuns, "GET", "/api/v1/cleanup", "", cookie, csrf)
	if strings.Contains(cleanupList.Body.String(), cleanup.CleanupID) {
		t.Fatalf("resolved item remained pending: %s", cleanupList.Body.String())
	}
	auditList := request(serverWithRuns, "GET", "/api/v1/cleanups?state=all", "", cookie, csrf)
	if auditList.Code != http.StatusOK || !strings.Contains(auditList.Body.String(), cleanup.CleanupID) || !strings.Contains(auditList.Body.String(), "operator verified that no cleanup is required") || strings.Contains(auditList.Body.String(), cleanup.OwnerToken) {
		t.Fatalf("cleanup audit listing %d: %s", auditList.Code, auditList.Body.String())
	}
	events := request(serverWithRuns, "GET", "/api/v1/runs/"+accepted.RunID+"/events", "", cookie, csrf)
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), "event: snapshot") {
		t.Fatalf("SSE snapshot response %d: %s", events.Code, events.Body.String())
	}
	resetRequest := httptest.NewRequest("GET", "http://"+address+"/api/v1/runs/"+accepted.RunID+"/events", nil)
	resetRequest.Header.Set("Origin", "http://"+address)
	resetRequest.Header.Set("Last-Event-ID", "999999")
	resetRequest.Header.Set("X-CSRF-Token", csrf)
	resetRequest.AddCookie(cookie)
	resetResponse := httptest.NewRecorder()
	serverWithRuns.ServeHTTP(resetResponse, resetRequest)
	if resetResponse.Code != http.StatusOK || !strings.Contains(resetResponse.Body.String(), "event: reset") {
		t.Fatalf("stale SSE cursor did not receive reset: %d %s", resetResponse.Code, resetResponse.Body.String())
	}
	var secondSubmission map[string]any
	if err := json.Unmarshal(body, &secondSubmission); err != nil {
		t.Fatal(err)
	}
	secondSubmission["idempotency_key"] = "web-api-cancel-test"
	secondBody, err := json.Marshal(secondSubmission)
	if err != nil {
		t.Fatal(err)
	}
	secondAccepted := request(serverWithRuns, "POST", "/api/v1/runs", string(secondBody), cookie, csrf)
	if secondAccepted.Code != http.StatusAccepted {
		t.Fatalf("second submit returned %d: %s", secondAccepted.Code, secondAccepted.Body.String())
	}
	var secondRun struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(secondAccepted.Body.Bytes(), &secondRun); err != nil || secondRun.RunID == "" {
		t.Fatalf("invalid second submit response: %v %s", err, secondAccepted.Body.String())
	}
	cancelled := request(serverWithRuns, "POST", "/api/v1/runs/"+secondRun.RunID+"/cancel", `{}`, cookie, csrf)
	if cancelled.Code != http.StatusAccepted || !strings.Contains(cancelled.Body.String(), `"status":"CANCELLED"`) {
		t.Fatalf("cancel response %d: %s", cancelled.Code, cancelled.Body.String())
	}
}

type blockingSSEWriter struct {
	header  http.Header
	body    bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockingSSEWriter) Header() http.Header { return w.header }
func (*blockingSSEWriter) WriteHeader(int)       {}
func (w *blockingSSEWriter) Write(value []byte) (int, error) {
	w.once.Do(func() { close(w.entered); <-w.release })
	return w.body.Write(value)
}
func (*blockingSSEWriter) Flush() {}

const draftSource = `schema_version: readyspec.plan/v1
plan_id: web-demo
name: Web draft
software_name: demo
software_version: '1'
version: '1'
state: DRAFT
topology_policy: {allowed_arch: [x86_64]}
roles: [{role_id: app, name: App, min_nodes: 1, max_nodes: null, allow_colocation: false, items: []}]
embedded_templates: []
template_lock: {}
`

func TestNonLoopbackListenAcceptsAnyHostWithMatchingPort(t *testing.T) {
	store, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	s, err := webui.New(webui.Options{Address: "0.0.0.0:8989", Token: token, Store: store, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(host string) int {
		r := httptest.NewRequest("GET", "http://"+host+"/api/v1/plans", nil)
		r.Header.Set("Origin", "http://"+host)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code
	}
	if code := call("warehouse:8989"); code != http.StatusUnauthorized {
		t.Fatalf("hostname access rejected: %d", code)
	}
	if code := call("10.20.1.5:8989"); code != http.StatusUnauthorized {
		t.Fatalf("interface IP access rejected: %d", code)
	}
	if code := call("attacker.invalid:8989"); code != http.StatusUnauthorized {
		t.Fatalf("unknown hostname must not be host-rejected on non-loopback listen: %d", code)
	}
	if code := call("warehouse:9999"); code != http.StatusForbidden {
		t.Fatalf("wrong port must remain rejected: %d", code)
	}
	if code := call("warehouse"); code != http.StatusForbidden {
		t.Fatalf("missing port must remain rejected: %d", code)
	}
}

func TestValidatedDraftCanBeSavedAndLoaded(t *testing.T) {
	s, store := newServerWithStore(t)
	cookie, csrf := login(t, s)
	body, _ := json.Marshal(map[string]any{"source": draftSource, "normalize_draft": true})
	w := request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	w = request(s, "GET", "/api/v1/plans", "", cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"plan_id":"web-demo"`) {
		t.Fatalf("saved plan missing: %d %s", w.Code, w.Body.String())
	}
	restarted, err := webui.New(webui.Options{Address: address, Token: token, Store: store, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cookie, csrf = login(t, restarted)
	w = request(restarted, "GET", "/api/v1/plans", "", cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"plan_id":"web-demo"`) {
		t.Fatalf("plan did not survive server recreation: %d %s", w.Code, w.Body.String())
	}
}

func TestTemplateCatalogAndStaticCompilation(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	w := request(s, "GET", "/api/v1/templates", "", cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"template_id":"S01"`) {
		t.Fatalf("catalog unavailable: %d %s", w.Code, w.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"source": draftSource})
	w = request(s, "POST", "/api/v1/plans/compile", string(body), cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"digest":"sha256:`) {
		t.Fatalf("compile failed: %d %s", w.Code, w.Body.String())
	}
}

func TestStaticRunPreviewDoesNotConnectAndReportsConfiguredRoot(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	body, _ := json.Marshal(map[string]any{"source": draftSource, "inventory": map[string]any{"format": "ini", "source": "[app]\nnode ansible_host=192.0.2.1 ansible_user=root\n"}, "role_map": map[string]any{"app": []string{"node"}}})
	w := request(s, "POST", "/api/v1/runs/preview", string(body), cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"network_accessed":false`) || !strings.Contains(w.Body.String(), `"configured_root_hosts":["node"]`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestValidationAPIProvidesSafeCapacityFieldAndUnknownHostDiagnostics(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	snapshot, _ := template.Lookup("H03", "1.0.0")
	raw, _ := json.Marshal([]any{snapshot})
	source := strings.Replace(draftSource, "embedded_templates: []", "embedded_templates: "+string(raw), 1)
	source = strings.Replace(source, "items: []", `items: [{item_id: memory, name: Memory, template_id: H03, template_version: '1.0.0', template_digest: '', importance: REQUIRED, enabled: true, params: {min_memory: 4G-private-value}, explicit_null_params: [], overridable_params: []}]`, 1)
	body, _ := json.Marshal(map[string]any{"source": source, "normalize_draft": true})
	w := request(s, "POST", "/api/v1/plans/validate", string(body), cookie, csrf)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"CAPACITY_FORMAT"`) || !strings.Contains(w.Body.String(), `"field":"/roles/0/items/0/params/min_memory"`) || strings.Contains(w.Body.String(), "4G-private-value") {
		t.Fatalf("unsafe or unlocated capacity response: %d %s", w.Code, w.Body.String())
	}
	body, _ = json.Marshal(map[string]any{"source": draftSource, "inventory": map[string]any{"format": "quick", "source": "node=192.0.2.1"}, "role_map": map[string][]string{"app": {"missing"}}})
	w = request(s, "POST", "/api/v1/runs/preview", string(body), cookie, csrf)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"code":"UNKNOWN_HOST"`) || !strings.Contains(w.Body.String(), `"field":"/role_map"`) {
		t.Fatalf("unknown host response: %d %s", w.Code, w.Body.String())
	}
}

func TestDraftSaveAndExportValidationNeverExecuteShell(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	source := strings.Replace(draftSource, "items: []", `items: [{item_id: shell-check, name: Shell, template_id: shell, template_version: '1', template_digest: '', importance: REQUIRED, enabled: true, params: {}, explicit_null_params: [], overridable_params: []}]`, 1)
	marker := t.TempDir() + "/must-not-exist"
	template := map[string]any{"schema_version": "readyspec.template/v1", "template_id": "shell", "version": "1", "name": "Shell", "source": "PLAN_LOCAL", "domain": "R", "execution_mode": "STANDARD", "evidence_mode": "ACTIVE_PROBE", "params_schema": map[string]any{"type": "object"}, "implementation": map[string]any{"kind": "CUSTOM_SHELL", "command": "touch '" + marker + "'", "expect_exit_code": 0}, "digest": ""}
	raw, _ := json.Marshal([]any{template})
	source = strings.Replace(source, "embedded_templates: []", "embedded_templates: "+string(raw), 1)
	body, _ := json.Marshal(map[string]any{"source": source, "normalize_draft": true})
	w := request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("cannot author shell draft: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Document map[string]any `json:"document"`
		Digest   string         `json:"digest"`
		DraftKey string         `json:"draft_key"`
		Revision string         `json:"revision"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Digest != "" || result.DraftKey == "" {
		t.Fatal("saving a draft must return its record key without claiming validation")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("static operation executed shell: %v", err)
	}
	document, _ := json.Marshal(result.Document)
	body, _ = json.Marshal(map[string]any{"source": string(document), "normalize_draft": true})
	w = request(s, "POST", "/api/v1/plans/validate", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("draft cannot be prepared for export: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Digest, "sha256:") {
		t.Fatal("strict export validation must compute a semantic digest")
	}
	document, _ = json.Marshal(result.Document)
	body, _ = json.Marshal(map[string]any{"source": string(document), "normalize_draft": false})
	w = request(s, "POST", "/api/v1/plans/validate", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("normalized export cannot be strictly reimported: %d %s", w.Code, w.Body.String())
	}
	// Parameter mistakes remain editable/persistable, but cannot be exported as valid.
	result.Document["embedded_templates"].([]any)[0].(map[string]any)["params_schema"] = map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "integer"}}}
	result.Document["roles"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["params"] = map[string]any{"count": "unfinished"}
	document, _ = json.Marshal(result.Document)
	body, _ = json.Marshal(map[string]any{"source": string(document), "draft_key": result.DraftKey, "expected_revision": result.Revision})
	w = request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("invalid parameter draft cannot be saved: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(map[string]any{"source": string(document), "normalize_draft": true})
	w = request(s, "POST", "/api/v1/plans/validate", string(body), cookie, csrf)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid parameter draft passed strict validation: %d", w.Code)
	}
}

func TestStrictImportDoesNotResignTamperedPlan(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	source := draftSource + "digest: sha256:" + strings.Repeat("0", 64) + "\n"
	body, _ := json.Marshal(map[string]any{"source": source, "normalize_draft": false})
	w := request(s, "POST", "/api/v1/plans/validate", string(body), cookie, csrf)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tampered plan accepted: %d %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(source)) {
		t.Fatal("source document leaked in error response")
	}
}

func TestServesEmbeddedUIWithSecurityHeaders(t *testing.T) {
	s := newServer(t)
	w := request(s, "GET", "/", "", nil, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `<div id="root">`) {
		t.Fatalf("embedded UI unavailable: %d", w.Code)
	}
	if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing browser security headers")
	}
}

func TestBootstrapTokenIsOneTimeButCookieCanResume(t *testing.T) {
	_, store := newServerWithStore(t)
	s, err := webui.New(webui.Options{Address: address, Token: token, OneTimeToken: true, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	cookie, _ := login(t, s)
	if w := request(s, "POST", "/api/v1/auth/session", `{"token":"`+token+`"}`, nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap token reused: %d", w.Code)
	}
	if w := request(s, "POST", "/api/v1/auth/session", `{}`, cookie, ""); w.Code != http.StatusOK {
		t.Fatalf("existing cookie could not resume: %d", w.Code)
	}
}

func TestRejectsOversizedAuthBodyAndMissingOrigin(t *testing.T) {
	s := newServer(t)
	if w := request(s, "POST", "/api/v1/auth/session", `{"token":"`+strings.Repeat("x", 5000)+`"}`, nil, ""); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized auth request returned %d", w.Code)
	}
	r := httptest.NewRequest("POST", "http://"+address+"/api/v1/auth/session", strings.NewReader(`{"token":"`+token+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unsafe request without Origin returned %d", w.Code)
	}
}

func TestIncompleteDraftSurvivesSaveAndServerRecreation(t *testing.T) {
	for _, entry := range []struct{ name, source string }{
		{"empty name", strings.Replace(draftSource, "name: Web draft", "name: ''", 1)},
		{"empty software", strings.Replace(draftSource, "software_name: demo", "software_name: ''", 1)},
		{"unfinished identity", strings.Replace(strings.Replace(draftSource, "plan_id: web-demo", "plan_id: ''", 1), "\nversion: '1'", "\nversion: ''", 1)},
	} {
		t.Run(entry.name, func(t *testing.T) {
			s, store := newServerWithStore(t)
			cookie, csrf := login(t, s)
			body, _ := json.Marshal(map[string]any{"source": entry.source, "normalize_draft": true})
			w := request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
			if w.Code != http.StatusOK {
				t.Fatalf("incomplete draft rejected: %d %s", w.Code, w.Body.String())
			}
			var saved struct {
				DraftKey  string         `json:"draft_key"`
				Document  map[string]any `json:"document"`
				Validated bool           `json:"validated"`
				Digest    string         `json:"digest"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
				t.Fatal(err)
			}
			if saved.DraftKey == "" || saved.Validated || saved.Digest != "" {
				t.Fatalf("saving an incomplete draft must not claim validation: %+v", saved)
			}
			restarted, err := webui.New(webui.Options{Address: address, Token: token, Store: store})
			if err != nil {
				t.Fatal(err)
			}
			cookie, csrf = login(t, restarted)
			w = request(restarted, "GET", "/api/v1/plans", "", cookie, csrf)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), saved.DraftKey) {
				t.Fatalf("cannot reopen incomplete draft: %d %s", w.Code, w.Body.String())
			}
			w = request(restarted, "POST", "/api/v1/plans/validate", string(body), cookie, csrf)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("incomplete draft incorrectly passed validation: %d", w.Code)
			}
		})
	}
}

func TestDraftRecordKeepsIdentityWhenPlanFieldsChange(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	body, _ := json.Marshal(map[string]any{"source": draftSource})
	w := request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var saved struct {
		Key      string `json:"draft_key"`
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	source := strings.Replace(draftSource, "plan_id: web-demo", "plan_id: renamed", 1)
	body, _ = json.Marshal(map[string]any{"source": source, "draft_key": saved.Key, "expected_revision": saved.Revision})
	w = request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("cannot update same draft: %d %s", w.Code, w.Body.String())
	}
	w = request(s, "GET", "/api/v1/plans", "", cookie, csrf)
	var list struct {
		Items []any `json:"items"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 {
		t.Fatalf("renaming duplicated draft record: %s", w.Body.String())
	}
}

func TestDraftSaveStillRejectsUnsafeShape(t *testing.T) {
	s := newServer(t)
	cookie, csrf := login(t, s)
	for _, source := range []string{draftSource + "ssh_password: must-not-persist\n", strings.Replace(draftSource, "items: []", "items: null", 1)} {
		body, _ := json.Marshal(map[string]any{"source": source})
		w := request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("unsafe draft shape accepted: %d", w.Code)
		}
	}
}

func request(s *webui.Server, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+address+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://"+address)
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func login(t *testing.T, s *webui.Server) (*http.Cookie, string) {
	t.Helper()
	w := request(s, "POST", "/api/v1/auth/session", `{"token":"`+token+`"}`, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("login status=%d: %s", w.Code, w.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || body.CSRF == "" {
		t.Fatal("missing protected session cookie or CSRF token")
	}
	return cookies[0], body.CSRF
}

func TestSessionRequiredForWorkspaceAccess(t *testing.T) {
	s := newServer(t)
	if w := request(s, "GET", "/api/v1/plans", "", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated access returned %d", w.Code)
	}
	cookie, csrf := login(t, s)
	w := request(s, "GET", "/api/v1/plans", "", cookie, csrf)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("cannot list empty workspace: %d %s", w.Code, w.Body.String())
	}
}
