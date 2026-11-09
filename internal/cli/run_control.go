package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/publicview"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/workspace"
	"github.com/spf13/cobra"
)

func newRunControlCommands(workspacePath func() string, setExitCode func(int)) []*cobra.Command {
	status := &cobra.Command{Use: "status <run-id>", Short: "Read durable Run status", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if data, err := localRunRequest(cmd.Context(), workspacePath(), http.MethodGet, "/api/v1/runs/"+url.PathEscape(args[0]), nil); err == nil {
			_, writeErr := cmd.OutOrStdout().Write(append(data, '\n'))
			return writeErr
		} else if isLocalServiceResponse(err) {
			return err
		}
		store, closeStore, err := openRunStore(workspacePath())
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		run, err := store.Get(args[0])
		if err != nil {
			setExitCode(13)
			return fmt.Errorf("RUN_NOT_FOUND")
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(publicview.RunValue(*run))
	}}
	cancel := &cobra.Command{Use: "cancel <run-id>", Short: "Request Run cancellation", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		body := []byte(`{}`)
		if data, err := localRunRequest(cmd.Context(), workspacePath(), http.MethodPost, "/api/v1/runs/"+url.PathEscape(args[0])+"/cancel", body); err == nil {
			_, writeErr := cmd.OutOrStdout().Write(append(data, '\n'))
			return writeErr
		} else if isLocalServiceResponse(err) {
			return err
		}
		store, closeStore, err := openRunStore(workspacePath())
		if err != nil {
			setExitCode(13)
			return err
		}
		defer closeStore()
		manager, err := runmanager.New(store, executor.NewNativeSSH())
		if err != nil {
			setExitCode(13)
			return err
		}
		run, err := manager.Cancel(args[0])
		if err != nil {
			setExitCode(13)
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"run_id": run.Acceptance.RunID, "status": run.Status})
	}}
	return []*cobra.Command{status, cancel}
}

func localRunRequest(ctx context.Context, workspacePath, method, endpoint string, body []byte) ([]byte, error) {
	dir, err := workspaceDirectory(workspacePath)
	if err != nil {
		return nil, err
	}
	ws, err := workspace.Open(dir)
	if err != nil {
		return nil, err
	}
	defer ws.Close()
	var instance serverInstance
	if err := ws.ReadJSON("runtime/server.json", &instance); err != nil || instance.ServerID == "" || instance.LocalToken == "" {
		return nil, fmt.Errorf("no active local server")
	}
	scheme := "http"
	if instance.Secure {
		scheme = "https"
	}
	base, err := url.Parse(scheme + "://" + instance.Address)
	if err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(base.Host)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fmt.Errorf("delegation address is not loopback")
	}
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base.String(), "/")+endpoint, input)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-ReadySpec-Local-Token", instance.LocalToken)
	req.Header.Set("Host", base.Host)
	req.Header.Set("Origin", base.Scheme+"://"+base.Host)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		return nil, fmt.Errorf("LOCAL_SERVICE_RESPONSE: %s", failure.Error)
	}
	return data, nil
}

func isLocalServiceResponse(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "LOCAL_SERVICE_RESPONSE:")
}
