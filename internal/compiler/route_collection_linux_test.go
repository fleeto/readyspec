//go:build linux

package compiler_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func routeSandboxCommand(root string, args ...string) *exec.Cmd {
	base := []string{"--die-with-parent", "--unshare-all", "--ro-bind", root, "/", "--ro-bind", "/usr/bin", "/bin", "--ro-bind", "/usr/sbin", "/usr/sbin", "--ro-bind", "/usr/lib", "/usr/lib", "--ro-bind", "/lib", "/lib", "--ro-bind", "/lib64", "/lib64", "--dev", "/dev", "--clearenv", "--setenv", "PATH", "/usr/sbin:/tools:/bin", "--setenv", "LC_ALL", "C", "--"}
	return exec.Command("bwrap", append(base, args...)...)
}

func requireRealIPRoute(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatalf("real route acceptance requires bubblewrap")
	}
	if _, err := os.Stat("/usr/sbin/ip"); err != nil {
		t.Fatalf("real route acceptance requires /usr/sbin/ip")
	}
}

func TestRouteCollectorObservesRealIsolatedRouting(t *testing.T) {
	for _, tt := range []struct {
		name, target, source, iface, gateway, status string
	}{
		{"loopback_route", "127.0.0.1", "", "lo", "", "PASS"},
		{"loopback_named_source", "127.0.0.1", "127.0.0.1", "lo", "", "PASS"},
		{"interface_mismatch", "127.0.0.1", "", "eth0", "", "FAIL"},
		{"gateway_absent", "127.0.0.1", "", "lo", "192.0.2.1", "FAIL"},
		{"unreachable_target", "203.0.113.1", "", "eth0", "", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requireRealIPRoute(t)
			root := collectionRoot(t)
			params := map[string]any{"target": tt.target, "interface": tt.iface}
			if tt.source != "" {
				params["source"] = tt.source
			}
			if tt.gateway != "" {
				params["gateway"] = tt.gateway
			}
			a := collectionArtifact(t, "N06", params)
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := routeSandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
			if err != nil && len(out) == 0 {
				t.Fatalf("real route artifact failed: %v", err)
			}
			results, err := compiler.Normalize(a, out)
			if err != nil {
				t.Fatalf("normalize: %v output=%s", err, out)
			}
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if tt.status == "PASS" {
				for _, entry := range results[0].Evidence {
					if entry.Source != "ip -j route get" || entry.StartTime == "" {
						t.Fatal("route source or collection time lost")
					}
				}
			}
		})
	}
}

func TestRouteCollectorValidatesPolicyMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, output, iface, gateway, status string
	}{
		{"policy_table_and_prefsrc", `[{"dev":"eth0","gateway":"192.0.2.1","table":"100","prefsrc":"192.0.2.10","uid":1000}]`, "eth0", "192.0.2.1", "PASS"},
		{"gateway_mismatch", `[{"dev":"eth0","gateway":"192.0.2.1","table":"100"}]`, "eth0", "192.0.2.2", "FAIL"},
		{"missing_device", `[{"gateway":"192.0.2.1"}]`, "eth0", "", "ERROR"},
		{"multiple_routes", `[{"dev":"eth0"},{"dev":"eth1"}]`, "eth0", "", "ERROR"},
		{"invalid_json", `not-json`, "eth0", "", "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "route-json", tt.output)
			fixtureTool(t, root, "tools/ip", `[ "$*" = '-j route get 192.0.2.2' ] || exit 99; cat /route-json`)
			params := map[string]any{"target": "192.0.2.2", "interface": tt.iface}
			if tt.gateway != "" {
				params["gateway"] = tt.gateway
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "N06", params))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}

func TestPolicyRouteCollectorObservesRuleSelection(t *testing.T) {
	root := collectionRoot(t)
	params := map[string]any{"target": "203.0.113.1", "source": "192.0.2.1", "interface": "dummy0", "gateway": "192.0.2.2"}
	a := collectionArtifact(t, "N06", params)
	if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
		t.Fatal(err)
	}
	setup := `set -eu
ip link add dummy0 type dummy
ip link set dummy0 up
ip address add 192.0.2.1/24 dev dummy0
ip route add default via 192.0.2.2 dev dummy0 table 100
ip rule add from 192.0.2.1 lookup 100
exec /goss -g /goss.json validate --format json
`
	cmd := routeSandboxCommand(root, "/bin/sh", "-c", setup)
	cmd.Args = append(cmd.Args[:1], append([]string{"--uid", "0", "--gid", "0", "--cap-add", "CAP_NET_ADMIN"}, cmd.Args[1:]...)...)
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		if strings.Contains(diagnostics.String(), "Operation not permitted") && os.Getenv("READYSPEC_REQUIRE_COLLECTION_SANDBOX") != "1" {
			t.Skip("policy routing fixture requires CAP_NET_ADMIN: " + diagnostics.String())
		}
		t.Fatalf("policy routing setup/artifact failed: %v: %s", err, diagnostics.String())
	}
	results, err := compiler.Normalize(a, out)
	if err != nil || len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("policy route was not observed: %+v err=%v output=%s", results, err, out)
	}
	if evidenceProperty(t, results, "interface") != "dummy0" {
		t.Fatal("selected policy route interface missing")
	}
}
