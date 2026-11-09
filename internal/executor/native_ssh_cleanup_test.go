package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/report"
	"golang.org/x/crypto/ssh"
)

type cleanupSSHFixture struct {
	listener net.Listener
	commands []string
	mu       sync.Mutex
	done     chan struct{}
}

func startCleanupSSHFixture(t *testing.T, activeProcesses bool) (*cleanupSSHFixture, *NativeSSH, Credential, HostInput, report.CleanupItem, string) {
	t.Helper()
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &cleanupSSHFixture{listener: listener, done: make(chan struct{})}
	serverConfig := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(clientSigner.PublicKey().Marshal()) {
			return nil, fmt.Errorf("public key rejected")
		}
		return nil, nil
	}}
	serverConfig.AddHostKey(hostSigner)
	go func() {
		defer close(fixture.done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		serverConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
		if err != nil {
			_ = conn.Close()
			return
		}
		go ssh.DiscardRequests(requests)
		for newChannel := range channels {
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer channel.Close()
				for request := range requests {
					if request.Type != "exec" {
						continue
					}
					var payload struct{ Command string }
					_ = ssh.Unmarshal(request.Payload, &payload)
					fixture.mu.Lock()
					fixture.commands = append(fixture.commands, payload.Command)
					fixture.mu.Unlock()
					_ = request.Reply(true, nil)
					switch {
					case strings.Contains(payload.Command, "printf 'UID='"):
						_, _ = channel.Write([]byte("UID=0\nARCH=x86_64\nMACHINE=machine-cleanup\nBOOT=dddddddd-dddd-4ddd-8ddd-dddddddddddd\nCHECKSUM=/usr/bin/sha256sum\n"))
					case strings.Contains(payload.Command, "for p in /proc/[0-9]*") && activeProcesses:
						_, _ = channel.Write([]byte("4242\n"))
					case strings.Contains(payload.Command, "if test -e "):
						_, _ = channel.Write([]byte("no"))
					}
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
					return
				}
			}()
		}
		_ = serverConn.Close()
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-fixture.done:
		case <-time.After(time.Second):
			t.Error("cleanup SSH fixture did not stop")
		}
	})

	address := listener.Addr().(*net.TCPAddr)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	line := fmt.Sprintf("[127.0.0.1]:%d %s\n", address.Port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))))
	if err := os.WriteFile(knownHosts, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	native := NewNativeSSH()
	native.KnownHostsPath = knownHosts
	credential := Credential{Method: "private_key", Signer: clientSigner}
	host := HostInput{Inventory: inventory.Host{ID: "cleanup-host", Address: "127.0.0.1", Port: address.Port, Username: "cleanup"}, AuthMethod: "private_key", PublicKeyFingerprint: ssh.FingerprintSHA256(clientSigner.PublicKey())}
	item := report.CleanupItem{CleanupID: "run-cleanup", HostID: host.Inventory.ID, MachineID: "machine-cleanup", BootID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Status: "PENDING", RemotePath: "/tmp/readyspec-cleanup-owner", OwnerToken: "0123456789abcdef0123456789abcdef"}
	return fixture, native, credential, host, item, ssh.FingerprintSHA256(hostSigner.PublicKey())
}

func (f *cleanupSSHFixture) commandSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func TestResolveCleanupRequiresConfirmationBeforeTerminatingOrDeleting(t *testing.T) {
	fixture, native, credential, host, item, hostFingerprint := startCleanupSSHFixture(t, true)
	resolved, err := native.ResolveCleanup(context.Background(), hostFingerprint, host, host.PublicKeyFingerprint, item, credential, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "UNKNOWN" {
		t.Fatalf("unconfirmed cleanup status=%q want UNKNOWN", resolved.Status)
	}
	for _, command := range fixture.commandSnapshot() {
		if strings.Contains(command, "kill -TERM") || strings.Contains(command, "rm -rf --") {
			t.Fatalf("unconfirmed cleanup issued destructive command: %s", command)
		}
	}
}

func TestResolveCleanupCompletesAfterConfirmedProcessTermination(t *testing.T) {
	fixture, native, credential, host, item, hostFingerprint := startCleanupSSHFixture(t, true)
	resolved, err := native.ResolveCleanup(context.Background(), hostFingerprint, host, host.PublicKeyFingerprint, item, credential, true)
	if err != nil {
		t.Fatalf("confirmed cleanup failed: %v", err)
	}
	if resolved.Status != "CLEAN" {
		t.Fatalf("confirmed cleanup status=%q want CLEAN", resolved.Status)
	}
	commands := fixture.commandSnapshot()
	killIndex, removeIndex := -1, -1
	for i, command := range commands {
		if strings.Contains(command, "kill -TERM") {
			killIndex = i
		}
		if strings.Contains(command, "rm -rf --") {
			removeIndex = i
		}
	}
	if killIndex < 0 || removeIndex <= killIndex {
		t.Fatalf("cleanup did not confirm termination before removal: commands=%v", commands)
	}
	vault := native.Credentials.(*CredentialVault)
	vault.mu.RLock()
	remainingCredentials := len(vault.runs)
	vault.mu.RUnlock()
	if remainingCredentials != 0 {
		t.Fatalf("cleanup operation retained %d credential entries", remainingCredentials)
	}
}

func TestResolveCleanupWithoutProcessesDoesNotIssueTermination(t *testing.T) {
	fixture, native, credential, host, item, hostFingerprint := startCleanupSSHFixture(t, false)
	resolved, err := native.ResolveCleanup(context.Background(), hostFingerprint, host, host.PublicKeyFingerprint, item, credential, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "CLEAN" {
		t.Fatalf("cleanup without processes status=%q want CLEAN", resolved.Status)
	}
	for _, command := range fixture.commandSnapshot() {
		if strings.Contains(command, "kill -TERM") {
			t.Fatalf("cleanup without processes issued a termination command: %s", command)
		}
	}
}

func TestResolveCleanupRejectsIdentityMismatchBeforeOwnershipChecks(t *testing.T) {
	fixture, native, credential, host, item, hostFingerprint := startCleanupSSHFixture(t, false)
	item.MachineID = "unexpected-machine"
	if _, err := native.ResolveCleanup(context.Background(), hostFingerprint, host, host.PublicKeyFingerprint, item, credential, true); err == nil || !strings.Contains(err.Error(), "CLEANUP_IDENTITY_MISMATCH") {
		t.Fatalf("identity mismatch error=%v", err)
	}
	commands := fixture.commandSnapshot()
	if len(commands) != 1 || !strings.Contains(commands[0], "printf 'UID='") {
		t.Fatalf("identity mismatch proceeded beyond fact discovery: %v", commands)
	}
}

func TestResolveCleanupRejectsUnsafeOwnershipBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*report.CleanupItem)
	}{
		{name: "owner token", edit: func(item *report.CleanupItem) { item.OwnerToken = "../../etc/passwd" }},
		{name: "remote path", edit: func(item *report.CleanupItem) { item.RemotePath = "/tmp/../etc" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, native, credential, host, item, hostFingerprint := startCleanupSSHFixture(t, false)
			tc.edit(&item)
			if _, err := native.ResolveCleanup(context.Background(), hostFingerprint, host, host.PublicKeyFingerprint, item, credential, true); err == nil || !strings.Contains(err.Error(), "CLEANUP_OWNERSHIP_INVALID") {
				t.Fatalf("unsafe ownership error=%v", err)
			}
			for _, command := range fixture.commandSnapshot() {
				if strings.Contains(command, "kill -TERM") || strings.Contains(command, "rm -rf --") {
					t.Fatalf("unsafe cleanup issued mutation command: %s", command)
				}
			}
		})
	}
}
