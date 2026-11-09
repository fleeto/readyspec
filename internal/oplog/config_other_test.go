//go:build !windows

package oplog

import (
	"os"
	"testing"
)

func privateLogTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
