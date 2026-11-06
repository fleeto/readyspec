package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fleeto/readyspec/internal/workspace"
)

func TestWorkspacePermissionsAndSymlinkConfinement(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "workspace")
	s, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := s.WriteJSON("runs/example/report.json", map[string]string{"verdict": "UNKNOWN"}); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{".": 0700, "runtime": 0700, "runtime/.lock": 0600, "runs/example": 0700, "runs/example/report.json": 0600} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("%s: mode %o, want %o", name, info.Mode().Perm(), mode)
		}
	}
	if err := os.Symlink(dir, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteJSON("escape/outside.json", map[string]bool{"escape": true}); err == nil {
		t.Fatal("followed a symlink outside workspace")
	}
	if _, err := workspace.Open(filepath.Join(root, "escape")); err == nil {
		t.Fatal("accepted a symbolic link as workspace root")
	}
}
