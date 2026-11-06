//go:build windows

package executor

import (
	"fmt"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsNamedPipeAgentEndpointRoundTrip(t *testing.T) {
	endpoint := fmt.Sprintf(`\\.\pipe\readyspec-agent-%d`, os.Getpid())
	name, err := windows.UTF16PtrFromString(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := os.NewFile(uintptr(pipe), "readyspec-agent-pipe")
	defer server.Close()
	serverDone := make(chan error, 1)
	go func() {
		if err := windows.ConnectNamedPipe(pipe, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
			serverDone <- err
			return
		}
		buffer := make([]byte, 4)
		if _, err := io.ReadFull(server, buffer); err != nil {
			serverDone <- err
			return
		}
		if string(buffer) != "ping" {
			serverDone <- fmt.Errorf("unexpected payload %q", buffer)
			return
		}
		_, err := server.Write([]byte("pong"))
		serverDone <- err
	}()
	client, err := dialAgentEndpoint(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(client, response); err != nil || string(response) != "pong" {
		t.Fatalf("named-pipe response=%q err=%v", response, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAgentEndpointRejectsNonLocalPipePaths(t *testing.T) {
	for _, endpoint := range []string{`\\server\pipe\agent`, `\\.\pipe\..\agent`, `tcp://127.0.0.1:1234`} {
		if _, err := dialAgentEndpoint(endpoint); err == nil {
			t.Errorf("accepted unsafe Agent endpoint %q", endpoint)
		}
	}
}

func TestWindowsAgentPipeServerIdentityBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		serverSID  string
		currentSID string
		want       bool
	}{
		{name: "same user", serverSID: "S-1-5-21-100", currentSID: "S-1-5-21-100", want: true},
		{name: "LocalSystem agent", serverSID: "S-1-5-18", currentSID: "S-1-5-21-100", want: true},
		{name: "different user", serverSID: "S-1-5-21-200", currentSID: "S-1-5-21-100", want: false},
		{name: "missing server identity", currentSID: "S-1-5-21-100", want: false},
		{name: "missing caller identity", serverSID: "S-1-5-21-100", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustedNamedPipeServerSID(tc.serverSID, tc.currentSID); got != tc.want {
				t.Fatalf("trustedNamedPipeServerSID()=%v want=%v", got, tc.want)
			}
		})
	}
}
