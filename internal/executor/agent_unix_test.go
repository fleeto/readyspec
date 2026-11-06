//go:build !windows

package executor_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func TestCredentialVaultKeepsSelectedLocalAgentForTaskLifetime(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: privateKey}); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(endpoint, 0600); err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = agent.ServeAgent(keyring, conn)
	}()

	vault := executor.NewCredentialVault()
	if err := vault.InstallRun("run-agent", map[string]executor.Credential{"node": {Method: "agent", AgentSocket: endpoint}}); err != nil {
		t.Fatal(err)
	}
	credential, err := vault.Credential(context.Background(), "run-agent", "node")
	if err != nil {
		t.Fatal(err)
	}
	client := agent.NewClient(credential.AgentConn)
	signers, err := client.Signers()
	expectedSigner, signerErr := ssh.NewSignerFromKey(privateKey)
	if err != nil || signerErr != nil || len(signers) != 1 || ssh.FingerprintSHA256(signers[0].PublicKey()) != ssh.FingerprintSHA256(expectedSigner.PublicKey()) {
		t.Fatalf("selected Agent identity was not available: signers=%d err=%v", len(signers), err)
	}
	vault.DestroyRun("run-agent")
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("task destruction did not close the selected Agent connection")
	}
}

func TestCredentialVaultRejectsAgentSocketAccessibleToOtherUsers(t *testing.T) {
	endpoint := filepath.Join(t.TempDir(), "insecure-agent.sock")
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Chmod(endpoint, 0666); err != nil {
		t.Fatal(err)
	}
	vault := executor.NewCredentialVault()
	err = vault.InstallRun("run-insecure-agent", map[string]executor.Credential{
		"node": {Method: "agent", AgentSocket: endpoint},
	})
	if err == nil {
		vault.DestroyRun("run-insecure-agent")
		t.Fatal("accepted an Agent socket accessible to other users")
	}
}

func TestCredentialVaultObservesAgentKeyRemovalDuringTask(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: privateKey}); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(t.TempDir(), "agent-key-removal.sock")
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(endpoint, 0600); err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = agent.ServeAgent(keyring, conn)
	}()

	vault := executor.NewCredentialVault()
	if err := vault.InstallRun("run-key-removal", map[string]executor.Credential{"node": {Method: "agent", AgentSocket: endpoint}}); err != nil {
		t.Fatal(err)
	}
	defer vault.DestroyRun("run-key-removal")
	credential, err := vault.Credential(context.Background(), "run-key-removal", "node")
	if err != nil {
		t.Fatal(err)
	}
	client := agent.NewClient(credential.AgentConn)
	if signers, err := client.Signers(); err != nil || len(signers) != 1 {
		t.Fatalf("initial Agent identities=%d err=%v", len(signers), err)
	}
	if err := keyring.RemoveAll(); err != nil {
		t.Fatal(err)
	}
	if signers, err := client.Signers(); err != nil || len(signers) != 0 {
		t.Fatalf("identities after Agent key removal=%d err=%v", len(signers), err)
	}
	vault.DestroyRun("run-key-removal")
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Agent server did not exit after task credential destruction")
	}
}

func TestCredentialVaultReportsAgentExitDuringTask(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: privateKey}); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(t.TempDir(), "agent-exit.sock")
	listener, err := net.Listen("unix", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(endpoint, 0600); err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	vault := executor.NewCredentialVault()
	if err := vault.InstallRun("run-agent-exit", map[string]executor.Credential{"node": {Method: "agent", AgentSocket: endpoint}}); err != nil {
		t.Fatal(err)
	}
	defer vault.DestroyRun("run-agent-exit")
	credential, err := vault.Credential(context.Background(), "run-agent-exit", "node")
	if err != nil {
		t.Fatal(err)
	}
	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("Agent connection was not accepted")
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		_ = agent.ServeAgent(keyring, serverConn)
	}()
	if _, err := agent.NewClient(credential.AgentConn).Signers(); err != nil {
		_ = serverConn.Close()
		t.Fatalf("initial Agent request failed: %v", err)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.NewClient(credential.AgentConn).Signers(); err == nil {
		t.Fatal("Agent exit during task did not fail the identity request")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("Agent server did not exit after peer disconnection")
	}
}
