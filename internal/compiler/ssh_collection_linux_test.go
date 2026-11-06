//go:build linux

package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

func sshdSandboxCommand(root string, args ...string) *exec.Cmd {
	base := []string{"--die-with-parent", "--unshare-all", "--ro-bind", root, "/", "--ro-bind", "/usr/bin", "/bin", "--ro-bind", "/usr/sbin", "/usr/sbin", "--ro-bind", "/usr/lib", "/usr/lib", "--ro-bind", "/lib", "/lib", "--ro-bind", "/lib64", "/lib64", "--dev", "/dev", "--clearenv", "--setenv", "PATH", "/tools:/bin", "--setenv", "LC_ALL", "C", "--"}
	return exec.Command("bwrap", append(base, args...)...)
}

func requireRealSSHD(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatalf("real sshd acceptance requires bubblewrap")
	}
	if _, err := os.Stat("/usr/sbin/sshd"); err != nil {
		t.Fatalf("real sshd acceptance requires /usr/sbin/sshd")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Fatalf("real sshd acceptance requires ssh-keygen for a fixture host key")
	}
	if err := os.MkdirAll(filepath.Join(root, "run/sshd"), 0755); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "etc/ssh/ssh_host_ed25519_key")
	if _, err := os.Stat(key); err == nil {
		return
	}
	command := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture host key: %v %s", err, output)
	}
}

func TestSSHCollectorUsesRealSSHDConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name, config, key, expected, context, status string
		withHostKey                                  bool
	}{
		{"configured_no", "PermitRootLogin no\n", "permitrootlogin", "no", "", "PASS", true},
		{"expected_mismatch", "PermitRootLogin yes\n", "permitrootlogin", "no", "", "FAIL", true},
		{"match_context", "PermitRootLogin no\nMatch User app\n    PermitRootLogin yes\n", "permitrootlogin", "yes", "user=app", "PASS", true},
		{"match_default_outside", "PermitRootLogin no\nMatch User app\n    PermitRootLogin yes\n", "permitrootlogin", "no", "", "PASS", true},
		{"invalid_configuration", "BogusKeyword yes\n", "permitrootlogin", "no", "", "ERROR", true},
		{"query_failure_with_config_present", "PermitRootLogin no\n", "permitrootlogin", "no", "", "ERROR", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "etc/ssh/sshd_config", tt.config)
			if tt.withHostKey {
				requireRealSSHD(t, root)
			}
			params := map[string]any{"key": tt.key, "expected": tt.expected}
			if tt.context != "" {
				params["match_context"] = tt.context
			}
			a := collectionArtifact(t, "A03", params)
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := sshdSandboxCommand(root, "/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
			if err != nil && len(out) == 0 {
				t.Fatalf("real sshd artifact failed: %v", err)
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
					if entry.Source != "/usr/sbin/sshd -T" || entry.StartTime == "" {
						t.Fatal("sshd source or collection time lost")
					}
				}
			}
		})
	}
}
