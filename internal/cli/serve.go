package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/fleeto/readyspec/internal/webui"
	"github.com/fleeto/readyspec/internal/workspace"
)

var errAuth = errors.New("AUTH_ERROR")

type serveOptions struct {
	listen, workspace, token, cert, key string
	allowedHosts                        []string
	noBrowser                           bool
}

func serve(ctx context.Context, options serveOptions, explicitAddress bool, out io.Writer) error {
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
	defer store.Close()
	lease, err := store.TryLock()
	if err != nil {
		return err
	}
	defer lease.Close()
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
	handler, err := webui.New(webui.Options{Address: listener.Addr().String(), Token: token, OneTimeToken: generatedToken, Secure: secure, AllowedHosts: options.allowedHosts, Version: Version, WorkspaceLabel: filepath.Base(dir), Store: store})
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 * 1024}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	shutdownComplete := make(chan struct{})
	defer close(done)
	go func() {
		defer close(shutdownComplete)
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
		<-shutdownComplete
		return nil
	}
	return err
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
