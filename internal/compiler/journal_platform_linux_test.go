//go:build linux

package compiler_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
)

// This opt-in test runs the original L02/L03 artifacts against copied real
// systemd journals. It never changes the host journal files.
func TestOriginalLogArtifactsInspectRealRotatedAndCorruptJournals(t *testing.T) {
	if os.Getenv("READYSPEC_TEST_REAL_JOURNAL") != "1" {
		t.Skip("set READYSPEC_TEST_REAL_JOURNAL=1 to inspect copied host journals")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Fatal("real journal test requires bubblewrap")
	}
	if _, err := exec.LookPath("journalctl"); err != nil {
		t.Fatal("real journal test requires journalctl")
	}
	machineID, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		t.Fatal(err)
	}
	machineID = []byte(strings.TrimSpace(string(machineID)))
	if len(machineID) == 0 {
		t.Fatal("system machine-id is empty")
	}
	source := filepath.Join("/run/log/journal", string(machineID))
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Skipf("no readable volatile system journal: %v", err)
	}
	var journals []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "system") && strings.Contains(entry.Name(), ".journal") {
			journals = append(journals, entry.Name())
		}
	}
	if len(journals) < 2 {
		t.Skipf("need an active journal and at least one rotated journal; found %d", len(journals))
	}

	for _, tc := range []struct {
		name, corrupt string
		want          string
	}{
		{name: "real_rotation", want: "PASS"},
		{name: "corrupt_active_journal", corrupt: "system.journal", want: "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := collectionRoot(t)
			journalTree := filepath.Join(t.TempDir(), "journal")
			targetDir := filepath.Join(journalTree, string(machineID))
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range journals {
				raw, err := os.ReadFile(filepath.Join(source, name))
				if err != nil {
					t.Fatal(err)
				}
				if name == tc.corrupt {
					if len(raw) == 0 {
						t.Fatal("active journal is empty")
					}
					raw[0] ^= 1 // damage the copied journal header only
				}
				if err := os.WriteFile(filepath.Join(targetDir, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			fixtureFile(t, root, "etc/machine-id", string(machineID)+"\n")
			uptime, err := os.ReadFile("/proc/uptime")
			if err != nil {
				t.Fatal(err)
			}
			fixtureFile(t, root, "proc/uptime", string(uptime))
			bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
			if err != nil {
				t.Fatal(err)
			}
			fixtureFile(t, root, "proc/sys/kernel/random/boot_id", string(bootID))
			if err := os.MkdirAll(filepath.Join(root, "run/log/journal"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/bin/journalctl", filepath.Join(root, "tools/journalctl")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/bin/date", filepath.Join(root, "tools/date")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/bin/find", filepath.Join(root, "tools/find")); err != nil {
				t.Fatal(err)
			}
			ids := []string{"L02", "L03"}
			for _, id := range ids {
				params := map[string]any{"lookback_seconds": 86400, "allow_since_boot": true, "max_events": 99999}
				if id == "L03" {
					params = map[string]any{"unit": "chronyd.service", "lookback_seconds": 86400, "allow_since_boot": true, "max_failures": 99999, "max_restarts": 99999}
				}
				a := collectionArtifact(t, id, params)
				if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
					t.Fatal(err)
				}
				cmd := sandboxCommand(root, "/bin/sh", "-c", `exec /goss -g /goss.json validate --format json`)
				cmd = insertSandboxOptions(cmd, "--ro-bind", journalTree, "/run/log/journal")
				out, runErr := cmd.Output()
				if runErr != nil && len(out) == 0 {
					t.Fatalf("%s original artifact execution failed: %v", id, runErr)
				}
				results, err := compiler.Normalize(a, out)
				if err != nil || len(results) != 1 || results[0].Status != tc.want {
					t.Fatalf("%s want %s got %+v err=%v output=%s", id, tc.want, results, err, out)
				}
			}
		})
	}
}
