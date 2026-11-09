package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/oplog"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/webui"
	"github.com/fleeto/readyspec/internal/workspace"
)

var errAuth = errors.New("AUTH_ERROR")

type serveOptions struct {
	listen, workspace, token, cert, key string
	logLevel, logFormat                 string
	logFile                             string
	allowedHosts                        []string
	noBrowser                           bool
}

func serve(ctx context.Context, options serveOptions, explicitAddress bool, out io.Writer) error {
	logger, logFile, err := oplog.Configure(oplog.Config{Level: options.logLevel, Format: options.logFormat, File: options.logFile}, os.Stderr)
	if err != nil {
		return err
	}
	var shutdownDeadline time.Time
	var deadlineMu sync.Mutex
	if logFile != nil {
		defer func() {
			deadlineMu.Lock()
			deadline := shutdownDeadline
			deadlineMu.Unlock()
			if err := closeLogFileBounded(logFile, deadline); err != nil {
				_, _ = io.WriteString(os.Stderr, "ReadySpec: operational log flush failed\n")
			}
		}()
	}
	slog.SetDefault(logger)
	if (options.cert == "") != (options.key == "") {
		return fmt.Errorf("both --tls-cert and --tls-key are required for HTTPS")
	}
	token := os.Getenv("READYSPEC_AUTH_TOKEN")
	if token == "" {
		token = options.token
	}
	generatedToken := token == ""
	if generatedToken {
		token = rand.Text() + rand.Text()
	}
	if len(token) < 32 {
		return fmt.Errorf("%w: access token must contain at least 32 characters", errAuth)
	}
	dir, err := workspaceDirectory(options.workspace)
	if err != nil {
		return err
	}
	store, err := workspace.Open(dir)
	if err != nil {
		return fmt.Errorf("open workspace: %w", err)
	}
	closeWorkspace := true
	defer func() {
		if closeWorkspace {
			_ = store.Close()
		}
	}()
	lease, err := store.TryLock()
	if err != nil {
		return err
	}
	defer func() {
		if closeWorkspace {
			_ = lease.Close()
		}
	}()
	runs, err := runstore.Open(store)
	if err != nil {
		return fmt.Errorf("recover Run store: %w", err)
	}
	logRunRecovery(logger, runs.List())
	manager, err := runmanager.New(runs, executor.NewNativeSSH())
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.listen)
	if err != nil && !explicitAddress {
		for port := 18481; port <= 18489; port++ {
			listener, err = net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	secure := options.cert != ""
	localToken := rand.Text() + rand.Text()
	serverIDBytes := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", listener.Addr(), os.Getpid(), localToken)))
	serverID := hex.EncodeToString(serverIDBytes[:16])
	localHost, localPort, _ := net.SplitHostPort(listener.Addr().String())
	if ip := net.ParseIP(localHost); ip == nil || ip.IsUnspecified() || !ip.IsLoopback() {
		localHost = "127.0.0.1"
	}
	localAddress := net.JoinHostPort(localHost, localPort)
	handler, err := webui.New(webui.Options{Address: listener.Addr().String(), Token: token, OneTimeToken: generatedToken, Secure: secure, AllowedHosts: options.allowedHosts, Version: Version, WorkspaceLabel: filepath.Base(dir), Store: store, Runs: manager, LocalToken: localToken, ServerID: serverID})
	if err != nil {
		return err
	}
	instance := serverInstance{ServerID: serverID, Address: localAddress, LocalToken: localToken, PID: os.Getpid(), StartedAt: time.Now().UTC(), Secure: secure}
	if err := store.WriteJSON("runtime/server.json", instance); err != nil {
		return fmt.Errorf("persist local server discovery record: %w", err)
	}
	defer func() {
		var current serverInstance
		if store.ReadJSON("runtime/server.json", &current) == nil && current.ServerID == serverID {
			_ = store.Remove("runtime/server.json")
		}
	}()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 * 1024}
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()
	done := make(chan struct{})
	shutdownComplete := make(chan struct{})
	shutdownStarted := make(chan time.Time, 1)
	workerDone := make(chan struct{})
	workerFailure := make(chan error, 1)
	defer close(done)
	go func() {
		defer close(workerDone)
		runErr := manager.Run(ctx)
		if runErr != nil && ctx.Err() == nil {
			handler.StopAcceptingRuns()
			logger.Error("worker stopped", "event", "worker.stopped", "reason_code", "RUN_WORKER_STOPPED")
			workerFailure <- fmt.Errorf("Run manager stopped after a storage or execution error")
			cancel()
		} else if runErr == nil && ctx.Err() == nil {
			handler.StopAcceptingRuns()
			logger.Error("worker stopped", "event", "worker.stopped", "reason_code", "RUN_WORKER_STOPPED")
			workerFailure <- fmt.Errorf("Run manager stopped unexpectedly")
			cancel()
		}
	}()
	go func() {
		defer close(shutdownComplete)
		select {
		case <-ctx.Done():
			deadline := time.Now().Add(5 * time.Second)
			deadlineMu.Lock()
			shutdownDeadline = deadline
			deadlineMu.Unlock()
			shutdownStarted <- deadline
			shutdownCtx, cancel := context.WithTimeout(context.Background(), boundedDuration(time.Until(deadline), 3*time.Second))
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	scheme := "http"
	if secure {
		scheme = "https"
	}
	url := scheme + "://" + net.JoinHostPort(host, port) + "/"
	logger.Info("service started", "event", "service.started", "version", Version, "listen_port", port, "backend", "native_ssh", "worker_state", "running")
	if generatedToken {
		url += "#token=" + token
	}
	fmt.Fprintf(out, "ReadySpec Web UI: %s\n", url)
	if !generatedToken {
		fmt.Fprintln(out, "Use the configured access token to sign in.")
	}
	if !options.noBrowser {
		openBrowser(url)
	}
	if secure {
		err = server.ServeTLS(listener, options.cert, options.key)
	} else {
		err = server.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		deadline := <-shutdownStarted
		<-shutdownComplete
		if !waitUntil(workerDone, deadline, 4*time.Second) {
			closeWorkspace = false
			return fmt.Errorf("Run worker did not stop within shutdown budget")
		}
		select {
		case failure := <-workerFailure:
			logger.Info("service stopped", "event", "service.stopped")
			return failure
		default:
			logger.Info("service stopped", "event", "service.stopped")
			return nil
		}
	}
	handler.StopAcceptingRuns()
	cancel()
	_ = server.Close()
	deadline := <-shutdownStarted
	<-shutdownComplete
	if !waitUntil(workerDone, deadline, 4*time.Second) {
		closeWorkspace = false
		return fmt.Errorf("Run worker did not stop after HTTP listener failure")
	}
	return err
}

func logRunRecovery(logger *slog.Logger, recoveredRuns []runstore.Run) {
	for _, recoveredRun := range recoveredRuns {
		if recoveredRun.RecoveryWarning != "" {
			logger.Warn("Run journal tail recovered", "event", "journal.tail_recovered", "run_id", recoveredRun.Acceptance.RunID, "reason_code", recoveredRun.RecoveryWarning)
		}
		if recoveredRun.RecoveryError != "" {
			logger.Error("Run recovery failed", "event", "run.recovery_failed", "run_id", recoveredRun.Acceptance.RunID, "reason_code", recoveredRun.RecoveryError)
		}
	}
}

func boundedDuration(remaining, maximum time.Duration) time.Duration {
	if remaining <= 0 {
		return time.Nanosecond
	}
	if remaining < maximum {
		return remaining
	}
	return maximum
}

func waitUntil(done <-chan struct{}, deadline time.Time, maximum time.Duration) bool {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return false
	}
	if maximum > 0 && remaining > maximum {
		remaining = maximum
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func closeLogFileBounded(closer io.Closer, deadline time.Time) error {
	budget := time.Second
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("LOG_FLUSH_TIMEOUT")
		}
		if remaining < budget {
			budget = remaining
		}
	}
	done := make(chan error, 1)
	go func() { done <- closer.Close() }()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("LOG_FLUSH_TIMEOUT")
	}
}

type serverInstance struct {
	ServerID   string    `json:"server_id"`
	Address    string    `json:"address"`
	LocalToken string    `json:"local_token"`
	PID        int       `json:"pid"`
	StartedAt  time.Time `json:"started_at"`
	Secure     bool      `json:"secure"`
}

func workspaceDirectory(flag string) (string, error) {
	if value := os.Getenv("READYSPEC_WORKSPACE"); value != "" {
		return filepath.Abs(value)
	}
	if flag != "" {
		return filepath.Abs(flag)
	}
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", fmt.Errorf("LOCALAPPDATA is required; alternatively specify --workspace")
		}
		return filepath.Join(base, "ReadySpec", "workspace"), nil
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Abs(filepath.Join(base, "readyspec", "workspace"))
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return
		}
		cmd = exec.Command("xdg-open", url)
	default:
		return
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}
