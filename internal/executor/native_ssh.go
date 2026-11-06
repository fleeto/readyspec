package executor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/goss"
	"github.com/fleeto/readyspec/internal/report"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

const commandOutputLimit = 16 * 1024 * 1024

var errOutputLimit = errors.New("remote output limit exceeded")

type Credential struct {
	Method       string
	Password     []byte
	SudoPassword []byte
	Signer       ssh.Signer
	AgentSocket  string
	AgentConn    io.ReadWriteCloser
}

type CredentialProvider interface {
	Credential(context.Context, string, string) (Credential, error)
}

type CredentialMap map[string]map[string]Credential

func (m CredentialMap) Credential(_ context.Context, runID, hostID string) (Credential, error) {
	byHost := m[runID]
	value, ok := byHost[hostID]
	if !ok {
		return Credential{}, errors.New("credential not available")
	}
	return value, nil
}

type CredentialVault struct {
	mu   sync.RWMutex
	runs map[string]map[string]Credential
}

func NewCredentialVault() *CredentialVault {
	return &CredentialVault{runs: map[string]map[string]Credential{}}
}

func (v *CredentialVault) InstallRun(runID string, credentials map[string]Credential) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, exists := v.runs[runID]; exists {
		return errors.New("credentials already installed for Run")
	}
	copy := make(map[string]Credential, len(credentials))
	installed := false
	defer func() {
		if installed {
			return
		}
		for hostID, value := range copy {
			for i := range value.Password {
				value.Password[i] = 0
			}
			for i := range value.SudoPassword {
				value.SudoPassword[i] = 0
			}
			if value.AgentConn != nil {
				_ = value.AgentConn.Close()
			}
			delete(copy, hostID)
		}
	}()
	for hostID, value := range credentials {
		if value.Method == "agent" {
			connection, err := dialAgentEndpoint(value.AgentSocket)
			if err != nil {
				return errors.New("cannot acquire selected local Agent connection")
			}
			value.AgentConn = connection
		}
		value.Password = append([]byte(nil), value.Password...)
		value.SudoPassword = append([]byte(nil), value.SudoPassword...)
		copy[hostID] = value
	}
	v.runs[runID] = copy
	installed = true
	return nil
}

func (v *CredentialVault) Credential(_ context.Context, runID, hostID string) (Credential, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	value, ok := v.runs[runID][hostID]
	if !ok {
		return Credential{}, errors.New("credential not available")
	}
	return value, nil
}

func (v *CredentialVault) DestroyRun(runID string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for hostID, credential := range v.runs[runID] {
		for i := range credential.Password {
			credential.Password[i] = 0
		}
		for i := range credential.SudoPassword {
			credential.SudoPassword[i] = 0
		}
		credential.Password = nil
		credential.SudoPassword = nil
		credential.Signer = nil
		credential.AgentSocket = ""
		if credential.AgentConn != nil {
			_ = credential.AgentConn.Close()
		}
		delete(v.runs[runID], hostID)
	}
	delete(v.runs, runID)
}

func (m CredentialMap) DestroyRun(runID string) {
	for hostID, credential := range m[runID] {
		for i := range credential.Password {
			credential.Password[i] = 0
		}
		for i := range credential.SudoPassword {
			credential.SudoPassword[i] = 0
		}
		credential.Password = nil
		credential.SudoPassword = nil
		credential.Signer = nil
		credential.AgentSocket = ""
		delete(m[runID], hostID)
	}
	delete(m, runID)
}

// NativeSSH uses the system known_hosts database and task-owned in-memory
// credentials. It never opens a path supplied by a RunConfig.
type NativeSSH struct {
	Credentials    CredentialProvider
	KnownHostsPath string
}

func NewNativeSSH() *NativeSSH { return &NativeSSH{Credentials: NewCredentialVault()} }

func (e *NativeSSH) InstallRunCredentials(runID string, credentials map[string]Credential) (func(), error) {
	vault, ok := e.Credentials.(*CredentialVault)
	if !ok {
		return nil, errors.New("NativeSSH requires its task credential vault")
	}
	if err := vault.InstallRun(runID, credentials); err != nil {
		return nil, err
	}
	return func() { vault.DestroyRun(runID) }, nil
}

func (e *NativeSSH) DestroyRun(runID string) {
	if destroyer, ok := e.Credentials.(interface{ DestroyRun(string) }); ok {
		destroyer.DestroyRun(runID)
	}
}

type connection struct {
	client *ssh.Client
	keyFP  string
}

func (e *NativeSSH) connect(ctx context.Context, runID string, hostID, address string, port int, username, expectedMethod, expectedFingerprint string) (*connection, error) {
	if e.Credentials == nil {
		return nil, errors.New("task credential provider is unavailable")
	}
	credential, err := e.Credentials.Credential(ctx, runID, hostID)
	if err != nil {
		return nil, err
	}
	if expectedMethod != "" && credential.Method != expectedMethod {
		return nil, errors.New("credential method does not match accepted identity")
	}
	auth := []ssh.AuthMethod{}
	var agentConn io.ReadWriteCloser
	switch credential.Method {
	case "password":
		if len(credential.Password) == 0 {
			return nil, errors.New("password credential is empty")
		}
		auth = append(auth, ssh.Password(string(credential.Password)))
	case "private_key":
		if credential.Signer == nil {
			return nil, errors.New("private key credential is unavailable")
		}
		if expectedFingerprint != "" && ssh.FingerprintSHA256(credential.Signer.PublicKey()) != expectedFingerprint {
			return nil, errors.New("selected public key identity mismatch")
		}
		auth = append(auth, ssh.PublicKeys(credential.Signer))
	case "agent":
		if credential.AgentSocket == "" {
			return nil, errors.New("explicit local SSH Agent endpoint is missing")
		}
		agentConn = credential.AgentConn
		if agentConn == nil {
			return nil, errors.New("task-owned Agent connection is unavailable")
		}
		agentClient := agent.NewClient(agentConn)
		auth = append(auth, ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
			signers, err := agentClient.Signers()
			if err != nil {
				return nil, err
			}
			selected := make([]ssh.Signer, 0, len(signers))
			for _, signer := range signers {
				if expectedFingerprint == "" || ssh.FingerprintSHA256(signer.PublicKey()) == expectedFingerprint {
					selected = append(selected, signer)
				}
			}
			if len(selected) == 0 {
				return nil, errors.New("selected Agent key is unavailable")
			}
			return selected, nil
		}))
	default:
		return nil, errors.New("unsupported SSH authentication method")
	}
	knownHostsFile := e.KnownHostsPath
	if knownHostsFile == "" {
		knownHostsFile = filepath.Join(userHomeDir(), ".ssh", "known_hosts")
	}
	knownHostCallback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("load SSH known_hosts: %w", err)
	}
	var hostKeyFingerprint string
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if err := knownHostCallback(hostname, remote, key); err != nil {
			return err
		}
		hostKeyFingerprint = ssh.FingerprintSHA256(key)
		return nil
	}
	config := &ssh.ClientConfig{User: username, Auth: auth, HostKeyCallback: callback, Timeout: 8 * time.Second}
	remote := net.JoinHostPort(address, strconv.Itoa(port))
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	transport, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", remote)
	if err != nil {
		return nil, err
	}
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = transport.SetDeadline(deadline)
	}
	clientConn, channels, requests, err := ssh.NewClientConn(transport, remote, config)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	_ = transport.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConn, channels, requests)
	return &connection{client: client, keyFP: hostKeyFingerprint}, nil
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

func (e *NativeSSH) Preflight(ctx context.Context, req PreflightRequest) PreflightResult {
	if req.Journal != nil {
		if err := req.Journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "CONNECTING"); err != nil {
			return PreflightResult{Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist connection phase"}}
		}
	}
	conn, err := e.connect(ctx, req.RunID, req.Host.Inventory.ID, req.Host.Inventory.Address, req.Host.Inventory.Port, req.Host.Inventory.Username, req.Host.AuthMethod, req.Host.PublicKeyFingerprint)
	if err != nil {
		class := classifySSHConnectFailure(err)
		return PreflightResult{Failure: &Failure{Class: class, Phase: "SSH_AUTHENTICATION", Message: "SSH connection or host key verification failed"}}
	}
	defer conn.Close()
	if req.Journal != nil {
		if err := req.Journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "DISCOVERING"); err != nil {
			return PreflightResult{Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist discovery phase"}}
		}
	}
	facts, err := discover(ctx, conn.client)
	if err != nil {
		return PreflightResult{Failure: &Failure{Class: FailureIdentity, Phase: "DISCOVERY", Message: "target identity facts could not be verified"}}
	}
	facts.HostKey = conn.keyFP
	facts.Root = facts.UID == 0
	owner := make([]byte, 16)
	if _, err := rand.Read(owner); err != nil {
		return PreflightResult{Facts: facts, Failure: &Failure{Class: FailureExecution, Phase: "PREFLIGHT", Message: "cannot establish remote cleanup ownership"}}
	}
	facts.OwnerToken = hex.EncodeToString(owner)
	if len(req.Artifacts.Privileged.Items) > 0 && !facts.Root {
		credential, credentialErr := e.Credentials.Credential(ctx, req.RunID, req.Host.Inventory.ID)
		if credentialErr != nil {
			return PreflightResult{Facts: facts, Failure: &Failure{Class: FailureAuth, Phase: "SUDO_CREDENTIAL", Message: "required sudo credential is unavailable"}}
		}
		if class := checkSudoPolicy(ctx, conn.client, req.RunID, req.Host, req.Artifacts.Privileged, facts.Arch, facts.OwnerToken, credential); class != "" {
			return PreflightResult{Facts: facts, Failure: &Failure{Class: class, Phase: "SUDO_POLICY", Message: "controlled sudo command was denied or could not be verified"}}
		}
	}
	return PreflightResult{Facts: facts}
}

func discover(ctx context.Context, client *ssh.Client) (HostFacts, error) {
	command := "printf 'UID='; id -u; printf 'ARCH='; uname -m; printf 'MACHINE='; cat /etc/machine-id 2>/dev/null; printf 'BOOT='; cat /proc/sys/kernel/random/boot_id 2>/dev/null; printf 'CHECKSUM='; command -v sha256sum || command -v shasum || true"
	output, err := runSSH(ctx, client, command, nil)
	if err != nil {
		return HostFacts{}, err
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	uid, err := strconv.Atoi(fields["UID"])
	if err != nil || fields["ARCH"] == "" || fields["MACHINE"] == "" || fields["BOOT"] == "" || fields["CHECKSUM"] == "" {
		return HostFacts{}, errors.New("required remote facts unavailable")
	}
	return HostFacts{UID: uid, Arch: fields["ARCH"], MachineID: fields["MACHINE"], BootID: fields["BOOT"]}, nil
}

func (e *NativeSSH) Execute(ctx context.Context, req ExecuteRequest, journal Journal) (outcome HostOutcome) {
	if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "CONNECTING"); err != nil {
		return HostOutcome{Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist connection phase"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	}
	conn, err := e.connect(ctx, req.RunID, req.Host.Inventory.ID, req.Host.Inventory.Address, req.Host.Inventory.Port, req.Host.Inventory.Username, req.Host.AuthMethod, req.Host.PublicKeyFingerprint)
	if err != nil {
		class := classifySSHConnectFailure(err)
		phase := "RECONNECT"
		if class == FailureIdentity {
			phase = "REVALIDATE"
		}
		return HostOutcome{Failure: &Failure{Class: class, Phase: phase, Message: "target reconnect or host identity verification failed"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	}
	defer conn.Close()
	if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "DISCOVERING"); err != nil {
		return HostOutcome{Failure: &Failure{Class: FailureExecution, Phase: "JOURNAL", Message: "cannot persist discovery phase"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	}
	facts, err := discover(ctx, conn.client)
	if err != nil || facts.MachineID != req.Facts.MachineID || facts.BootID != req.Facts.BootID || facts.UID != req.Facts.UID {
		return HostOutcome{Failure: &Failure{Class: FailureIdentity, Phase: "REVALIDATE", Message: "target identity or privilege facts changed after preflight"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	}
	facts.OwnerToken = req.Facts.OwnerToken
	if conn.keyFP != req.Facts.HostKey {
		return HostOutcome{Failure: &Failure{Class: FailureIdentity, Phase: "REVALIDATE", Message: "SSH host key changed after preflight"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	}
	if len(req.Artifacts.Privileged.Items) > 0 && !facts.Root {
		credential, credentialErr := e.Credentials.Credential(ctx, req.RunID, req.Host.Inventory.ID)
		if credentialErr != nil {
			return HostOutcome{Failure: &Failure{Class: FailureAuth, Phase: "SUDO_CREDENTIAL", Message: "required sudo credential is unavailable"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
		}
		if class := checkSudoPolicy(ctx, conn.client, req.RunID, req.Host, req.Artifacts.Privileged, facts.Arch, facts.OwnerToken, credential); class != "" {
			return HostOutcome{Failure: &Failure{Class: class, Phase: "SUDO_POLICY", Message: "controlled sudo command was denied or could not be verified"}, Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
		}
	}
	result := HostOutcome{Results: []compiler.ItemResult{}, Cleanup: []report.CleanupItem{}, Modes: map[string]bool{}}
	if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "PREPARING"); err != nil {
		return failedOutcome(result, "JOURNAL", "cannot persist preparation phase")
	}
	ownerToken := facts.OwnerToken
	if len(ownerToken) != 32 {
		return failedOutcome(result, "PREPARE", "cleanup ownership token is unavailable")
	}
	remotePath := executionWorkspace(req.RunID, req.Host.Inventory.ID)
	cleanup := report.CleanupItem{CleanupID: req.RunID + ":" + ownerToken[:12], HostID: req.Host.Inventory.ID, MachineID: facts.MachineID, BootID: facts.BootID, Status: "PENDING", RemotePath: remotePath, OwnerToken: ownerToken}
	if err := journal.CleanupRegistered(ctx, req.RunID, cleanup); err != nil {
		return failedOutcome(result, "JOURNAL", "cannot persist cleanup registration")
	}
	prepare := "umask 077; mkdir -- " + shellQuote(remotePath) + " && chmod 700 -- " + shellQuote(remotePath) + " && printf '%s' " + shellQuote(ownerToken) + " > " + shellQuote(remotePath+"/.readyspec_owner")
	if _, err := runSSH(ctx, conn.client, prepare, nil); err != nil {
		cleanup.Status, cleanup.Reason = "UNKNOWN", "remote workspace creation could not be confirmed"
		_ = journal.CleanupUpdated(ctx, req.RunID, cleanup)
		result.Cleanup = append(result.Cleanup, cleanup)
		return failedOutcome(result, "PREPARE", "remote execution workspace could not be created")
	}
	defer func() {
		_ = journal.Phase(context.Background(), req.RunID, req.Host.Inventory.ID, "CLEANING")
		command := "test \"$(cat /etc/machine-id 2>/dev/null)\" = " + shellQuote(facts.MachineID) + " && test \"$(cat /proc/sys/kernel/random/boot_id 2>/dev/null)\" = " + shellQuote(facts.BootID) + " && test ! -L " + shellQuote(remotePath) + " && test \"$(cat " + shellQuote(remotePath+"/.readyspec_owner") + " 2>/dev/null)\" = " + shellQuote(ownerToken) + " && rm -rf -- " + shellQuote(remotePath)
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, cleanErr := runSSH(cleanupCtx, conn.client, command, nil)
		cleanupCancel()
		if cleanErr == nil {
			cleanup.Status, cleanup.Reason = "CLEAN", ""
		} else {
			cleanup.Status, cleanup.Reason = "UNKNOWN", "remote cleanup could not be confirmed"
		}
		archiveCtx, archiveCancel := context.WithTimeout(context.Background(), 3*time.Second)
		journalErr := journal.CleanupUpdated(archiveCtx, req.RunID, cleanup)
		if phaseErr := journal.Phase(archiveCtx, req.RunID, req.Host.Inventory.ID, "FINISHED"); journalErr == nil {
			journalErr = phaseErr
		}
		archiveCancel()
		outcome.Cleanup = append(outcome.Cleanup, cleanup)
		if journalErr != nil && outcome.Failure == nil {
			outcome.Failure = &Failure{Class: FailureExecution, Phase: "CLEANUP_JOURNAL", Message: "cleanup outcome could not be persisted"}
		}
	}()
	for _, artifact := range []compiler.Artifact{req.Artifacts.Standard, req.Artifacts.Privileged} {
		if len(artifact.Items) == 0 {
			continue
		}
		if artifact.ExecutionMode == "PRIVILEGED" && !facts.Root {
			credential, credentialErr := e.Credentials.Credential(ctx, req.RunID, req.Host.Inventory.ID)
			if credentialErr != nil || checkSudoPolicy(ctx, conn.client, req.RunID, req.Host, req.Artifacts.Privileged, facts.Arch, facts.OwnerToken, credential) != "" {
				return failedOutcome(result, "SUDO_POLICY", "controlled sudo command could not be revalidated")
			}
		}
		gossBinary, err := goss.Binary(facts.Arch)
		if err != nil {
			return failedOutcome(result, "PREPARE", "embedded Goss is unavailable for target architecture")
		}
		gossPath := remotePath + "/goss"
		configPath := remotePath + "/" + strings.ToLower(artifact.ExecutionMode) + ".yaml"
		if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "UPLOADING_"+artifact.ExecutionMode); err != nil {
			return failedOutcome(result, "JOURNAL", "cannot persist upload phase")
		}
		if _, err := runSSH(ctx, conn.client, "cat > '"+gossPath+"' && chmod 700 -- '"+gossPath+"'", gossBinary); err != nil {
			return failedOutcome(result, "UPLOAD", "embedded Goss upload failed")
		}
		if _, err := runSSH(ctx, conn.client, "cat > '"+configPath+"'", artifact.Config); err != nil {
			return failedOutcome(result, "UPLOAD", "compiled check configuration upload failed")
		}
		if err := verifyRemoteSHA256(ctx, conn.client, gossPath, gossBinary); err != nil {
			return failedOutcome(result, "UPLOAD_VERIFY", "embedded Goss digest verification failed")
		}
		if err := verifyRemoteSHA256(ctx, conn.client, configPath, artifact.Config); err != nil {
			return failedOutcome(result, "UPLOAD_VERIFY", "compiled configuration digest verification failed")
		}
		if err := markExecutionStarted(ctx, journal, req.RunID, req.Host.Inventory.ID, artifact.ExecutionMode); err != nil {
			return failedOutcome(result, "JOURNAL", "execution marker or phase could not be persisted")
		}
		command := "env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/ '" + gossPath + "' -g '" + configPath + "' validate --format json --retry-timeout 0s --no-color --max-concurrent 10"
		var commandInput []byte
		if artifact.ExecutionMode == "PRIVILEGED" {
			rootDir := remotePath + "/privileged"
			script := privilegedShellScript(gossPath, configPath, rootDir, artifact, gossBinary, ownerToken)
			if facts.Root {
				command = "/bin/sh -c " + shellQuote(script)
			} else {
				credential, _ := e.Credentials.Credential(ctx, req.RunID, req.Host.Inventory.ID)
				command = "/usr/bin/sudo -S -p '' -- /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/root /bin/sh -c " + shellQuote(script)
				if len(credential.SudoPassword) > 0 {
					commandInput = append(append([]byte(nil), credential.SudoPassword...), '\n')
				}
			}
		}
		output, err := runSSH(ctx, conn.client, command, commandInput)
		for i := range commandInput {
			commandInput[i] = 0
		}
		if artifact.ExecutionMode == "PRIVILEGED" && !facts.Root {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, ticketErr := runSSH(cleanupCtx, conn.client, "/usr/bin/sudo -k", nil)
			cleanupCancel()
			if ticketErr != nil {
				return failedOutcome(result, "SUDO_TICKET", "sudo timestamp could not be cleared")
			}
		}
		if err != nil && !isCheckFailure(err) {
			return failedOutcome(result, "EXECUTE", "Goss execution failed or returned invalid output")
		}
		if err := journal.Phase(ctx, req.RunID, req.Host.Inventory.ID, "COLLECTING_"+artifact.ExecutionMode); err != nil {
			return failedOutcome(result, "JOURNAL", "cannot persist collection phase")
		}
		normalized, err := compiler.Normalize(artifact, output)
		if err != nil {
			return failedOutcome(result, "COLLECT", "Goss output did not satisfy the result contract")
		}
		result.Results = append(result.Results, normalized...)
		result.Modes[artifact.ExecutionMode] = true
	}
	return result
}

func markExecutionStarted(ctx context.Context, journal Journal, runID, hostID, mode string) error {
	if err := journal.ExecutionStarted(ctx, runID, hostID, mode); err != nil {
		return err
	}
	return journal.Phase(ctx, runID, hostID, "RUNNING_"+mode)
}

func classifySSHConnectFailure(err error) FailureClass {
	var keyErr *knownhosts.KeyError
	switch {
	case errors.As(err, &keyErr), strings.Contains(strings.ToLower(err.Error()), "known_hosts"):
		return FailureIdentity
	case strings.Contains(strings.ToLower(err.Error()), "unable to authenticate"), strings.Contains(strings.ToLower(err.Error()), "credential"), strings.Contains(strings.ToLower(err.Error()), "public key identity"):
		return FailureAuth
	default:
		return FailureNetwork
	}
}

func isCheckFailure(err error) bool {
	var exitError *ssh.ExitError
	return errors.As(err, &exitError) && exitError.ExitStatus() == 1
}

// ResolveCleanup uses fresh, operation-scoped credentials and never reuses the
// completed Run's credential vault entry.
func (e *NativeSSH) ResolveCleanup(ctx context.Context, expectedHostKey string, host HostInput, authFingerprint string, item report.CleanupItem, credential Credential, confirmTerminate bool) (report.CleanupItem, error) {
	operationNonce := make([]byte, 16)
	if _, err := rand.Read(operationNonce); err != nil {
		return item, fmt.Errorf("INFRA_ERROR: cannot create cleanup operation identity")
	}
	operationID := "cleanup-" + hex.EncodeToString(operationNonce)
	destroy, err := e.InstallRunCredentials(operationID, map[string]Credential{host.Inventory.ID: credential})
	if err != nil {
		return item, fmt.Errorf("AUTH_ERROR: fresh cleanup credential is unavailable")
	}
	defer destroy()
	conn, err := e.connect(ctx, operationID, host.Inventory.ID, host.Inventory.Address, host.Inventory.Port, host.Inventory.Username, credential.Method, authFingerprint)
	if err != nil {
		return item, fmt.Errorf("AUTH_ERROR: cleanup SSH identity could not be verified")
	}
	defer conn.Close()
	facts, err := discover(ctx, conn.client)
	if err != nil || facts.MachineID != item.MachineID || facts.BootID != item.BootID || conn.keyFP != expectedHostKey {
		return item, fmt.Errorf("CLEANUP_IDENTITY_MISMATCH")
	}
	if !safeRemoteCleanupPath(item.RemotePath) || !validOwnerToken(item.OwnerToken) {
		return item, fmt.Errorf("CLEANUP_OWNERSHIP_INVALID")
	}
	verify := "test ! -L " + shellQuote(item.RemotePath) + " && test -d " + shellQuote(item.RemotePath) + " && test \"$(cat " + shellQuote(item.RemotePath+"/.readyspec_owner") + " 2>/dev/null)\" = " + shellQuote(item.OwnerToken)
	if _, err := runSSH(ctx, conn.client, verify, nil); err != nil {
		return item, fmt.Errorf("CLEANUP_OWNERSHIP_INVALID")
	}
	processCommand := "command -v readlink >/dev/null 2>&1 || exit 127; for p in /proc/[0-9]*; do found=; c=$(readlink \"$p/cwd\" 2>/dev/null) || c=; case \"$c\" in " + shellQuote(item.RemotePath) + "|" + shellQuote(item.RemotePath) + "/*) found=1;; esac; for fd in \"$p\"/fd/*; do f=$(readlink \"$fd\" 2>/dev/null) || continue; case \"$f\" in " + shellQuote(item.RemotePath) + "|" + shellQuote(item.RemotePath) + "/*) found=1; break;; esac; done; test -z \"$found\" || echo \"${p##*/}\"; done"
	var output []byte
	if facts.UID == 0 {
		output, err = runSSH(ctx, conn.client, processCommand, nil)
	} else {
		output, err = runSudoScript(ctx, conn.client, "set -eu; "+verify+"; "+processCommand, credential.SudoPassword)
	}
	if err != nil {
		return item, fmt.Errorf("CLEANUP_PROCESS_CHECK_FAILED")
	}
	pids := strings.Fields(string(output))
	for _, pid := range pids {
		if _, err := strconv.Atoi(pid); err != nil {
			return item, fmt.Errorf("CLEANUP_PROCESS_CHECK_FAILED")
		}
	}
	if !mayTerminateCleanupProcesses(pids, confirmTerminate) {
		item.Status, item.Reason = "UNKNOWN", "associated processes are active; explicit termination confirmation is required"
		return item, nil
	}
	rootPath := item.RemotePath + "/privileged"
	if !safeRemoteCleanupPath(rootPath) {
		return item, fmt.Errorf("CLEANUP_OWNERSHIP_INVALID")
	}
	rootExistsOutput, err := runSSH(ctx, conn.client, "if test -e "+shellQuote(rootPath)+"; then printf yes; else printf no; fi", nil)
	if err != nil {
		return item, fmt.Errorf("CLEANUP_OWNERSHIP_INVALID")
	}
	rootExists := strings.TrimSpace(string(rootExistsOutput)) == "yes"
	if rootExists {
		rootScript := "set -eu; root=" + shellQuote(rootPath) + "; test ! -L \"$root\"; test -d \"$root\"; test \"$(cat \"$root/.readyspec_owner\" 2>/dev/null)\" = '" + item.OwnerToken + "'; "
		if len(pids) > 0 {
			rootScript += "for pid in " + strings.Join(pids, " ") + "; do kill -TERM \"$pid\" 2>/dev/null || true; done; sleep 1; for pid in " + strings.Join(pids, " ") + "; do if kill -0 \"$pid\" 2>/dev/null; then exit 3; fi; done; "
		}
		rootScript += "rm -rf -- \"$root\"; test ! -e \"$root\""
		if facts.UID == 0 {
			_, err = runSSH(ctx, conn.client, "/bin/sh -c "+shellQuote(rootScript), nil)
		} else {
			_, err = runSudoScript(ctx, conn.client, rootScript, credential.SudoPassword)
		}
		if err != nil {
			return item, fmt.Errorf("CLEANUP_ROOT_DIRECTORY_UNCONFIRMED")
		}
	} else if len(pids) > 0 {
		kill := "set -eu; " + verify + "; for pid in " + strings.Join(pids, " ") + "; do kill -TERM \"$pid\" 2>/dev/null || true; done; sleep 1; for pid in " + strings.Join(pids, " ") + "; do if kill -0 \"$pid\" 2>/dev/null; then exit 3; fi; done"
		var killErr error
		if facts.UID == 0 {
			_, killErr = runSSH(ctx, conn.client, "/bin/sh -c "+shellQuote(kill), nil)
		} else {
			_, killErr = runSudoScript(ctx, conn.client, kill, credential.SudoPassword)
		}
		if killErr != nil {
			return item, fmt.Errorf("CLEANUP_PROCESS_TERMINATION_FAILED")
		}
		pids = nil
	}
	if rootExists && len(pids) > 0 {
		pids = nil
	}
	if len(pids) > 0 {
		return item, fmt.Errorf("CLEANUP_PROCESSES_REMAIN_ACTIVE")
	}
	remove := verify + " && rm -rf -- " + shellQuote(item.RemotePath) + " && test ! -e " + shellQuote(item.RemotePath)
	if _, err := runSSH(ctx, conn.client, remove, nil); err != nil {
		return item, fmt.Errorf("CLEANUP_DELETE_UNCONFIRMED")
	}
	item.Status, item.Reason = "CLEAN", ""
	return item, nil
}

func mayTerminateCleanupProcesses(pids []string, confirmed bool) bool {
	return len(pids) == 0 || confirmed
}

func runSudoScript(ctx context.Context, client *ssh.Client, script string, password []byte) ([]byte, error) {
	command := "/usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/root /bin/sh -c " + shellQuote(script)
	_, queryErr := runSSH(ctx, client, "/usr/bin/sudo -n -l -- "+command, nil)
	if queryErr != nil && len(password) > 0 {
		input := append(append([]byte(nil), password...), '\n')
		_, queryErr = runSSH(ctx, client, "/usr/bin/sudo -S -p '' -l -- "+command, input)
		for i := range input {
			input[i] = 0
		}
	}
	if queryErr != nil {
		clearCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = runSSH(clearCtx, client, "/usr/bin/sudo -k", nil)
		cancel()
		return nil, fmt.Errorf("CLEANUP_SUDO_POLICY_UNVERIFIABLE")
	}
	input := []byte(nil)
	if len(password) > 0 {
		input = append(append([]byte(nil), password...), '\n')
	}
	output, runErr := runSSH(ctx, client, "/usr/bin/sudo -S -p '' -- "+command, input)
	for i := range input {
		input[i] = 0
	}
	killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, ticketErr := runSSH(killCtx, client, "/usr/bin/sudo -k", nil)
	cancel()
	if runErr != nil {
		return output, runErr
	}
	if ticketErr != nil {
		return output, fmt.Errorf("sudo timestamp could not be cleared")
	}
	return output, nil
}

func safeRemoteCleanupPath(value string) bool {
	return strings.HasPrefix(value, "/tmp/readyspec-") && !strings.ContainsAny(value, "'\n\r") && !strings.Contains(value, "..")
}

func validOwnerToken(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func executionWorkspace(runID, hostID string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + hostID))
	return "/tmp/readyspec-" + hex.EncodeToString(sum[:12])
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func privilegedShellScript(gossPath, configPath, rootDir string, artifact compiler.Artifact, binary []byte, ownerToken string) string {
	binaryDigest := fmt.Sprintf("%x", sha256.Sum256(binary))
	configDigest := fmt.Sprintf("%x", sha256.Sum256(artifact.Config))
	return "set -eu; umask 077; root=" + shellQuote(rootDir) + "; trap 'rm -rf -- \"$root\"' 0; trap 'exit 129' HUP; trap 'exit 130' INT; trap 'exit 143' TERM; " +
		"mkdir -- \"$root\"; chown 0:0 -- \"$root\"; chmod 700 -- \"$root\"; printf '%s' '" + ownerToken + "' > \"$root/.readyspec_owner\"; chmod 600 -- \"$root/.readyspec_owner\"; " +
		"cp -- " + shellQuote(gossPath) + " \"$root/goss\"; cp -- " + shellQuote(configPath) + " \"$root/config.yaml\"; " +
		"chown 0:0 -- \"$root/goss\" \"$root/config.yaml\"; chmod 700 -- \"$root/goss\"; chmod 600 -- \"$root/config.yaml\"; " +
		"if command -v sha256sum >/dev/null 2>&1; then printf '%s  %s\\n' '" + binaryDigest + "' \"$root/goss\" | sha256sum -c - >/dev/null && printf '%s  %s\\n' '" + configDigest + "' \"$root/config.yaml\" | sha256sum -c - >/dev/null; " +
		"elif command -v shasum >/dev/null 2>&1; then printf '%s  %s\\n' '" + binaryDigest + "' \"$root/goss\" | shasum -a 256 -c - >/dev/null && printf '%s  %s\\n' '" + configDigest + "' \"$root/config.yaml\" | shasum -a 256 -c - >/dev/null; else exit 127; fi; " +
		"if env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/root \"$root/goss\" -g \"$root/config.yaml\" validate --format json --retry-timeout 0s --no-color --max-concurrent 10; then status=0; else status=$?; fi; exit \"$status\""
}

func checkSudoPolicy(ctx context.Context, client *ssh.Client, runID string, host HostInput, artifact compiler.Artifact, arch, ownerToken string, credential Credential) FailureClass {
	binary, err := goss.Binary(arch)
	if err != nil {
		return FailureSudoUnknown
	}
	workspace := executionWorkspace(runID, host.Inventory.ID)
	script := privilegedShellScript(workspace+"/goss", workspace+"/privileged.yaml", workspace+"/privileged", artifact, binary, ownerToken)
	command := "/usr/bin/sudo -n -l -- /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/root /bin/sh -c " + shellQuote(script)
	output, queryErr := runSSH(ctx, client, command, nil)
	if queryErr != nil && len(credential.SudoPassword) > 0 {
		passwordInput := append(append([]byte(nil), credential.SudoPassword...), '\n')
		command = "/usr/bin/sudo -S -p '' -l -- /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LC_ALL=C HOME=/root /bin/sh -c " + shellQuote(script)
		output, queryErr = runSSH(ctx, client, command, passwordInput)
		for i := range passwordInput {
			passwordInput[i] = 0
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, cleanupErr := runSSH(cleanupCtx, client, "/usr/bin/sudo -k", nil)
	cancel()
	return classifySudoPolicyResult(queryErr, cleanupErr, output)
}

func classifySudoPolicyResult(queryErr, cleanupErr error, output []byte) FailureClass {
	if queryErr == nil && cleanupErr == nil {
		return ""
	}
	message := strings.ToLower(fmt.Sprint(queryErr) + " " + fmt.Sprint(cleanupErr) + " " + string(output))
	if strings.Contains(message, "not allowed to execute") || strings.Contains(message, "not permitted") ||
		strings.Contains(message, "not in the sudoers") || strings.Contains(message, "incorrect password") ||
		strings.Contains(message, "may not run sudo") || strings.Contains(message, "a password is required") ||
		strings.Contains(message, "interactive authentication is required") {
		return FailureAuth
	}
	return FailureSudoUnknown
}

func verifyRemoteSHA256(ctx context.Context, client *ssh.Client, remotePath string, local []byte) error {
	want := fmt.Sprintf("%x", sha256.Sum256(local))
	command := "if command -v sha256sum >/dev/null 2>&1; then sha256sum '" + remotePath + "'; elif command -v shasum >/dev/null 2>&1; then shasum -a 256 '" + remotePath + "'; else exit 127; fi"
	output, err := runSSH(ctx, client, command, nil)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 || fields[0] != want {
		return errors.New("remote digest mismatch")
	}
	return nil
}

func failedOutcome(out HostOutcome, phase, message string) HostOutcome {
	out.Failure = &Failure{Class: FailureExecution, Phase: phase, Message: message}
	return out
}

func (c *connection) Close() error {
	var err error
	if c.client != nil {
		err = c.client.Close()
	}
	return err
}

func runSSH(ctx context.Context, client *ssh.Client, command string, input []byte) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := session.Start(command); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	var stderrBuffer bytes.Buffer
	var stdoutErr, stderrErr error
	var wg sync.WaitGroup
	wg.Add(2)
	copyLimited := func(reader io.Reader, target *bytes.Buffer, result *error) {
		defer wg.Done()
		written, err := io.Copy(target, io.LimitReader(reader, commandOutputLimit+1))
		if written > commandOutputLimit {
			*result = errOutputLimit
			_ = session.Close()
		} else if err != nil {
			*result = err
		}
	}
	go copyLimited(stdout, &output, &stdoutErr)
	go copyLimited(stderr, &stderrBuffer, &stderrErr)
	inputDone := make(chan error, 1)
	go func() {
		if len(input) > 0 {
			_, err := stdin.Write(input)
			if err != nil {
				inputDone <- err
				return
			}
		}
		inputDone <- stdin.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		return nil, ctx.Err()
	case err := <-done:
		wg.Wait()
		if inputErr := <-inputDone; inputErr != nil && len(input) > 0 {
			return output.Bytes(), inputErr
		}
		if stdoutErr != nil || stderrErr != nil {
			return output.Bytes(), errors.Join(stdoutErr, stderrErr)
		}
		if err != nil {
			stderrText := strings.TrimSpace(stderrBuffer.String())
			if stderrText != "" {
				return output.Bytes(), fmt.Errorf("%w: %s", err, stderrText)
			}
			return output.Bytes(), err
		}
		return output.Bytes(), nil
	}
}

var _ Executor = (*NativeSSH)(nil)
