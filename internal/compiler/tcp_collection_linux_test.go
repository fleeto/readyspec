//go:build linux

package compiler_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

const multiAddressDNS = `import socket, struct
server = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
server.bind(('127.0.0.1', 53))
while True:
    data, address = server.recvfrom(512)
    name = data[12:]
    end = 0
    while name[end] != 0:
        end += 1 + name[end]
    question = name[:end+5]
    answers = b''
    for value in ('127.0.0.2', '127.0.0.1'):
        answers += b'\xc0\x0c' + struct.pack('>HHIH', 1, 1, 0, 4) + socket.inet_aton(value)
    response = data[:2] + struct.pack('>HHHHH', 0x8180, 1, 2, 0, 0) + question + answers
    server.sendto(response, address)
`

func TestTCPCollectorFollowsMultipleResolvedAddresses(t *testing.T) {
	for _, tt := range []struct {
		name      string
		listen    bool
		reachable bool
		status    string
	}{
		{"fallback_to_reachable_address", true, true, "PASS"},
		{"all_addresses_refused", false, false, "PASS"},
		{"all_addresses_refused_unexpected", false, true, "FAIL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "etc/resolv.conf", "nameserver 127.0.0.1\noptions attempts:1 timeout:1\n")
			fixtureFile(t, root, "etc/nsswitch.conf", "hosts: files dns\n")
			fixtureFile(t, root, "dns-server.py", multiAddressDNS)
			a := collectionArtifact(t, "N03", map[string]any{"address": "multi.test:23235", "reachable": tt.reachable, "timeout_ms": float64(1000)})
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			listener := "true"
			if tt.listen {
				listener = `python3 -c 'import socket,time
s=socket.socket()
s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(("127.0.0.1",23235))
s.listen(8)
time.sleep(6)' &`
			}
			setup := `set -eu
ip link set lo up
python3 /dns-server.py &
` + listener + `
sleep 0.7
exec /goss -g /goss.json validate --format json
`
			cmd := sandboxCommand(root, "/bin/sh", "-c", setup)
			cmd.Args = append(cmd.Args[:1], append([]string{"--uid", "0", "--gid", "0", "--cap-add", "CAP_NET_ADMIN", "--cap-add", "CAP_NET_BIND_SERVICE"}, cmd.Args[1:]...)...)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			out, err := cmd.Output()
			if err != nil && len(out) == 0 {
				if strings.Contains(diagnostics.String(), "Operation not permitted") && os.Getenv("READYSPEC_REQUIRE_COLLECTION_SANDBOX") != "1" {
					t.Skip("multi-address fixture requires CAP_NET_BIND_SERVICE: " + diagnostics.String())
				}
				t.Fatalf("multi-address setup/artifact failed: %v: %s", err, diagnostics.String())
			}
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v err=%v output=%s", tt.status, results, err, out)
			}
		})
	}
}

func TestTCPCollectorClassifiesTimeoutOnBlackholedRoute(t *testing.T) {
	root := collectionRoot(t)
	a := collectionArtifact(t, "N03", map[string]any{"address": "203.0.113.1:23236", "reachable": true, "timeout_ms": float64(300)})
	if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	setup := `set -eu
ip link add dummy0 type dummy
ip link set dummy0 up
ip address add 192.0.2.1/24 dev dummy0
ip route add 203.0.113.0/24 via 192.0.2.2 dev dummy0
exec /goss -g /goss.json validate --format json
`
	cmd := sandboxCommand(root, "/bin/sh", "-c", setup)
	cmd.Args = append(cmd.Args[:1], append([]string{"--uid", "0", "--gid", "0", "--cap-add", "CAP_NET_ADMIN"}, cmd.Args[1:]...)...)
	for i, arg := range cmd.Args {
		if arg == "--" {
			args := append([]string{}, cmd.Args[:i]...)
			args = append(args, "--ro-bind", "/usr/sbin", "/usr/sbin", "--setenv", "PATH", "/usr/sbin:/tools:/bin")
			cmd.Args = append(args, cmd.Args[i:]...)
			break
		}
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		if strings.Contains(diagnostics.String(), "Operation not permitted") && os.Getenv("READYSPEC_REQUIRE_COLLECTION_SANDBOX") != "1" {
			t.Skip("timeout fixture requires CAP_NET_ADMIN: " + diagnostics.String())
		}
		t.Fatalf("timeout fixture/artifact failed: %v: %s", err, diagnostics.String())
	}
	results, err := compiler.Normalize(a, out)
	if err != nil || len(results) != 1 || results[0].Status != "ERROR" {
		t.Fatalf("timeout must not be treated as reachable: %+v err=%v output=%s", results, err, out)
	}
	if raw := fmt.Sprint(evidenceProperty(t, results, "stdout")); !strings.Contains(raw, "TIMEOUT") {
		t.Fatalf("timeout reason was not preserved: %s", raw)
	}
}
