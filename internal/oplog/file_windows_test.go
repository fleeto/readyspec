//go:build windows

package oplog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsLogFileUsesPrivateACLAndExclusiveWriter(t *testing.T) {
	dir, err := os.MkdirTemp("", "readyspec-log-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureFileACL(handle); err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	if err := setTestDirectoryOwner(handle); err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	if err := verifyPrivateACL(handle); err != nil {
		windows.CloseHandle(handle)
		t.Fatalf("private test directory ACL did not verify: %v", err)
	}
	_ = windows.CloseHandle(handle)
	path := filepath.Join(dir, "service.log")
	first, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("private record\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(path); err == nil {
		t.Fatal("second writer acquired the log file")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("log file was not written: info=%v err=%v", info, err)
	}
}

func TestWindowsLogFileRejectsDirectoryACLForOtherUsers(t *testing.T) {
	dir, err := os.MkdirTemp("", "readyspec-log-acl-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	unsafeSD, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	unsafeDACL, _, err := unsafeSD.DACL()
	if err == nil {
		err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, unsafeDACL, nil)
	}
	_ = windows.CloseHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFile(filepath.Join(dir, "service.log")); err == nil {
		t.Fatal("world-accessible log directory was accepted")
	}
}

func TestWindowsLogFileRejectsReparsePoint(t *testing.T) {
	dir := privateWindowsLogDir(t)
	target := filepath.Join(dir, "target.log")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "service.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable on this Windows host: %v", err)
	}
	if _, err := OpenFile(link); err == nil {
		t.Fatal("reparse-point log path was accepted")
	}
	contents, err := os.ReadFile(target)
	if err != nil || string(contents) != "keep" {
		t.Fatalf("target changed: %q err=%v", contents, err)
	}
}

func TestWindowsRotatingFileKeepsThreeBackups(t *testing.T) {
	dir := privateWindowsLogDir(t)
	path := filepath.Join(dir, "service.log")
	file, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, maxRecord)
	for cycle := 0; cycle < backups+1; cycle++ {
		for written := int64(0); written <= maxFileBytes; written += int64(len(chunk)) {
			if _, err := file.Write(chunk); err != nil {
				file.Close()
				t.Fatalf("cycle %d write: %v", cycle, err)
			}
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= backups; index++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, index)); err != nil {
			t.Fatalf("backup %d missing: %v", index, err)
		}
	}
	if _, err := os.Stat(fmt.Sprintf("%s.%d", path, backups+1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected fourth backup: %v", err)
	}
}

func privateWindowsLogDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "readyspec-log-private-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.WRITE_OWNER|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureFileACL(handle); err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	if err := setTestDirectoryOwner(handle); err != nil {
		windows.CloseHandle(handle)
		t.Fatal(err)
	}
	if err := verifyPrivateACL(handle); err != nil {
		windows.CloseHandle(handle)
		t.Fatalf("private test directory ACL did not verify: %v", err)
	}
	_ = windows.CloseHandle(handle)
	return dir
}

func setTestDirectoryOwner(handle windows.Handle) error {
	user, err := currentSID()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user, nil, nil, nil)
}
