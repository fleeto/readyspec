package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fleeto/readyspec/internal/diagnostic"
	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/preview"
	"github.com/fleeto/readyspec/internal/report"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

func newSubmitCommand(workspacePath func() string, setExitCode func(int)) *cobra.Command {
	var planFile, inventoryInput, inventoryFormat, overridesFile string
	var expectedDigest, reportOutput string
	var idempotencyKey string
	var noWait bool
	var lockWait time.Duration
	var mode, scope, backend, authMethod, keyFile, keyFingerprint, profileFile string
	var roleFlags []string
	command := &cobra.Command{
		Use: "submit", Short: "Accept and execute an inspection Run",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var privateKeyHandoff, privateKeyPassphrase []byte
			defer func() {
				for _, secret := range [][]byte{privateKeyHandoff, privateKeyPassphrase} {
					for i := range secret {
						secret[i] = 0
					}
				}
			}()
			p, err := loadPlan(planFile)
			if err != nil {
				setExitCode(12)
				return err
			}
			if expectedDigest != "" && expectedDigest != p.Digest {
				setExitCode(15)
				return fmt.Errorf("PLAN_DIGEST_MISMATCH: expected digest does not match validated plan")
			}
			source, format, err := loadInventorySource(inventoryInput, inventoryFormat)
			if err != nil {
				setExitCode(12)
				return err
			}
			hosts, err := inventory.Parse(source, format)
			if err != nil {
				setExitCode(12)
				return err
			}
			roleMap, err := parseRoleFlags(roleFlags)
			if err != nil {
				setExitCode(12)
				return err
			}
			var overrides []preview.Override
			if overridesFile != "" {
				raw, readErr := readBoundedFile(overridesFile)
				if readErr != nil {
					setExitCode(12)
					return readErr
				}
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&overrides); err != nil || decoder.Decode(new(any)) != io.EOF {
					setExitCode(12)
					return fmt.Errorf("PLAN_ERROR: invalid field override JSON")
				}
			}
			if backend == "" {
				backend = "native_ssh"
			}
			implementation := executor.Executor(nil)
			credentials := map[string]executor.Credential{}
			auth := runstore.AuthIdentity{Method: authMethod}
			switch backend {
			case "native_ssh":
				native := executor.NewNativeSSH()
				implementation = native
				switch authMethod {
				case "password":
					password := []byte(os.Getenv("READYSPEC_SSH_PASSWORD"))
					if len(password) == 0 {
						setExitCode(15)
						return fmt.Errorf("AUTH_ERROR: READYSPEC_SSH_PASSWORD is required")
					}
					for _, host := range hosts {
						sudoPassword := []byte(os.Getenv("READYSPEC_SUDO_PASSWORD"))
						credentials[host.ID] = executor.Credential{Method: "password", Password: append([]byte(nil), password...), SudoPassword: append([]byte(nil), sudoPassword...)}
						for i := range sudoPassword {
							sudoPassword[i] = 0
						}
					}
					for i := range password {
						password[i] = 0
					}
				case "private_key":
					if keyFile == "" {
						setExitCode(15)
						return fmt.Errorf("AUTH_ERROR: --private-key is required")
					}
					keyBytes, err := readBoundedFile(keyFile)
					if err != nil {
						setExitCode(15)
						return fmt.Errorf("AUTH_ERROR: private key cannot be read")
					}
					signer, parseErr := ssh.ParsePrivateKey(keyBytes)
					if parseErr != nil {
						passphrase := []byte(os.Getenv("READYSPEC_SSH_KEY_PASSPHRASE"))
						privateKeyPassphrase = append([]byte(nil), passphrase...)
						signer, parseErr = ssh.ParsePrivateKeyWithPassphrase(keyBytes, passphrase)
						for i := range passphrase {
							passphrase[i] = 0
						}
					}
					privateKeyHandoff = append([]byte(nil), keyBytes...)
					for i := range keyBytes {
						keyBytes[i] = 0
					}
					if parseErr != nil {
						setExitCode(15)
						return fmt.Errorf("AUTH_ERROR: private key cannot be parsed")
					}
					auth.PublicKeyFingerprint = ssh.FingerprintSHA256(signer.PublicKey())
					sudoPassword := []byte(os.Getenv("READYSPEC_SUDO_PASSWORD"))
					for _, host := range hosts {
						credentials[host.ID] = executor.Credential{Method: "private_key", Signer: signer, SudoPassword: append([]byte(nil), sudoPassword...)}
					}
					for i := range sudoPassword {
						sudoPassword[i] = 0
					}
				case "agent":
					socket := os.Getenv("SSH_AUTH_SOCK")
					if socket == "" || keyFingerprint == "" {
						setExitCode(15)
						return fmt.Errorf("AUTH_ERROR: SSH_AUTH_SOCK and --public-key-fingerprint are required")
					}
					auth.PublicKeyFingerprint = keyFingerprint
					sudoPassword := []byte(os.Getenv("READYSPEC_SUDO_PASSWORD"))
					for _, host := range hosts {
						credentials[host.ID] = executor.Credential{Method: "agent", AgentSocket: socket, SudoPassword: append([]byte(nil), sudoPassword...)}
					}
					for i := range sudoPassword {
						sudoPassword[i] = 0
					}
				default:
					setExitCode(15)
					return fmt.Errorf("AUTH_ERROR: choose password, private_key, or agent authentication")
				}
			case "mock_simulator":
				auth.Method = "offline"
				if profileFile == "" {
					setExitCode(12)
					return fmt.Errorf("PLAN_ERROR: --profiles is required for mock_simulator")
				}
				raw, readErr := readBoundedFile(profileFile)
				if readErr != nil {
					setExitCode(12)
					return readErr
				}
				var profiles executor.Profiles
				decoder := json.NewDecoder(bytes.NewReader(raw))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&profiles); err != nil || decoder.Decode(new(any)) != io.EOF {
					setExitCode(12)
					return fmt.Errorf("PLAN_ERROR: invalid simulator profiles")
				}
				implementation = executor.Simulator{Profiles: profiles}
			default:
				setExitCode(12)
				return fmt.Errorf("PLAN_ERROR: unsupported executor backend")
			}
			if mode == "" {
				mode = "GATE"
			}
			if scope == "" {
				scope = "FULL"
			}
			if auth.Method == "" {
				auth.Method = authMethod
			}
			if auth.Method == "" {
				auth.Method = "offline"
			}
			var simulator executor.Profiles
			if value, ok := implementation.(executor.Simulator); ok {
				simulator = value.Profiles
			}
			workspaceDir, err := workspaceDirectory(workspacePath())
			if err != nil {
				setExitCode(13)
				return err
			}
			requestKey, keyErr := requestKey(idempotencyKey)
			if keyErr != nil {
				setExitCode(12)
				return keyErr
			}
			ws, err := workspace.Open(workspaceDir)
			if err != nil {
				setExitCode(13)
				return fmt.Errorf("INFRA_ERROR: cannot open workspace")
			}
			defer ws.Close()
			lease, err := ws.TryLock()
			if err != nil {
				if errors.Is(err, workspace.ErrBusy) {
					config := runstore.RunConfig{PlanDigest: p.Digest, Mode: mode, Scope: scope, Backend: backend, Inventory: hosts, RoleMap: roleMap, Overrides: overrides, Auth: auth, Simulator: simulator}
					var passwordHandoff, sudoPasswordHandoff []byte
					for _, credential := range credentials {
						passwordHandoff = credential.Password
						sudoPasswordHandoff = credential.SudoPassword
						break
					}
					payload, payloadErr := delegatedSubmission(p, config, source, format, requestKey, credentials, passwordHandoff, sudoPasswordHandoff, privateKeyHandoff, privateKeyPassphrase, simulator)
					if payloadErr != nil {
						setExitCode(15)
						return payloadErr
					}
					finished, delegateErr := delegateRun(cmd.Context(), ws, payload, cmd.ErrOrStderr())
					for i := range payload {
						payload[i] = 0
					}
					if delegateErr == nil {
						if err := emitReport(cmd.OutOrStdout(), reportOutput, finished); err != nil {
							setExitCode(13)
							return err
						}
						setExitCode(report.ExitCode(report.ExitConditions{AuthError: finished.TerminationReason == "AUTH_PREFLIGHT_REJECTED", Report: finished}))
						return nil
					}
					if !strings.Contains(delegateErr.Error(), "WORKSPACE_BUSY") {
						setExitCode(delegatedExitCode(delegateErr))
						return delegateErr
					}
					if noWait {
						setExitCode(16)
						return fmt.Errorf("WORKSPACE_BUSY: workspace is owned by a short-lived process")
					}
					deadline := time.Now().Add(lockWait)
					for time.Now().Before(deadline) {
						select {
						case <-cmd.Context().Done():
							setExitCode(16)
							return fmt.Errorf("WORKSPACE_BUSY: lock wait interrupted")
						case <-time.After(100 * time.Millisecond):
						}
						lease, err = ws.TryLock()
						if err == nil {
							break
						}
						if !errors.Is(err, workspace.ErrBusy) {
							setExitCode(13)
							return err
						}
					}
					if err != nil {
						setExitCode(16)
						return fmt.Errorf("WORKSPACE_BUSY: lock wait budget expired")
					}
				} else {
					setExitCode(13)
					return err
				}
			}
			defer lease.Close()
			store, err := runstore.Open(ws)
			if err != nil {
				setExitCode(13)
				return fmt.Errorf("INFRA_ERROR: cannot recover Run store")
			}
			manager, err := runmanager.New(store, implementation)
			if err != nil {
				setExitCode(13)
				return err
			}
			config := runstore.RunConfig{
				PlanDigest: p.Digest, Mode: mode, Scope: scope, Backend: backend,
				Inventory: hosts, RoleMap: roleMap, Overrides: overrides, Auth: auth, Simulator: simulator,
			}
			submissionID, idErr := diagnostic.NewID()
			if idErr != nil {
				setExitCode(13)
				return fmt.Errorf("INFRA_ERROR: cannot create submission diagnostic ID")
			}
			request := runstore.Request{IdempotencyKey: requestKey, SubmissionID: submissionID, Plan: p, Config: config}
			run, created, err := manager.Submit(request, credentials)
			if err != nil {
				var diagnosticErr *diagnostic.Error
				if errors.As(err, &diagnosticErr) {
					item := diagnostic.ForCode(diagnosticErr.Code, diagnosticErr.HostID)
					_ = json.NewEncoder(cmd.ErrOrStderr()).Encode(map[string]any{"submission_id": submissionID, "acceptance": "rejected", "error": item.Category, "diagnostics": []diagnostic.Item{item}})
				} else {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Submission ID %s; acceptance=rejected\n", submissionID)
				}
				if strings.Contains(err.Error(), "PLAN_DIGEST_MISMATCH") || strings.Contains(err.Error(), "RUN_IDEMPOTENCY_CONFLICT") {
					setExitCode(15)
				} else if strings.Contains(err.Error(), "AUTH_ERROR") {
					setExitCode(15)
				} else if strings.Contains(err.Error(), "WORKSPACE_BUSY") {
					setExitCode(16)
				} else if strings.Contains(err.Error(), "PLAN") {
					setExitCode(12)
				} else {
					setExitCode(13)
				}
				return err
			}
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Accepted Run %s\n", run.Acceptance.RunID); err != nil {
				setExitCode(13)
				return fmt.Errorf("INFRA_ERROR: cannot report accepted Run ID")
			}
			if created {
				if _, err := manager.ProcessNext(cmd.Context()); err != nil {
					setExitCode(13)
					return fmt.Errorf("INFRA_ERROR: Run could not be archived")
				}
			}
			finished, err := store.Get(run.Acceptance.RunID)
			if err != nil || finished.Report == nil {
				setExitCode(13)
				return fmt.Errorf("INFRA_ERROR: valid RunReport is unavailable")
			}
			for _, event := range finished.Events {
				if event.Kind != "PHASE" || event.HostID == "" || event.Status == "" {
					continue
				}
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "[%s] %s %s\n", strconv.QuoteToASCII(event.HostID), event.Status, strconv.QuoteToASCII(event.Mode)); err != nil {
					setExitCode(13)
					return fmt.Errorf("INFRA_ERROR: cannot write Run progress")
				}
			}
			if err := emitReport(cmd.OutOrStdout(), reportOutput, finished.Report); err != nil {
				setExitCode(13)
				return err
			}
			authError := finished.Report.TerminationReason == "AUTH_PREFLIGHT_REJECTED"
			setExitCode(report.ExitCode(report.ExitConditions{AuthError: authError, Report: finished.Report}))
			return nil
		},
	}
	command.Flags().StringVar(&planFile, "plan", "", "Validated self-contained plan file")
	command.Flags().StringVar(&inventoryInput, "inventory", "", "Inventory file or quick host list")
	command.Flags().StringVar(&inventoryFormat, "inventory-format", "auto", "Inventory format: auto, quick, ini, yaml")
	command.Flags().StringArrayVar(&roleFlags, "role-map", nil, "Role binding: role=host[,host]")
	command.Flags().StringVar(&overridesFile, "field-overrides", "", "JSON field override file")
	command.Flags().StringVar(&mode, "mode", "GATE", "Run mode: GATE or TRIAL")
	command.Flags().StringVar(&scope, "scope", "FULL", "Run scope: FULL or PARTIAL")
	command.Flags().StringVar(&backend, "backend", "native_ssh", "Executor: native_ssh or mock_simulator")
	command.Flags().StringVar(&authMethod, "auth", "", "SSH authentication: password, private_key, or agent")
	command.Flags().StringVar(&keyFile, "private-key", "", "Local private key file; never sent as a server-side path")
	command.Flags().StringVar(&keyFingerprint, "public-key-fingerprint", "", "Selected SSH Agent key fingerprint")
	command.Flags().StringVar(&profileFile, "profiles", "", "Explicit simulator profile JSON")
	command.Flags().StringVar(&expectedDigest, "expected-plan-digest", "", "Require this exact validated Plan digest")
	command.Flags().StringVar(&reportOutput, "report-out", "", "Write the authoritative JSON report to a new file")
	command.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Stable key for safely recovering an uncertain submission (not logged)")
	command.Flags().BoolVar(&noWait, "no-wait", false, "Do not wait for a short-lived workspace lock owner")
	command.Flags().DurationVar(&lockWait, "lock-wait", 5*time.Second, "Maximum wait for a short-lived workspace lock owner")
	_ = command.MarkFlagRequired("plan")
	_ = command.MarkFlagRequired("inventory")
	return command
}

func loadPlan(file string) (*plan.Plan, error) {
	input, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: cannot open plan file")
	}
	defer input.Close()
	p, err := plan.Load(input)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func loadInventorySource(input, format string) (string, string, error) {
	source := input
	if info, err := os.Stat(input); err == nil && !info.IsDir() {
		raw, readErr := readBoundedFile(input)
		if readErr != nil {
			return "", "", readErr
		}
		source = string(raw)
		if format == "auto" {
			switch strings.ToLower(filepath.Ext(input)) {
			case ".yaml", ".yml", ".json":
				format = "yaml"
			default:
				format = "ini"
			}
		}
	} else if format == "auto" {
		format = "quick"
	}
	return source, format, nil
}

func parseRoleFlags(flags []string) (map[string][]string, error) {
	result := map[string][]string{}
	for _, value := range flags {
		role, hosts, ok := strings.Cut(value, "=")
		if !ok || role == "" || hosts == "" {
			return nil, fmt.Errorf("PLAN_ERROR: expected --role-map role=host[,host]")
		}
		if _, exists := result[role]; exists {
			return nil, fmt.Errorf("PLAN_ERROR: duplicate role binding")
		}
		result[role] = strings.Split(hosts, ",")
	}
	return result, nil
}

func randomRunKey() string { return rand.Text() }

func requestKey(flagValue string) (string, error) {
	value := strings.TrimSpace(flagValue)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("READYSPEC_IDEMPOTENCY_KEY"))
	}
	if value == "" {
		return "cli-" + randomRunKey(), nil
	}
	if len(value) > 124 {
		return "", fmt.Errorf("PLAN_ERROR: idempotency key must be at most 124 bytes")
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return "", fmt.Errorf("PLAN_ERROR: idempotency key must contain printable ASCII without spaces")
		}
	}
	return "cli-" + value, nil
}

func delegatedExitCode(err error) int {
	message := err.Error()
	switch {
	case strings.Contains(message, "AUTH_ERROR"), strings.Contains(message, "PLAN_DIGEST_MISMATCH"), strings.Contains(message, "RUN_IDEMPOTENCY_CONFLICT"):
		return 15
	case strings.Contains(message, "WORKSPACE_BUSY"):
		return 16
	case strings.Contains(message, "PLAN_ERROR"):
		return 12
	default:
		return 13
	}
}

func emitReport(out io.Writer, destination string, value *report.RunReport) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("REPORT_ERROR: cannot encode archived report")
	}
	if destination != "" {
		absolute, err := filepath.Abs(destination)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(absolute, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("INFRA_ERROR: cannot create report output")
		}
		if _, err := file.Write(append(encoded, '\n')); err != nil {
			_ = file.Close()
			_ = os.Remove(absolute)
			return fmt.Errorf("INFRA_ERROR: cannot write report output")
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(absolute)
			return fmt.Errorf("INFRA_ERROR: cannot sync report output")
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("INFRA_ERROR: cannot close report output")
		}
	}
	_, err = out.Write(append(encoded, '\n'))
	return err
}

func delegatedSubmission(p *plan.Plan, config runstore.RunConfig, inventorySource, inventoryFormat, key string, credentials map[string]executor.Credential, password, sudoPassword, privateKey, passphrase []byte, simulator executor.Profiles) ([]byte, error) {
	planBytes, err := plan.DocumentJSON(p)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: cannot serialize validated plan")
	}
	credentialBody := make(map[string]map[string]string, len(config.Inventory))
	for _, host := range config.Inventory {
		value := credentials[host.ID]
		entry := map[string]string{}
		if len(sudoPassword) > 0 {
			entry["sudo_password"] = string(sudoPassword)
		}
		switch config.Auth.Method {
		case "password":
			entry["password"] = string(password)
		case "private_key":
			entry["private_key"], entry["passphrase"] = string(privateKey), string(passphrase)
		case "agent":
			entry["agent_socket"] = value.AgentSocket
		}
		credentialBody[host.ID] = entry
	}
	body := map[string]any{
		"plan":      string(planBytes),
		"inventory": map[string]string{"source": inventorySource, "format": inventoryFormat},
		"role_map":  config.RoleMap, "field_overrides": config.Overrides,
		"run_mode": config.Mode, "run_scope": config.Scope, "executor_backend": config.Backend,
		"idempotency_key": key, "auth_identity": config.Auth, "credentials": credentialBody,
		"simulator_profiles": simulator,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("PLAN_ERROR: cannot encode delegated Run request")
	}
	return encoded, nil
}

func delegateRun(ctx context.Context, ws *workspace.Store, body []byte, stderr io.Writer) (*report.RunReport, error) {
	var instance serverInstance
	if err := ws.ReadJSON("runtime/server.json", &instance); err != nil || instance.ServerID == "" || instance.LocalToken == "" {
		return nil, fmt.Errorf("WORKSPACE_BUSY: no verifiable ReadySpec service owns the workspace")
	}
	parsed, err := url.Parse(map[bool]string{true: "https", false: "http"}[instance.Secure] + "://" + instance.Address)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("WORKSPACE_BUSY: active service address is invalid")
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fmt.Errorf("WORKSPACE_BUSY: delegation target is not a loopback address")
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, endpoint string, payload []byte) (*http.Response, error) {
		var input io.Reader
		if payload != nil {
			input = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(parsed.String(), "/")+endpoint, input)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-ReadySpec-Local-Token", instance.LocalToken)
		req.Header.Set("Host", parsed.Host)
		req.Header.Set("Origin", parsed.Scheme+"://"+parsed.Host)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		return client.Do(req)
	}
	infoResponse, err := request(http.MethodGet, "/api/v1/system/info", nil)
	if err != nil {
		return nil, fmt.Errorf("WORKSPACE_BUSY: active service could not be reached")
	}
	var info struct {
		ServerID string `json:"server_id"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(infoResponse.Body, 64*1024)).Decode(&info)
	_ = infoResponse.Body.Close()
	if infoResponse.StatusCode != http.StatusOK || decodeErr != nil || info.ServerID != instance.ServerID {
		return nil, fmt.Errorf("WORKSPACE_BUSY: active service identity could not be verified")
	}
	acceptedResponse, err := request(http.MethodPost, "/api/v1/runs", body)
	if err != nil {
		return nil, fmt.Errorf("INFRA_ERROR: delegated Run submission failed")
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(acceptedResponse.Body, 64*1024))
	_ = acceptedResponse.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("INFRA_ERROR: delegated Run response could not be read")
	}
	var accepted struct {
		RunID string `json:"run_id"`
	}
	decodeErr = json.Unmarshal(responseBody, &accepted)
	if acceptedResponse.StatusCode != http.StatusAccepted || decodeErr != nil || accepted.RunID == "" {
		var rejected struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &rejected)
		switch acceptedResponse.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return nil, fmt.Errorf("AUTH_ERROR: local delegation was rejected")
		case http.StatusTooManyRequests:
			return nil, fmt.Errorf("WORKSPACE_BUSY: service queue is full")
		default:
			if strings.Contains(rejected.Error, "PLAN_DIGEST_MISMATCH") {
				return nil, fmt.Errorf("PLAN_DIGEST_MISMATCH")
			}
			if strings.Contains(rejected.Error, "RUN_IDEMPOTENCY_CONFLICT") {
				return nil, fmt.Errorf("RUN_IDEMPOTENCY_CONFLICT")
			}
			if strings.Contains(rejected.Error, "AUTH_ERROR") {
				return nil, fmt.Errorf("AUTH_ERROR: delegated credentials rejected (%s)", rejected.Error)
			}
			if strings.Contains(rejected.Error, "PLAN") {
				return nil, fmt.Errorf("PLAN_ERROR: delegated request rejected")
			}
			return nil, fmt.Errorf("INFRA_ERROR: delegated Run was not accepted")
		}
	}
	if _, err := fmt.Fprintf(stderr, "Accepted Run %s\n", accepted.RunID); err != nil {
		return nil, fmt.Errorf("INFRA_ERROR: cannot report accepted Run ID")
	}
	deadline := time.Now().Add(2 * time.Hour)
	var cursor int64
	seenTerminal := false
	for time.Now().Before(deadline) {
		if !seenTerminal {
			response, streamErr := request(http.MethodGet, "/api/v1/runs/"+url.PathEscape(accepted.RunID)+"/events?after_seq="+strconv.FormatInt(cursor, 10), nil)
			if streamErr == nil && response.StatusCode == http.StatusOK {
				lastID := ""
				var eventName, data string
				scanner := bufio.NewScanner(io.LimitReader(response.Body, 32*1024*1024))
				scanner.Buffer(make([]byte, 4096), 1024*1024)
				for scanner.Scan() {
					line := scanner.Text()
					if line == "" {
						if data != "" {
							var payload map[string]any
							_ = json.Unmarshal([]byte(data), &payload)
							if eventName == "reset" {
								if projection, ok := payload["sequence"].(float64); ok {
									cursor = int64(projection)
								}
							} else if seq, err := strconv.ParseInt(lastID, 10, 64); err == nil && seq > cursor {
								cursor = seq
								fmt.Fprintf(stderr, "[%s] %s\n", eventName, safeProgressLabel(payload))
							}
							if eventName == "TERMINAL_ARCHIVED" {
								seenTerminal = true
							}
							if eventName == "recovery_error" {
								_ = response.Body.Close()
								return nil, fmt.Errorf("INFRA_ERROR: accepted Run storage recovery failed")
							}
						}
						lastID, eventName, data = "", "", ""
						continue
					}
					if strings.HasPrefix(line, "id: ") {
						lastID = strings.TrimPrefix(line, "id: ")
					}
					if strings.HasPrefix(line, "event: ") {
						eventName = strings.TrimPrefix(line, "event: ")
					}
					if strings.HasPrefix(line, "data: ") {
						data = strings.TrimPrefix(line, "data: ")
					}
				}
				_ = scanner.Err()
				_ = response.Body.Close()
			} else if response != nil {
				_ = response.Body.Close()
			}
		}
		response, err := request(http.MethodGet, "/api/v1/runs/"+url.PathEscape(accepted.RunID)+"/report", nil)
		if err != nil {
			return nil, fmt.Errorf("INFRA_ERROR: accepted Run report could not be retrieved")
		}
		if response.StatusCode == http.StatusOK {
			var result report.RunReport
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 16*1024*1024)).Decode(&result)
			_ = response.Body.Close()
			if decodeErr != nil {
				return nil, fmt.Errorf("INFRA_ERROR: delegated Run report is invalid")
			}
			return &result, nil
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusConflict {
			return nil, fmt.Errorf("INFRA_ERROR: delegated Run report query failed")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("INFRA_ERROR: delegated Run report deadline expired")
}

func safeProgressLabel(payload map[string]any) string {
	parts := []string{}
	for _, field := range []string{"host_id", "status", "phase", "mode"} {
		if value, ok := payload[field].(string); ok && len(value) <= 128 {
			parts = append(parts, field+"="+strconv.QuoteToASCII(value))
		}
	}
	return strings.Join(parts, " ")
}
