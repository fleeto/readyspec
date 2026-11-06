// Package webui serves the embedded console and its same-origin local API.
package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fleeto/readyspec/internal/template"
	"github.com/fleeto/readyspec/internal/workspace"
)

//go:embed dist
var assets embed.FS

const sessionCookie = "readyspec_session"
const maxBodyBytes = 2*10*1024*1024 + 1024

type Options struct {
	Address        string
	Token          string
	OneTimeToken   bool
	Secure         bool
	AllowedHosts   []string
	Version        string
	WorkspaceLabel string
	Store          *workspace.Store
}

type session struct {
	csrf    string
	expires time.Time
}
type Server struct {
	options       Options
	mux           *http.ServeMux
	mu            sync.Mutex
	sessions      map[string]session
	bootstrapUsed bool
	plansMu       sync.Mutex
}

func New(options Options) (*Server, error) {
	if len(options.Token) < 32 {
		return nil, fmt.Errorf("access token must contain at least 32 characters")
	}
	if _, _, err := net.SplitHostPort(options.Address); err != nil {
		return nil, fmt.Errorf("invalid Web listener address")
	}
	if options.Store == nil {
		return nil, fmt.Errorf("workspace store is required")
	}
	s := &Server{options: options, mux: http.NewServeMux(), sessions: make(map[string]session)}
	s.mux.HandleFunc("POST /api/v1/auth/session", s.authenticate)
	s.mux.HandleFunc("GET /api/v1/system/info", s.authorize(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"version": options.Version, "workspace_label": options.WorkspaceLabel, "executor_available": false})
	}))
	s.mux.HandleFunc("GET /api/v1/plans", s.authorize(s.listPlans))
	s.mux.HandleFunc("POST /api/v1/plans", s.authorize(s.savePlan))
	s.mux.HandleFunc("POST /api/v1/plans/validate", s.authorize(s.validatePlan))
	s.mux.HandleFunc("GET /api/v1/templates", s.authorize(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"items": template.List(), "definitions": template.Catalog()})
	}))
	s.mux.HandleFunc("GET /api/v1/templates/{id}/{version}", s.authorize(func(w http.ResponseWriter, r *http.Request) {
		snapshot, ok := template.Lookup(r.PathValue("id"), r.PathValue("version"))
		if !ok {
			writeError(w, http.StatusNotFound, "TEMPLATE_NOT_FOUND")
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}))
	s.mux.HandleFunc("POST /api/v1/plans/compile", s.authorize(s.compilePlan))
	s.mux.HandleFunc("POST /api/v1/runs/preview", s.authorize(s.previewRun))
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "API_NOT_FOUND") })
	static, err := fs.Sub(assets, "dist")
	if err != nil {
		return nil, err
	}
	files := http.FileServer(http.FS(static))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/assets/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	if !s.validHost(r) {
		writeError(w, http.StatusForbidden, "HOST_REJECTED")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		writeError(w, http.StatusForbidden, "CROSS_ORIGIN_REJECTED")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		scheme := "http"
		if s.options.Secure {
			scheme = "https"
		}
		origin := r.Header.Get("Origin")
		if (origin != "" && origin != scheme+"://"+r.Host) || (r.Method != http.MethodGet && r.Method != http.MethodHead && origin == "") {
			writeError(w, http.StatusForbidden, "ORIGIN_REJECTED")
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// validHost 校验请求 Host 头。回环监听保持严格校验（监听地址、白名单或
// localhost）；非回环监听（面向内网部署）只校验请求端口与监听端口一致，
// 允许通过主机名、网卡 IP、域名等任意入口访问，不再拦截 DNS Rebinding。
func (s *Server) validHost(r *http.Request) bool {
	configured, port, err := net.SplitHostPort(s.options.Address)
	if err != nil {
		return false
	}
	if configuredIP := net.ParseIP(configured); configuredIP == nil || !configuredIP.IsLoopback() {
		_, requestPort, err := net.SplitHostPort(r.Host)
		return err == nil && requestPort == port
	}
	if strings.EqualFold(r.Host, s.options.Address) {
		return true
	}
	for _, host := range s.options.AllowedHosts {
		if strings.EqualFold(r.Host, host) {
			return true
		}
	}
	host, requestPort, err := net.SplitHostPort(r.Host)
	if err != nil || requestPort != port {
		return false
	}
	return host == "localhost"
}

func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(w, r, &body); err != nil {
		requestBodyError(w, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for key, value := range s.sessions {
		if now.After(value.expires) {
			delete(s.sessions, key)
		}
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil && body.Token == "" {
		if current, ok := s.sessions[cookie.Value]; ok {
			writeJSON(w, http.StatusOK, map[string]string{"csrf_token": current.csrf})
			return
		}
	}
	if !constantEqual(body.Token, s.options.Token) || (s.options.OneTimeToken && s.bootstrapUsed) {
		writeError(w, http.StatusUnauthorized, "访问令牌无效或已过期，请使用启动终端中的访问方式。")
		return
	}
	if len(s.sessions) >= 64 {
		writeError(w, http.StatusTooManyRequests, "会话数量已达上限，请稍后重试。")
		return
	}
	key, csrf := rand.Text(), rand.Text()
	s.sessions[key] = session{csrf: csrf, expires: now.Add(8 * time.Hour)}
	s.bootstrapUsed = true
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: key, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.options.Secure, MaxAge: 8 * 60 * 60})
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": csrf})
}

func (s *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "SESSION_REQUIRED")
			return
		}
		s.mu.Lock()
		current, ok := s.sessions[cookie.Value]
		s.mu.Unlock()
		if !ok || time.Now().After(current.expires) {
			writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED")
			return
		}
		if !constantEqual(r.Header.Get("X-CSRF-Token"), current.csrf) {
			writeError(w, http.StatusForbidden, "CSRF_REJECTED")
			return
		}
		next(w, r)
	}
}

func constantEqual(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "application/json" && contentType != "application/json; charset=utf-8" {
		return fmt.Errorf("expected JSON request")
	}
	limit := int64(maxBodyBytes)
	if r.URL.Path == "/api/v1/auth/session" {
		limit = 4096
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func requestBodyError(w http.ResponseWriter, err error) {
	var sizeError *http.MaxBytesError
	if errors.As(err, &sizeError) {
		writeError(w, http.StatusRequestEntityTooLarge, "请求超过大小限制。")
		return
	}
	writeError(w, http.StatusBadRequest, "请求格式无效，请提交单个 JSON 对象。")
}
