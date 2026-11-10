package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func policyTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestDefaultHostKeyPolicyRecordsWithoutReadingOrWritingKnownHosts(t *testing.T) {
	for _, state := range []string{"missing", "empty", "invalid", "changed", "revoked"} {
		t.Run(state, func(t *testing.T) {
			signer := policyTestSigner(t)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				config := &ssh.ServerConfig{NoClientAuth: true}
				config.AddHostKey(signer)
				server, _, _, err := ssh.NewServerConn(conn, config)
				if err == nil {
					server.Close()
				}
			}()
			file := filepath.Join(t.TempDir(), "known_hosts")
			content := ""
			switch state {
			case "invalid":
				content = "private-invalid-known-hosts-fixture\n"
			case "changed":
				content = knownhosts.Line([]string{listener.Addr().String()}, policyTestSigner(t).PublicKey()) + "\n"
			case "revoked":
				content = "@revoked " + knownhosts.Line([]string{listener.Addr().String()}, signer.PublicKey()) + "\n"
			}
			if state != "missing" {
				if err := os.WriteFile(file, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			native := NewNativeSSH()
			native.KnownHostsPath = file
			native.Credentials = CredentialMap{"run": {"node": {Method: "password", Password: []byte("fixture")}}}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, err := native.connect(ctx, "run", "node", "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "tester", "password", "")
			if err != nil {
				t.Fatalf("%s known_hosts blocked default connection: %v", state, err)
			}
			defer conn.Close()
			if conn.keyFP != ssh.FingerprintSHA256(signer.PublicKey()) {
				t.Fatal("presented host key fingerprint was not recorded")
			}
			raw, err := os.ReadFile(file)
			if state == "missing" {
				if !os.IsNotExist(err) {
					t.Fatal("connection created a known_hosts file")
				}
			} else if err != nil || string(raw) != content {
				t.Fatal("connection changed known_hosts")
			}
		})
	}
}

func TestRecordOnlyReconnectPinsFingerprintBeforeSendingCredentials(t *testing.T) {
	first, changed := policyTestSigner(t), policyTestSigner(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var authentications atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, signer := range []ssh.Signer{first, changed} {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { authentications.Add(1); return nil, nil }}
			config.AddHostKey(signer)
			server, _, _, err := ssh.NewServerConn(conn, config)
			if err == nil {
				server.Close()
			}
			conn.Close()
		}
	}()
	native := NewNativeSSH()
	native.KnownHostsPath = filepath.Join(t.TempDir(), "absent")
	native.Credentials = CredentialMap{"run": {"node": {Method: "password", Password: []byte("fixture")}}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	port := listener.Addr().(*net.TCPAddr).Port
	conn, err := native.connect(ctx, "run", "node", "127.0.0.1", port, "tester", "password", "")
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := conn.keyFP
	conn.Close()
	_, err = native.connectPinned(ctx, "run", "node", "127.0.0.1", port, "tester", "password", "", fingerprint)
	if err == nil || connectFailure(err, "node").Diagnostics[0].Code != "HOST_KEY_CHANGED" {
		t.Fatalf("changed reconnect was accepted: %v", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("SSH fixture did not stop")
	}
	if authentications.Load() != 1 {
		t.Fatalf("credentials sent to changed key: authentications=%d", authentications.Load())
	}
}
