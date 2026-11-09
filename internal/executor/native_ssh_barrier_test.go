package executor_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/executor"
	"github.com/fleeto/readyspec/internal/inventory"
	"github.com/fleeto/readyspec/internal/plan"
	"github.com/fleeto/readyspec/internal/runmanager"
	"github.com/fleeto/readyspec/internal/runstore"
	"github.com/fleeto/readyspec/internal/template"
	"github.com/fleeto/readyspec/internal/workspace"
	"golang.org/x/crypto/ssh"
)

type barrierSSHServer struct {
	listener net.Listener
	commands chan string
	done     chan struct{}
}

func startBarrierSSHServer(t *testing.T, hostSigner ssh.Signer, acceptedKey ssh.PublicKey, facts string, rotateAfterFirst ...ssh.Signer) *barrierSSHServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &barrierSSHServer{listener: listener, commands: make(chan string, 32), done: make(chan struct{})}
	go func() {
		defer close(server.done)
		connectionNumber := 0
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connectionNumber++
			activeSigner := hostSigner
			if connectionNumber > 1 && len(rotateAfterFirst) > 0 {
				activeSigner = rotateAfterFirst[0]
			}
			config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
				if string(key.Marshal()) != string(acceptedKey.Marshal()) {
					return nil, fmt.Errorf("public key rejected")
				}
				return nil, nil
			}}
			config.AddHostKey(activeSigner)
			go func(conn net.Conn, config *ssh.ServerConfig) {
				serverConn, channels, requests, err := ssh.NewServerConn(conn, config)
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
							server.commands <- payload.Command
							_ = request.Reply(true, nil)
							if strings.Contains(payload.Command, "probe_dir()") {
								_, _ = channel.Write([]byte("DIR\t/tmp\ttrue\ttrue\ttrue\tfalse\nDIR\t/var/tmp\ttrue\ttrue\ttrue\tfalse\nDIR\t/run\ttrue\ttrue\ttrue\tfalse\nHOME=/home/test\nMOUNT\t36 25 0:32 / / rw,nosuid - ext4 /dev/root rw\n"))
							} else if strings.Contains(payload.Command, "printf 'UID='") {
								_, _ = channel.Write([]byte(facts))
							}
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
							return
						}
					}()
				}
				_ = serverConn.Close()
			}(conn, config)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-server.done:
		case <-time.After(time.Second):
			t.Error("SSH test server did not stop")
		}
	})
	return server
}

func TestNativeSSHGlobalPreflightPreventsUploadsToEligibleHost(t *testing.T) {
	if _, err := os.Stat("/etc/machine-id"); err != nil {
		t.Skip("host identity unavailable")
	}
	_, clientPrivateA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivateA)
	if err != nil {
		t.Fatal(err)
	}
	_, rejectedPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rejectedSigner, err := ssh.NewSignerFromKey(rejectedPrivate)
	if err != nil {
		t.Fatal(err)
	}
	_, hostPrivateA, _ := ed25519.GenerateKey(rand.Reader)
	_, hostPrivateB, _ := ed25519.GenerateKey(rand.Reader)
	hostSignerA, _ := ssh.NewSignerFromKey(hostPrivateA)
	hostSignerB, _ := ssh.NewSignerFromKey(hostPrivateB)
	factsA := "UID=1000\nARCH=x86_64\nMACHINE=machine-a\nBOOT=boot-a\nCHECKSUM=/usr/bin/sha256sum\n"
	factsB := "UID=1000\nARCH=x86_64\nMACHINE=machine-b\nBOOT=boot-b\nCHECKSUM=/usr/bin/sha256sum\n"
	serverA := startBarrierSSHServer(t, hostSignerA, clientSigner.PublicKey(), factsA)
	serverB := startBarrierSSHServer(t, hostSignerB, rejectedSigner.PublicKey(), factsB)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	entries := fmt.Sprintf("[127.0.0.1]:%d %s\n[127.0.0.1]:%d %s\n", serverA.listener.Addr().(*net.TCPAddr).Port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSignerA.PublicKey()))), serverB.listener.Addr().(*net.TCPAddr).Port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSignerB.PublicKey()))))
	if err := os.WriteFile(knownHosts, []byte(entries), 0600); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := template.Lookup("S01", "1.0.0")
	if !ok {
		t.Fatal("S01 template unavailable")
	}
	item := plan.CheckItem{ItemID: "passwd_exists", Name: "passwd exists", TemplateID: "S01", TemplateVersion: "1.0.0", TemplateDigest: snapshot["digest"].(string), Importance: "REQUIRED", Enabled: true, Params: map[string]any{"path": "/etc/passwd", "exists": true, "filetype": "file"}, ExplicitNullParams: []string{}, OverridableParams: []string{}}
	doc := map[string]any{"schema_version": "readyspec.plan/v1", "plan_id": "preflight-barrier", "name": "Preflight barrier", "software_name": "fixture", "software_version": "1", "version": "1.0.0", "state": "DRAFT", "topology_policy": map[string]any{"allowed_arch": []string{"x86_64"}}, "roles": []plan.Role{{RoleID: "app", Name: "App", MinNodes: 1, AllowColocation: false, Items: []plan.CheckItem{item}}}, "embedded_templates": []any{snapshot}, "template_lock": map[string]any{"S01@1.0.0": snapshot["digest"]}}
	raw, _ := json.Marshal(doc)
	p, err := plan.Load(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	native := executor.NewNativeSSH()
	native.KnownHostsPath = knownHosts
	manager, err := runmanager.New(store, native)
	if err != nil {
		t.Fatal(err)
	}
	hosts := []inventory.Host{{ID: "node-a", Address: "127.0.0.1", Port: serverA.listener.Addr().(*net.TCPAddr).Port, Username: "tester"}, {ID: "node-b", Address: "127.0.0.1", Port: serverB.listener.Addr().(*net.TCPAddr).Port, Username: "tester"}}
	fingerprint := ssh.FingerprintSHA256(clientSigner.PublicKey())
	accepted, created, err := manager.Submit(runstore.Request{IdempotencyKey: "native-barrier", Plan: p, Config: runstore.RunConfig{PlanDigest: p.Digest, Mode: "TRIAL", Scope: "FULL", Backend: "native_ssh", Inventory: hosts, RoleMap: map[string][]string{"app": {"node-a", "node-b"}}, Auth: runstore.AuthIdentity{Method: "private_key", PublicKeyFingerprint: fingerprint}}}, map[string]executor.Credential{"node-a": {Method: "private_key", Signer: clientSigner}, "node-b": {Method: "private_key", Signer: clientSigner}})
	if err != nil || !created {
		t.Fatalf("submit: created=%v err=%v", created, err)
	}
	if _, err := manager.ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	run, err := store.Get(accepted.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.TerminationReason != "AUTH_PREFLIGHT_REJECTED" {
		t.Fatalf("unexpected refusal report: %+v", run.Report)
	}
	for _, server := range []*barrierSSHServer{serverA, serverB} {
		for {
			select {
			case command := <-server.commands:
				if strings.Contains(command, "cat >") || strings.Contains(command, "/goss") {
					t.Fatalf("remote upload/check command ran before global preflight completed: %q", command)
				}
			default:
				goto drained
			}
		}
	drained:
	}
}

func TestNativeSSHRejectsHostKeyRotationBetweenPreflightAndExecution(t *testing.T) {
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	_, hostPrivateA, _ := ed25519.GenerateKey(rand.Reader)
	_, hostPrivateB, _ := ed25519.GenerateKey(rand.Reader)
	hostSignerA, _ := ssh.NewSignerFromKey(hostPrivateA)
	hostSignerB, _ := ssh.NewSignerFromKey(hostPrivateB)
	facts := "UID=1000\nARCH=x86_64\nMACHINE=machine-stable\nBOOT=boot-stable\nCHECKSUM=/usr/bin/sha256sum\n"
	server := startBarrierSSHServer(t, hostSignerA, clientSigner.PublicKey(), facts, hostSignerB)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	entry := fmt.Sprintf("[127.0.0.1]:%d %s\n", server.listener.Addr().(*net.TCPAddr).Port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSignerA.PublicKey()))))
	if err := os.WriteFile(knownHosts, []byte(entry), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := template.Lookup("S01", "1.0.0")
	if !ok {
		t.Fatal("S01 template unavailable")
	}
	item := plan.CheckItem{ItemID: "passwd_exists", Name: "passwd exists", TemplateID: "S01", TemplateVersion: "1.0.0", TemplateDigest: snapshot["digest"].(string), Importance: "REQUIRED", Enabled: true, Params: map[string]any{"path": "/etc/passwd", "exists": true, "filetype": "file"}, ExplicitNullParams: []string{}, OverridableParams: []string{}}
	doc := map[string]any{"schema_version": "readyspec.plan/v1", "plan_id": "host-key-rotation", "name": "Host key rotation", "software_name": "fixture", "software_version": "1", "version": "1.0.0", "state": "DRAFT", "topology_policy": map[string]any{"allowed_arch": []string{"x86_64"}}, "roles": []plan.Role{{RoleID: "app", Name: "App", MinNodes: 1, AllowColocation: false, Items: []plan.CheckItem{item}}}, "embedded_templates": []any{snapshot}, "template_lock": map[string]any{"S01@1.0.0": snapshot["digest"]}}
	raw, _ := json.Marshal(doc)
	p, err := plan.Load(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	lease, err := ws.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	store, err := runstore.Open(ws)
	if err != nil {
		t.Fatal(err)
	}
	native := executor.NewNativeSSH()
	native.KnownHostsPath = knownHosts
	manager, err := runmanager.New(store, native)
	if err != nil {
		t.Fatal(err)
	}
	host := inventory.Host{ID: "node", Address: "127.0.0.1", Port: server.listener.Addr().(*net.TCPAddr).Port, Username: "tester"}
	fingerprint := ssh.FingerprintSHA256(clientSigner.PublicKey())
	accepted, _, err := manager.Submit(runstore.Request{IdempotencyKey: "host-key-rotation", Plan: p, Config: runstore.RunConfig{PlanDigest: p.Digest, Mode: "TRIAL", Scope: "FULL", Backend: "native_ssh", Inventory: []inventory.Host{host}, RoleMap: map[string][]string{"app": {"node"}}, Auth: runstore.AuthIdentity{Method: "private_key", PublicKeyFingerprint: fingerprint}}}, map[string]executor.Credential{"node": {Method: "private_key", Signer: clientSigner}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ProcessNext(context.Background()); err != nil {
		t.Fatal(err)
	}
	run, err := store.Get(accepted.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || len(run.Report.Failures) == 0 || run.Report.Failures[0].Code != "IDENTITY_ERROR" || run.Report.Failures[0].Phase != "REVALIDATE" {
		t.Fatalf("host-key rotation was not classified as an identity failure: %+v", run.Report)
	}
	for {
		select {
		case command := <-server.commands:
			if !strings.Contains(command, "printf 'UID='") && !strings.Contains(command, "probe_dir()") {
				t.Fatalf("unexpected remote command after host-key rotation: %q", command)
			}
		default:
			return
		}
	}
}
