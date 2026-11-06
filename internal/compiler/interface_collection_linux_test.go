//go:build linux

package compiler_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func TestInterfaceCollectorObservesRealNetworkNamespace(t *testing.T) {
	for _, tt := range []struct {
		name        string
		up, missing bool
		addresses   []any
		status      string
	}{
		{"up_dual_stack", true, false, []any{"192.0.2.10/24", "2001:db8:1::10/64"}, "PASS"},
		{"second_bound_address", true, false, []any{"198.51.100.20/25"}, "PASS"},
		{"cidr_membership", true, false, []any{"192.0.2.0/24", "2001:db8:1::/64"}, "PASS"},
		{"missing_address", true, false, []any{"203.0.113.10/24"}, "FAIL"},
		{"all_addresses_required", true, false, []any{"192.0.2.10/24", "203.0.113.10/24"}, "FAIL"},
		{"expected_down", false, false, []any{}, "PASS"},
		{"missing_interface", true, true, []any{}, "FAIL"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			artifact := collectionArtifact(t, "N01", map[string]any{"name": "rsprobe0", "up": tt.up, "addresses": tt.addresses})
			if err := os.WriteFile(filepath.Join(root, "goss.json"), artifact.Config, 0600); err != nil {
				t.Fatal(err)
			}
			setup := `set -eu
ip link add rsprobe0 type dummy
ip address add 192.0.2.10/24 dev rsprobe0
ip address add 198.51.100.20/25 dev rsprobe0
ip -6 address add 2001:db8:1::10/64 dev rsprobe0 nodad
`
			if tt.up {
				setup += "ip link set rsprobe0 up\n"
			}
			if tt.missing {
				setup += "ip link del rsprobe0\n"
			}
			setup += `exec /goss -g /goss.json validate --format json`
			cmd := sandboxCommand(root, "/bin/sh", "-c", setup)
			// iproute2 lives in /usr/sbin on several distributions and the base
			// sandbox only binds /usr/bin; expose the read-only tool directory.
			for i, arg := range cmd.Args {
				if arg == "--" {
					args := append([]string{}, cmd.Args[:i]...)
					args = append(args, "--ro-bind", "/usr/sbin", "/usr/sbin", "--setenv", "PATH", "/usr/sbin:/tools:/bin")
					cmd.Args = append(args, cmd.Args[i:]...)
					break
				}
			}
			// CAP_NET_ADMIN applies only to the new user/network namespaces. No
			// host interface or routing table is configured by this test.
			cmd.Args = append(cmd.Args[:1], append([]string{"--uid", "0", "--gid", "0", "--cap-add", "CAP_NET_ADMIN"}, cmd.Args[1:]...)...)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			out, err := cmd.Output()
			if err != nil && len(out) == 0 {
				if strings.Contains(diagnostics.String(), "RTNETLINK answers: Operation not permitted") && os.Getenv("READYSPEC_REQUIRE_COLLECTION_SANDBOX") != "1" {
					t.Skip("network fixture requires permission to configure interfaces inside the isolated network namespace: " + diagnostics.String())
				}
				t.Fatalf("network setup/original artifact failed: %v: %s", err, diagnostics.String())
			}
			results, err := compiler.Normalize(artifact, out)
			if err != nil || len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v err=%v output=%s", tt.status, results, err, out)
			}
			if !tt.missing {
				if actual := evidenceProperty(t, results, "name"); actual != "rsprobe0" {
					t.Fatalf("interface name missing: %v", actual)
				}
				if evidenceProperty(t, results, "up") != tt.up {
					t.Fatal("observed administrative state lost")
				}
				raw, _ := json.Marshal(evidenceProperty(t, results, "addresses"))
				if len(raw) < 10 {
					t.Fatalf("bound address set missing: %s", raw)
				}
				for _, entry := range results[0].Evidence {
					if entry.Source != "ip -j address" || entry.StartTime == "" || entry.EndTime == "" {
						t.Fatal("source or collection times lost")
					}
				}
			} else if evidenceProperty(t, results, "exists") != false {
				t.Fatal("missing interface must be explicit")
			}
		})
	}
}
