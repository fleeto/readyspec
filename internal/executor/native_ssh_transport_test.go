package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func loopbackSSHClient(t *testing.T, handle func(ssh.Channel, <-chan *ssh.Request, string)) *ssh.Client {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer listener.Close()
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
				for request := range requests {
					if request.Type == "exec" {
						var command struct{ Value string }
						_ = ssh.Unmarshal(request.Payload, &command)
						_ = request.Reply(true, nil)
						handle(channel, requests, command.Value)
						if command.Value != "disconnect" {
							_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
						}
						_ = channel.Close()
						return
					}
				}
			}()
		}
		_ = serverConn.Close()
	}()
	clientConfig := &ssh.ClientConfig{User: "test", HostKeyCallback: ssh.InsecureIgnoreHostKey()}
	client, err := ssh.Dial("tcp", listener.Addr().String(), clientConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-serverDone:
		case <-time.After(time.Second):
			t.Error("loopback SSH server did not stop")
		}
	})
	return client
}

func TestRunSSHRejectsRemoteOutputBeyondLimit(t *testing.T) {
	client := loopbackSSHClient(t, func(channel ssh.Channel, _ <-chan *ssh.Request, _ string) {
		chunk := make([]byte, 32*1024)
		for written := 0; written < commandOutputLimit+1; written += len(chunk) {
			if _, err := channel.Write(chunk); err != nil {
				break
			}
		}
	})
	_, err := runSSH(context.Background(), client, "large-output", nil)
	if err == nil || !errors.Is(err, errOutputLimit) {
		t.Fatalf("runSSH error=%v, want output limit error", err)
	}
}

func TestRunSSHStopsSessionWhenExecutionContextExpires(t *testing.T) {
	client := loopbackSSHClient(t, func(_ ssh.Channel, requests <-chan *ssh.Request, _ string) {
		for range requests {
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runSSH(ctx, client, "wait-forever", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runSSH error=%v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("runSSH did not stop promptly after deadline")
	}
}

func TestRunSSHStopsSessionWhenRunIsCancelled(t *testing.T) {
	started := make(chan struct{})
	client := loopbackSSHClient(t, func(_ ssh.Channel, requests <-chan *ssh.Request, _ string) {
		close(started)
		for range requests {
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := runSSH(ctx, client, "cancel-me", nil); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("loopback SSH command did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runSSH error=%v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runSSH did not stop promptly after Run cancellation")
	}
}

func TestVerifyRemoteSHA256RejectsMismatch(t *testing.T) {
	client := loopbackSSHClient(t, func(channel ssh.Channel, _ <-chan *ssh.Request, command string) {
		if !strings.Contains(command, "sha256sum") {
			t.Errorf("unexpected checksum command %q", command)
		}
		_, _ = channel.Write([]byte(strings.Repeat("0", 64) + "  /tmp/fixture\n"))
	})
	if err := verifyRemoteSHA256(context.Background(), client, "/tmp/fixture", []byte("expected bytes")); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
}

func TestMarkedExecutionIsNotRetriedAfterTransportDisconnect(t *testing.T) {
	client := loopbackSSHClient(t, func(ssh.Channel, <-chan *ssh.Request, string) {})
	journal := &journalProbe{}
	ctx := context.Background()
	commands := 0
	execute := func() error {
		if err := markExecutionStarted(ctx, journal, "run-disconnect", "host", "STANDARD"); err != nil {
			return err
		}
		commands++
		_, err := runSSH(ctx, client, "disconnect", nil)
		return err
	}
	if err := execute(); err == nil {
		t.Fatal("unexpected transport disconnect succeeded")
	}
	if err := execute(); err == nil {
		t.Fatal("durably marked mode was allowed to retry")
	}
	if commands != 1 {
		t.Fatalf("remote command attempted %d times after disconnect", commands)
	}
}
