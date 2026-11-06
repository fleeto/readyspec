//go:build linux

package compiler_test

import (
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fleeto/readyspec/internal/compiler"
	"github.com/fleeto/readyspec/internal/plan"
)

func fixtureStatScript(statistics string) string {
	return `case "$*" in *' -- /data') :;; *) exit 2;; esac
case "$1" in -f) printf '%s\n' '` + strings.ReplaceAll(statistics, "'", "'\"'\"'") + `';; --printf) printf '/data\n';; *) exit 2;; esac`
}

func evidenceProperty(t *testing.T, results []compiler.ItemResult, property string) any {
	t.Helper()
	for _, result := range results {
		for _, evidence := range result.Evidence {
			if evidence.Property == property {
				return evidence.Actual
			}
			if fields, ok := evidence.Actual.(map[string]any); ok {
				if value, exists := fields[property]; exists {
					return value
				}
			}
		}
	}
	t.Fatalf("required evidence %s missing: %+v", property, results)
	return nil
}

func TestFileCollectorRetainsOwnershipWithoutOwnershipAssertions(t *testing.T) {
	root := collectionRoot(t)
	fixtureFile(t, root, "data", "content")
	fixtureFile(t, root, "etc/passwd", fmt.Sprintf("fixture-user:x:%d:%d::/app:/bin/sh\n", os.Getuid(), os.Getgid()))
	fixtureFile(t, root, "etc/group", fmt.Sprintf("fixture-group:x:%d:\n", os.Getgid()))
	a := collectionArtifact(t, "S02", map[string]any{"path": "/data", "mode": "0644"})
	results := collectInSandbox(t, root, a)
	if len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("unexpected %+v", results)
	}
	for property, expected := range map[string]string{"uid": fmt.Sprint(os.Getuid()), "gid": fmt.Sprint(os.Getgid()), "owner": "fixture-user", "group": "fixture-group", "mode": "0644"} {
		if actual := fmt.Sprint(evidenceProperty(t, results, property)); actual != expected {
			t.Fatalf("%s want %s got %s", property, expected, actual)
		}
	}
}

func TestFileCollectorOwnershipAssertionsRetainNumericIdentity(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			root := collectionRoot(t)
			fixtureFile(t, root, "data", "content")
			fixtureFile(t, root, "etc/passwd", fmt.Sprintf("fixture-user:x:%d:%d::/app:/bin/sh\n", os.Getuid(), os.Getgid()))
			fixtureFile(t, root, "etc/group", fmt.Sprintf("fixture-group:x:%d:\n", os.Getgid()))
			owner := "fixture-user"
			expected := "PASS"
			if wrong {
				owner = "other"
				expected = "FAIL"
			}
			a := collectionArtifact(t, "S02", map[string]any{"path": "/data", "owner": owner, "group": "fixture-group"})
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != expected {
				t.Fatalf("want %s got %+v", expected, results)
			}
			if fmt.Sprint(evidenceProperty(t, results, "uid")) != fmt.Sprint(os.Getuid()) || fmt.Sprint(evidenceProperty(t, results, "gid")) != fmt.Sprint(os.Getgid()) {
				t.Fatal("numeric identity lost")
			}
		})
	}
}

func TestFileCollectorDoesNotPassWhenOwnershipNamesCannotBeResolved(t *testing.T) {
	root := collectionRoot(t)
	fixtureFile(t, root, "data", "content")
	fixtureFile(t, root, "etc/passwd", "")
	fixtureFile(t, root, "etc/group", "")
	a := collectionArtifact(t, "S02", map[string]any{"path": "/data", "mode": "0644"})
	results := collectInSandbox(t, root, a)
	if len(results) != 1 || results[0].Status != "ERROR" {
		t.Fatalf("unresolved ownership must not pass: %+v", results)
	}
	if fmt.Sprint(evidenceProperty(t, results, "uid")) != fmt.Sprint(os.Getuid()) || fmt.Sprint(evidenceProperty(t, results, "gid")) != fmt.Sprint(os.Getgid()) {
		t.Fatal("numeric identity must remain available when names cannot be resolved")
	}
}

func TestFilesystemCollectorBoundariesUseNonprivilegedAvailability(t *testing.T) {
	for _, tt := range []struct {
		name, id, statistics string
		params               map[string]any
		status               string
	}{
		{"reserved_blocks_not_available", "S04", "4096\n100\n20\n10\n100\n50\next2/ext3", map[string]any{"path": "/data", "min_available": "40961 B"}, "FAIL"},
		{"reserved_blocks_exact_boundary", "S04", "4096\n100\n20\n10\n100\n50\next2/ext3", map[string]any{"path": "/data", "min_available": "40960 B"}, "PASS"},
		{"large_integer_exact", "S04", "1\n9007199254740994\n9007199254740993\n9007199254740993\n100\n50\nxfs", map[string]any{"path": "/data", "min_available": "9007199254740993 B"}, "PASS"},
		{"large_integer_below_boundary", "S04", "1\n9007199254740994\n9007199254740993\n9007199254740992\n100\n50\nxfs", map[string]any{"path": "/data", "min_available": "9007199254740993 B"}, "FAIL"},
		{"inode_ratio_exact", "S05", "4096\n100\n20\n10\n10\n5\nxfs", map[string]any{"path": "/data", "min_percent": float64(50)}, "PASS"},
		{"inode_ratio_below", "S05", "4096\n100\n20\n10\n10\n4\nxfs", map[string]any{"path": "/data", "min_percent": float64(50)}, "FAIL"},
		{"unknown_inode_total", "S05", "4096\n100\n20\n10\n0\n0\nxfs", map[string]any{"path": "/data", "min_available": float64(0)}, "ERROR"},
		{"invalid_available_blocks", "S04", "4096\n100\n20\n21\n10\n5\nxfs", map[string]any{"path": "/data", "min_available": "0 B"}, "ERROR"},
		{"invalid_block_size", "S04", "0\n100\n20\n10\n10\n5\nxfs", map[string]any{"path": "/data", "min_available": "0 B"}, "ERROR"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := collectionRoot(t)
			fixtureTool(t, root, "tools/stat", fixtureStatScript(tt.statistics))
			a := collectionArtifact(t, tt.id, tt.params)
			if strings.HasSuffix(tt.statistics, "\next2/ext3") {
				fixtureFile(t, root, "proc/self/mountinfo", "24 1 8:1 / /data rw - ext4 /dev/fixture rw\n")
			}
			results := collectInSandbox(t, root, a)
			if len(results) != 1 || results[0].Status != tt.status {
				t.Fatalf("want %s got %+v", tt.status, results)
			}
			if tt.status != "ERROR" {
				_ = evidenceProperty(t, results, "total_bytes")
				_ = evidenceProperty(t, results, "free_bytes")
				_ = evidenceProperty(t, results, "available_bytes")
				_ = evidenceProperty(t, results, "filesystem")
				_ = evidenceProperty(t, results, "mount_point")
				if strings.HasSuffix(tt.statistics, "\next2/ext3") && evidenceProperty(t, results, "filesystem") != "ext4" {
					t.Fatal("filesystem family must not replace exact mount type")
				}
			}
		})
	}
}

func TestMountCollectorRetainsBindMountSource(t *testing.T) {
	root := collectionRoot(t)
	if err := os.Mkdir(filepath.Join(root, "bound"), 0755); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, root, "proc/self/mountinfo", "24 1 8:1 / / rw - ext4 /dev/fixture rw\n25 24 8:1 /source/subdir /bound ro,noexec - ext4 /dev/fixture rw\n")
	a := collectionArtifact(t, "S03", map[string]any{"path": "/bound", "filesystem": "ext4", "required_options": []any{"ro", "noexec"}})
	results := collectInSandbox(t, root, a)
	if len(results) != 1 || results[0].Status != "PASS" {
		t.Fatalf("unexpected %+v", results)
	}
	if actual := evidenceProperty(t, results, "source"); actual != "/dev/fixture" {
		t.Fatalf("wrong bind source %v", actual)
	}
}

func TestFileCollectorMetadataDoesNotAddOrMergeBusinessAssertions(t *testing.T) {
	root := collectionRoot(t)
	fixtureFile(t, root, "data", "content")
	fixtureFile(t, root, "etc/passwd", fmt.Sprintf("fixture-user:x:%d:%d::/app:/bin/sh\n", os.Getuid(), os.Getgid()))
	fixtureFile(t, root, "etc/group", fmt.Sprintf("fixture-group:x:%d:\n", os.Getgid()))
	p := catalogPlan(t, "S02")
	owner := p.Roles[0].Items[0]
	owner.ItemID = "owner"
	owner.Params = map[string]any{"path": "/data", "owner": "different-owner"}
	permissions := owner
	permissions.ItemID = "permissions"
	permissions.Params = map[string]any{"path": "/data", "mode": "0644"}
	var digest string
	for _, reverse := range []bool{false, true} {
		p.Roles[0].Items = []plan.CheckItem{owner, permissions}
		if reverse {
			p.Roles[0].Items = []plan.CheckItem{permissions, owner}
		}
		a := selectedArtifact(t, p, compiler.HostInput{Roles: []string{"app"}})
		if digest != "" && digest != a.Digest {
			t.Fatal("capture/assertion merge depends on item order")
		}
		digest = a.Digest
		results := collectInSandbox(t, root, a)
		for _, result := range results {
			expected := "PASS"
			if result.ItemID == "owner" {
				expected = "FAIL"
			}
			if result.Status != expected {
				t.Fatalf("expected %s got %+v", expected, result)
			}
		}
	}
	conflicting := owner
	conflicting.ItemID = "conflicting"
	conflicting.Params = map[string]any{"path": "/data", "owner": "another-owner"}
	p.Roles[0].Items = append(p.Roles[0].Items, conflicting)
	if _, err := compiler.CompileExecutionModes(p, compiler.HostInput{Roles: []string{"app"}}); err == nil || !strings.Contains(err.Error(), "RESOURCE_CONFLICT") {
		t.Fatalf("different explicit ownership assertions were merged: %v", err)
	}
}

func sandboxWithBind(root, source, target string, command ...string) *exec.Cmd {
	cmd := sandboxWithMountInfo(root, command...)
	for i, arg := range cmd.Args {
		if arg == "--" {
			args := append([]string{}, cmd.Args[:i]...)
			args = append(args, "--ro-bind", source, target)
			cmd.Args = append(args, cmd.Args[i:]...)
			return cmd
		}
	}
	panic("sandbox argument separator missing")
}

func TestStorageCollectorsObserveActualBindMount(t *testing.T) {
	for _, id := range []string{"S03", "S04", "S05"} {
		t.Run(id, func(t *testing.T) {
			root := collectionRoot(t)
			for _, path := range []string{"source/child", "bound"} {
				if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
					t.Fatal(err)
				}
			}
			params := map[string]any{"path": "/bound", "required_options": []any{"ro"}}
			if id == "S04" {
				params = map[string]any{"path": "/bound/child", "min_available": "0 B"}
			}
			if id == "S05" {
				params = map[string]any{"path": "/bound/child", "min_available": float64(0)}
			}
			a := collectionArtifact(t, id, params)
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := sandboxWithBind(root, filepath.Join(root, "source"), "/bound", "/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
			if err != nil && len(out) == 0 {
				t.Fatal(err)
			}
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != "PASS" {
				t.Fatalf("unexpected %+v %v", results, err)
			}
			if id == "S03" {
				if source := fmt.Sprint(evidenceProperty(t, results, "source")); source == "" {
					t.Fatal("bind source missing")
				}
				_ = evidenceProperty(t, results, "filesystem")
			} else if mount := evidenceProperty(t, results, "mount_point"); mount != "/bound" {
				t.Fatalf("bind target expected /bound got %v", mount)
			}
		})
	}
}

// Use the real namespace's mountinfo for stat/statvfs-based filesystem probes.
func sandboxWithMountInfo(root string, command ...string) *exec.Cmd {
	cmd := sandboxCommand(root, command...)
	for i, arg := range cmd.Args {
		if arg == "--" {
			args := append([]string{}, cmd.Args[:i]...)
			args = append(args, "--proc", "/proc")
			cmd.Args = append(args, cmd.Args[i:]...)
			return cmd
		}
	}
	panic("sandbox argument separator missing")
}

func TestFilesystemCollectorsRetainRealReadonlyStatistics(t *testing.T) {
	for _, id := range []string{"S04", "S05"} {
		t.Run(id, func(t *testing.T) {
			root := collectionRoot(t)
			data := filepath.Join(root, "data")
			if err := os.Mkdir(data, 0755); err != nil {
				t.Fatal(err)
			}
			params := map[string]any{"path": "/data", "min_available": float64(0)}
			if id == "S04" {
				params = map[string]any{"path": "/data", "min_available": "0 B"}
			}
			a := collectionArtifact(t, id, params)
			if err := os.WriteFile(filepath.Join(root, "goss.json"), a.Config, 0600); err != nil {
				t.Fatal(err)
			}
			var expected syscall.Statfs_t
			if err := syscall.Statfs(data, &expected); err != nil {
				t.Fatal(err)
			}
			out, err := sandboxWithMountInfo(root, "/goss", "-g", "/goss.json", "validate", "--format", "json").Output()
			if err != nil && len(out) == 0 {
				t.Fatal(err)
			}
			results, err := compiler.Normalize(a, out)
			if err != nil || len(results) != 1 || results[0].Status != "PASS" {
				t.Fatalf("unexpected %+v %v", results, err)
			}
			if mount := fmt.Sprint(evidenceProperty(t, results, "mount_point")); !strings.HasPrefix(mount, "/") {
				t.Fatalf("invalid mount point %s", mount)
			}
			if filesystem := fmt.Sprint(evidenceProperty(t, results, "filesystem")); filesystem == "" {
				t.Fatal("filesystem type missing")
			}
			if id == "S04" {
				total := new(big.Int).Mul(new(big.Int).SetUint64(expected.Blocks), big.NewInt(expected.Bsize))
				if fmt.Sprint(evidenceProperty(t, results, "total_bytes")) != total.String() {
					t.Fatal("total bytes differ from independent statfs")
				}
				available, ok := new(big.Int).SetString(fmt.Sprint(evidenceProperty(t, results, "available_bytes")), 10)
				if !ok || available.Sign() < 0 || available.Cmp(total) > 0 {
					t.Fatal("invalid nonprivileged available bytes")
				}
			} else {
				if fmt.Sprint(evidenceProperty(t, results, "total_inodes")) != fmt.Sprint(expected.Files) {
					t.Fatal("total inodes differ from independent statfs")
				}
				_ = evidenceProperty(t, results, "available_inodes")
				_ = evidenceProperty(t, results, "free_percent")
			}
			entries, err := os.ReadDir(data)
			if err != nil || len(entries) != 0 {
				t.Fatal("readonly statistics wrote files into target directory")
			}
		})
	}
}
