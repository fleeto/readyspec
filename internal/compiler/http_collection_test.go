package compiler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
)

func runLocalArtifact(t *testing.T, a compiler.Artifact) []compiler.ItemResult {
	t.Helper()
	payload, err := goss.Binary(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "goss")
	if err := os.WriteFile(binary, payload, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goss.json")
	if err := os.WriteFile(path, a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(binary, "-g", path, "validate", "--format", "json").Output()
	if err != nil && len(out) == 0 {
		t.Fatal(err)
	}
	results, err := compiler.Normalize(a, out)
	if err != nil {
		t.Fatalf("%v output=%s", err, out)
	}
	return results
}

func TestHTTPCollectionEnforcesTotalTimeoutAndHeaderLimit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow-body":
			w.Header().Set("Content-Length", "20")
			w.WriteHeader(200)
			for i := 0; i < 20; i++ {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				if _, err := w.Write([]byte("x")); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		case "/redirect":
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			select {
			case <-r.Context().Done():
				return
			case <-time.After(80 * time.Millisecond):
			}
			if n < 5 {
				http.Redirect(w, r, fmt.Sprintf("/redirect?n=%d", n+1), http.StatusFound)
			} else {
				_, _ = w.Write([]byte("healthy"))
			}
		case "/large-headers":
			for i := 0; i < 20; i++ {
				w.Header().Set(fmt.Sprintf("X-Large-%02d", i), strings.Repeat("x", 4096))
			}
			_, _ = w.Write([]byte("healthy"))
		}
	}))
	defer server.Close()
	for _, tt := range []struct {
		name, path, code string
		body             bool
	}{
		{"slow_body_total_budget", "/slow-body", "TIMEOUT", true},
		{"redirects_share_body_budget", "/redirect", "TIMEOUT", true},
		{"redirects_share_status_budget", "/redirect", "TIMEOUT", false},
		{"body_header_limit", "/large-headers", "HEADERS_TOO_LARGE", true},
		{"status_header_limit", "/large-headers", "HEADERS_TOO_LARGE", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := catalogPlan(t, "N05")
			params := map[string]any{"url": server.URL + tt.path, "status": float64(200), "timeout_ms": float64(250), "follow_redirects": true}
			if tt.code == "HEADERS_TOO_LARGE" {
				params["timeout_ms"] = float64(2000)
			}
			if tt.body {
				params["body_regex"] = ".+"
			}
			p.Roles[0].Items[0].Params = params
			a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
			start := time.Now()
			results := runLocalArtifact(t, a)
			elapsed := time.Since(start)
			if len(results) != 1 || results[0].Status != "ERROR" {
				t.Fatalf("want ERROR(%s), elapsed=%s got %+v", tt.code, elapsed, results)
			}
			if code := evidencePropertyPortable(t, results, "error"); code != tt.code {
				t.Fatalf("want %s got %v", tt.code, code)
			}
			if elapsed > 1500*time.Millisecond {
				t.Fatalf("request exceeded bounded budget by too much: %s", elapsed)
			}
		})
	}
}

func evidencePropertyPortable(t *testing.T, results []compiler.ItemResult, property string) any {
	t.Helper()
	for _, result := range results {
		for _, entry := range result.Evidence {
			if fields, ok := entry.Actual.(map[string]any); ok {
				if value, exists := fields[property]; exists {
					return value
				}
			}
		}
	}
	t.Fatalf("missing %s evidence: %+v", property, results)
	return nil
}

func TestHTTPCollectionHeaderAndRedirectBoundaries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exact-headers", "/over-headers":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer connection.Close()
			prefix := "HTTP/1.1 200 OK\r\nContent-Length: 7\r\nConnection: close\r\nX-Fill: "
			limit := 65536
			if r.URL.Path == "/over-headers" {
				limit++
			}
			_, _ = io.WriteString(connection, prefix+strings.Repeat("x", limit-len(prefix)-4)+"\r\n\r\nhealthy")
		case "/exact-response", "/over-response":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer connection.Close()
			total := 1048576
			if r.URL.Path == "/over-response" {
				total++
			}
			bodySize := total
			prefix := ""
			for i := 0; i < 4; i++ {
				prefix = fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", bodySize)
				bodySize = total - len(prefix)
			}
			_, _ = io.WriteString(connection, prefix+strings.Repeat("x", bodySize-7)+"healthy")
		case "/header-chain":
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			size, _ := strconv.Atoi(r.URL.Query().Get("size"))
			w.Header().Set("X-Fill", strings.Repeat("x", size))
			if n == 0 {
				http.Redirect(w, r, fmt.Sprintf("/header-chain?n=1&size=%d", size), 302)
			} else {
				_, _ = w.Write([]byte("healthy"))
			}
		case "/loop":
			n, _ := strconv.Atoi(r.URL.Query().Get("n"))
			http.Redirect(w, r, fmt.Sprintf("/loop?n=%d", n+1), 302)
		case "/redirect-large-body":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer connection.Close()
			_, _ = io.WriteString(connection, "HTTP/1.1 302 Found\r\nLocation: /healthy\r\nContent-Length: 5000000\r\n\r\n")
		case "/incomplete-body":
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer connection.Close()
			_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nhealthy")
		default:
			_, _ = w.Write([]byte("healthy"))
		}
	}))
	defer server.Close()
	for _, tt := range []struct {
		name, path, status, code string
		follow                   bool
	}{
		{"headers_exact_64k", "/exact-headers", "PASS", "", false},
		{"response_exact_1mib", "/exact-response", "PASS", "", false},
		{"response_one_over", "/over-response", "ERROR", "RESPONSE_TOO_LARGE", false},
		{"headers_one_over", "/over-headers", "ERROR", "HEADERS_TOO_LARGE", false},
		{"cumulative_headers_within_limit", "/header-chain?size=20000", "PASS", "", true},
		{"cumulative_headers_over_limit", "/header-chain?size=40000", "ERROR", "HEADERS_TOO_LARGE", true},
		{"redirect_limit", "/loop", "ERROR", "TOO_MANY_REDIRECTS", true},
		{"redirect_body_not_drained", "/redirect-large-body", "PASS", "", true},
		{"incomplete_body", "/incomplete-body", "ERROR", "HTTP_COLLECTION_ERROR", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := catalogPlan(t, "N05")
			p.Roles[0].Items[0].Params = map[string]any{"url": server.URL + tt.path, "status": float64(200), "body_contains": "healthy", "follow_redirects": tt.follow, "timeout_ms": float64(2000)}
			results := runLocalArtifact(t, selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if tt.code != "" {
				if code := evidencePropertyPortable(t, results, "error"); code != tt.code {
					t.Fatalf("want %s got %v", tt.code, code)
				}
			}
			if tt.name == "headers_exact_64k" && fmt.Sprint(evidencePropertyPortable(t, results, "header_bytes")) != "65536" {
				t.Fatal("header byte boundary not preserved")
			}
			if tt.name == "response_exact_1mib" && fmt.Sprint(evidencePropertyPortable(t, results, "response_bytes")) != "1048576" {
				t.Fatal("combined response boundary not preserved")
			}
			raw, _ := json.Marshal(results)
			if len(raw) > 4096 {
				t.Fatalf("response headers leaked into evidence: %d bytes", len(raw))
			}
		})
	}
}

func TestHTTPCollectionDeclaresCapabilityAndPreservesConfiguredDeadline(t *testing.T) {
	p := catalogPlan(t, "N05")
	p.Roles[0].Items[0].Params = map[string]any{"url": "http://127.0.0.1:8080", "status": float64(200), "timeout_ms": float64(60000)}
	a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
	var config map[string]map[string]map[string]any
	if err := json.Unmarshal(a.Config, &config); err != nil {
		t.Fatal(err)
	}
	commands := config["command"]
	if len(commands) != 1 {
		t.Fatal("status-only HTTP must use bounded collection")
	}
	for _, command := range commands {
		if command["timeout"] != float64(62000) {
			t.Fatalf("outer executor would preempt configured deadline: %v", command["timeout"])
		}
	}
	found := false
	for _, capability := range a.RequiredCapabilities {
		if capability == "tool:python3" {
			found = true
		}
	}
	if !found {
		t.Fatal("HTTP status-only path must declare Python capability")
	}
}

func TestBoundedHTTPCollectionBranches(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exact":
			_, _ = w.Write([]byte(strings.Repeat("x", 1048576)))
		case "/chunked-large":
			w.(http.Flusher).Flush()
			_, _ = w.Write([]byte(strings.Repeat("x", 1048577)))
		case "/redirect":
			http.Redirect(w, r, "/healthy", http.StatusFound)
		default:
			w.Header().Add("X-Health", "ok")
			_, _ = w.Write([]byte("HEALTHY"))
		}
	}))
	defer server.Close()
	for _, tt := range []struct {
		name, path string
		params     map[string]any
		status     string
	}{
		{"body_plus_headers_exceeds_response_limit", "/exact", map[string]any{"body_regex": "^x+$"}, "ERROR"},
		{"chunked_over_limit", "/chunked-large", map[string]any{"body_contains": "x"}, "ERROR"},
		{"redirect_disabled", "/redirect", map[string]any{"body_contains": "HEALTHY"}, "FAIL"},
		{"redirect_enabled", "/redirect", map[string]any{"body_contains": "HEALTHY", "follow_redirects": true}, "PASS"},
		{"head_no_body", "/healthy", map[string]any{"method": "HEAD", "body_contains": "HEALTHY"}, "FAIL"},
		{"body_and_headers", "/healthy", map[string]any{"body_regex": "^HEALTHY$", "headers": map[string]any{"X-Health": "ok"}}, "PASS"},
		{"header_mismatch", "/healthy", map[string]any{"body_contains": "HEALTHY", "headers": map[string]any{"X-Health": "bad"}}, "FAIL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := catalogPlan(t, "N05")
			params := map[string]any{"url": server.URL + tt.path, "status": float64(200)}
			for key, value := range tt.params {
				params[key] = value
			}
			p.Roles[0].Items[0].Params = params
			a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
			results := runLocalArtifact(t, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			raw, _ := json.Marshal(results)
			if len(raw) > 4096 {
				t.Fatalf("HTTP evidence must be summarized, got %d bytes", len(raw))
			}
		})
	}
}

func TestBoundedHTTPCollectionVerifiesTLSByDefault(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("official payload targets Linux")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("HEALTHY")) }))
	defer server.Close()
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	for _, verify := range []bool{true, false} {
		p := catalogPlan(t, "N05")
		p.Roles[0].Items[0].Params = map[string]any{"url": server.URL, "status": float64(200), "body_contains": "HEALTHY", "verify_tls": verify}
		a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
		results := runLocalArtifact(t, a)
		expected := "ERROR"
		if !verify {
			expected = "PASS"
		}
		if len(results) != 1 || results[0].Status != expected {
			t.Fatalf("verify=%v want %s got %+v", verify, expected, results)
		}
	}
}
