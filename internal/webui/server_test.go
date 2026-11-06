package webui_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/webui"
	"github.com/fleeto/readyspec/internal/workspace"
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
	body, _ = json.Marshal(map[string]any{"source": string(document), "draft_key": result.DraftKey})
	w = request(s, "POST", "/api/v1/plans", string(body), cookie, csrf)
	if w.Code != http.StatusOK {
		t.Fatalf("invalid parameter draft cannot be saved: %d %s", w.Code, w.Body.String())
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
		Key string `json:"draft_key"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	source := strings.Replace(draftSource, "plan_id: web-demo", "plan_id: renamed", 1)
	body, _ = json.Marshal(map[string]any{"source": source, "draft_key": saved.Key})
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
