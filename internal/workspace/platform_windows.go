package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	// Protected DACL; only the owner, SYSTEM and Administrators inherit access.
	return windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
}

func createRoot(dir string) error {
	// Apply the private DACL at creation, before runtime data can be written.
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return err
	}
	sd, err := privateDescriptor()
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	err = windows.CreateDirectory(name, &sa)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	return err
}

func secureRoot(root *os.Root) error {
	sd, err := privateDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	// Open a handle with WRITE_DAC; ordinary os.Open directory handles do not
	// grant the rights required to replace an existing DACL.
	name, err := windows.UTF16PtrFromString(root.Name())
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(handle), root.Name())
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	opened, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("workspace identity changed during permission setup")
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func lockFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}

func replace(root *os.Root, old, name string) error {
	// os.Root.Rename uses Windows rename semantics after the source handle has
	// closed; both names are resolved beneath the same open root.
	return root.Rename(old, name)
}

// File.Sync calls FlushFileBuffers before replacement. Windows does not expose
// portable directory fsync through os.File; durability needs native validation.
func syncDir(_ *os.Root, _ string) error { return nil }
