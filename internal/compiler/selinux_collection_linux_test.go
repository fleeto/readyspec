//go:build linux

package compiler_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSELinuxCollectorUsesKernelState(t *testing.T) {
	for _, tt := range []struct {
		name, getenforce, enforce, mode, status string
		selinuxDir, denyEnforce                 bool
	}{
		{"getenforce_enforcing", "Enforcing\n", "", "enforcing", "PASS", false, false},
		{"getenforce_permissive_mismatch", "Permissive\n", "", "enforcing", "FAIL", false, false},
		{"getenforce_disabled", "Disabled\n", "", "disabled", "PASS", false, false},
		{"getenforce_garbage", "Unknown\n", "", "enforcing", "ERROR", false, false},
		{"sysfs_enforcing", "", "1\n", "enforcing", "PASS", true, false},
		{"sysfs_permissive", "", "0\n", "permissive", "PASS", true, false},
		{"sysfs_invalid", "", "2\n", "enforcing", "ERROR", true, false},
		{"kernel_without_framework", "", "", "disabled", "PASS", false, false},
		{"selinux_dir_without_readable_enforce", "", "1\n", "enforcing", "ERROR", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			if err := os.MkdirAll(filepath.Join(root, "sys/fs"), 0755); err != nil {
				t.Fatal(err)
			}
			if tt.getenforce != "" {
				fixtureTool(t, root, "tools/getenforce", "cat /getenforce-state")
				fixtureFile(t, root, "getenforce-state", tt.getenforce)
			}
			if tt.selinuxDir {
				fixtureFile(t, root, "sys/fs/selinux/enforce", tt.enforce)
				if tt.denyEnforce {
					path := filepath.Join(root, "sys/fs/selinux/enforce")
					if err := os.Chmod(path, 0000); err != nil {
						t.Fatal(err)
					}
				}
			}
			results := collectInSandbox(t, root, collectionArtifact(t, "A04", map[string]any{"framework": "selinux", "mode": tt.mode}))
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
		})
	}
}
