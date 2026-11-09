//go:build windows

package oplog

import "testing"

func privateLogTestDir(t *testing.T) string {
	t.Helper()
	return privateWindowsLogDir(t)
}
