package executor_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
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

func TestNativeSSHLoopbackStandardAndPrivilegedVerticalSlice(t *testing.T) {
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Skip("loopback SSH integration requires sshd")
	}
	username, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/etc/machine-id"); err != nil {
		t.Skip("host machine identity is unavailable")
	}
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		t.Skip("privileged vertical slice requires sudo")
	}
	policyQuery := exec.Command(sudoPath, "-n", "-l", "--", "/usr/bin/env", "-i", "PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "HOME=/root", "/bin/sh", "-c", "true")
	if output, err := policyQuery.CombinedOutput(); err != nil {
		t.Skipf("local account cannot run a controlled non-interactive sudo command: %s", output)
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	serverPublic, serverPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverSigner, err := ssh.NewSignerFromKey(serverPrivate)
	if err != nil {
		t.Fatal(err)
	}
	serverPEM, err := ssh.MarshalPrivateKey(serverPrivate, "readyspec-test-host")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host_key"), pem.EncodeToMemory(serverPEM), 0600); err != nil {
		t.Fatal(err)
	}
	// Only the ed25519 key is trusted; the server also offers an ECDSA key.
	ecdsaPrivate, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaPEM, err := ssh.MarshalPrivateKey(ecdsaPrivate, "unregistered-host-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "host_key_ecdsa"), pem.EncodeToMemory(ecdsaPEM), 0600); err != nil {
		t.Fatal(err)
	}
	clientPublic, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "authorized_keys"), ssh.MarshalAuthorizedKey(clientSigner.PublicKey()), 0600); err != nil {
		t.Fatal(err)
	}
	_ = clientPublic

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	config := strings.Join([]string{
		"Port " + fmt.Sprint(port), "ListenAddress 127.0.0.1", "HostKey " + filepath.Join(dir, "host_key"),
		"HostKey " + filepath.Join(dir, "host_key_ecdsa"),
		"PidFile " + filepath.Join(dir, "sshd.pid"), "AuthorizedKeysFile " + filepath.Join(dir, "authorized_keys"),
		"StrictModes no", "PasswordAuthentication no", "PubkeyAuthentication yes", "UsePAM no",
		"PermitRootLogin no", "AllowUsers " + username.Username, "LogLevel ERROR", "Subsystem sftp internal-sftp",
	}, "\n") + "\n"
	configPath := filepath.Join(dir, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(sshd, "-t", "-f", configPath)
	if output, err := check.CombinedOutput(); err != nil {
		t.Skipf("sshd cannot run as this test user: %v: %s", err, output)
	}
	server := exec.Command(sshd, "-D", "-e", "-f", configPath)
	server.Stdout = os.Stderr
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Skipf("cannot start loopback sshd: %v", err)
	}
	defer func() {
		_ = server.Process.Kill()
		_, _ = server.Process.Wait()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loopback sshd did not listen: %v", dialErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	knownHosts := filepath.Join(dir, "known_hosts")
	knownHostLine := fmt.Sprintf("[127.0.0.1]:%d %s\n", port, ssh.MarshalAuthorizedKey(serverSigner.PublicKey()))
	knownHostLine = strings.ReplaceAll(knownHostLine, "\n", " ")
	if err := os.WriteFile(knownHosts, []byte(knownHostLine+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	templateSnapshot, ok := template.Lookup("S01", "1.0.0")
	if !ok {
		t.Fatal("S01 template is unavailable")
	}
	privilegedSnapshot, ok := template.Lookup("A03", "1.0.0")
	if !ok {
		t.Fatal("A03 template is unavailable")
	}
	item := plan.CheckItem{
		ItemID: "passwd_exists", Name: "passwd exists", TemplateID: "S01", TemplateVersion: "1.0.0",
		TemplateDigest: templateSnapshot["digest"].(string), Importance: "REQUIRED", Enabled: true,
		Params:             map[string]any{"path": "/etc/passwd", "exists": true, "filetype": "file"},
		ExplicitNullParams: []string{}, OverridableParams: []string{},
	}
	privilegedItem := plan.CheckItem{
		ItemID: "sshd_root_login", Name: "sshd root login", TemplateID: "A03", TemplateVersion: "1.0.0",
		TemplateDigest: privilegedSnapshot["digest"].(string), Importance: "REQUIRED", Enabled: true,
		Params:             map[string]any{"key": "permitrootlogin", "expected": "no"},
		ExplicitNullParams: []string{}, OverridableParams: []string{},
	}
	doc := map[string]any{
		"schema_version": "readyspec.plan/v1", "plan_id": "loopback-privileged", "name": "Loopback standard and privileged",
		"software_name": "fixture", "software_version": "1", "version": "1.0.0", "state": "DRAFT",
		"topology_policy":    map[string]any{"allowed_arch": []string{"x86_64"}},
		"roles":              []plan.Role{{RoleID: "app", Name: "App", MinNodes: 1, AllowColocation: false, Items: []plan.CheckItem{item, privilegedItem}}},
		"embedded_templates": []any{templateSnapshot, privilegedSnapshot}, "template_lock": map[string]any{"S01@1.0.0": templateSnapshot["digest"], "A03@1.0.0": privilegedSnapshot["digest"]},
	}
	raw, _ := json.Marshal(doc)
	p, err := plan.Load(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	ws, err := workspace.Open(filepath.Join(dir, "workspace"))
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
	native.HostKeyPolicy = executor.HostKeyStrict
	native.RemoteTempDir = dir
	manager, err := runmanager.New(store, native)
	if err != nil {
		t.Fatal(err)
	}
	host := inventory.Host{ID: "node", Address: "127.0.0.1", Port: port, Username: username.Username}
	configValue := runstore.RunConfig{
		PlanDigest: p.Digest, Mode: "TRIAL", Scope: "FULL", Backend: "native_ssh",
		Inventory: []inventory.Host{host}, RoleMap: map[string][]string{"app": {"node"}},
		Auth:    runstore.AuthIdentity{Method: "private_key", PublicKeyFingerprint: ssh.FingerprintSHA256(clientSigner.PublicKey())},
		Budgets: runstore.Budgets{QueueSeconds: 60, RunSeconds: 120, HostSeconds: 60},
	}
	accepted, created, err := manager.Submit(runstore.Request{IdempotencyKey: "loopback-privileged", Plan: p, Config: configValue}, map[string]executor.Credential{
		"node": {Method: "private_key", Signer: clientSigner},
	})
	if err != nil || !created {
		t.Fatalf("submit loopback Run: created=%v err=%v", created, err)
	}
	if _, err := manager.ProcessNext(context.Background()); err != nil {
		t.Fatalf("process loopback Run: %v", err)
	}
	run, err := store.Get(accepted.Acceptance.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Report == nil || run.Report.RunStatus != "COMPLETED" || len(run.Report.Hosts) != 1 || len(run.Report.Hosts[0].ItemResults) != 2 {
		t.Fatalf("unexpected loopback S01 result: %+v", run.Report)
	}
	if len(run.Report.CleanupItems) != 1 || run.Report.CleanupItems[0].Status != "CLEAN" || run.Report.CleanupItems[0].OwnerToken != "" {
		t.Fatalf("cleanup outcome was not safely archived: %+v", run.Report.CleanupItems)
	}
	cleanup := run.Report.CleanupItems[0]
	if !filepath.IsAbs(cleanup.RemotePath) || filepath.Dir(cleanup.RemotePath) != dir {
		t.Errorf("standard workspace did not use the selected absolute directory: %q", cleanup.RemotePath)
	}
	if !filepath.IsAbs(cleanup.PrivilegedPath) {
		t.Errorf("privileged workspace was not absolute: %q", cleanup.PrivilegedPath)
	}
	for _, path := range []string{cleanup.RemotePath, cleanup.PrivilegedPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("workspace %q was not removed: %v", path, err)
		}
	}
	for _, result := range run.Report.Hosts[0].ItemResults {
		if result.ItemID == "sshd_root_login" && result.Status != "PASS" && result.Status != "FAIL" {
			t.Fatalf("privileged A03 did not return a check observation: %+v", result)
		}
	}
	hostKey, err := ssh.NewPublicKey(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.HostKeyFP != ssh.FingerprintSHA256(hostKey) {
		t.Fatalf("cleanup did not retain the verified host identity: got %q", cleanup.HostKeyFP)
	}
}
