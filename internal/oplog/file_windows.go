//go:build windows

package oplog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	maxFileBytes = 10 << 20
	maxRecord    = 4 << 10
	backups      = 3
)

type RotatingFile struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	lock   *os.File
	closed bool
}

func OpenFile(path string) (*RotatingFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := checkWindowsDirectory(filepath.Dir(abs)); err != nil {
		return nil, err
	}
	lock, err := openWindowsFile(abs+".lock", true)
	if err != nil {
		return nil, err
	}
	if err := windows.LockFileEx(windows.Handle(lock.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		lock.Close()
		return nil, fmt.Errorf("LOG_FILE_IN_USE")
	}
	file, err := openWindowsFile(abs, true)
	if err != nil {
		_ = windows.UnlockFileEx(windows.Handle(lock.Fd()), 0, 1, 0, &windows.Overlapped{})
		lock.Close()
		return nil, err
	}
	return &RotatingFile{path: abs, file: file, lock: lock}, nil
}

func checkWindowsDirectory(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(filepath.Separator)
	rel := strings.TrimPrefix(strings.TrimPrefix(abs, volume), string(filepath.Separator))
	if rel == "" {
		return fmt.Errorf("LOG_DIRECTORY_UNSAFE")
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for index, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		name, _ := windows.UTF16PtrFromString(current)
		h, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			return fmt.Errorf("LOG_DIRECTORY_UNSAFE")
		}
		info, infoErr := fileInfo(h)
		_ = windows.CloseHandle(h)
		if infoErr != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("LOG_DIRECTORY_UNSAFE")
		}
		if index == len(parts)-1 {
			checkHandle, openErr := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
				windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
				windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if openErr != nil {
				return fmt.Errorf("LOG_DIRECTORY_UNSAFE")
			}
			securityErr := verifyPrivateACL(checkHandle)
			_ = windows.CloseHandle(checkHandle)
			if securityErr != nil {
				return fmt.Errorf("LOG_DIRECTORY_UNSAFE")
			}
		}
	}
	return nil
}

func fileInfo(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(handle, &info)
	return info, err
}

func currentSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

func verifyPrivateACL(handle windows.Handle) error {
	security, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := security.Owner()
	if err != nil {
		return err
	}
	user, err := currentSID()
	if err != nil || !windows.EqualSid(owner, user) {
		return fmt.Errorf("LOG_FILE_UNSAFE")
	}
	control, _, err := security.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("LOG_FILE_UNSAFE")
	}
	acl, _, err := security.DACL()
	if err != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("LOG_FILE_UNSAFE")
	}
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("LOG_FILE_UNSAFE")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !windows.EqualSid(sid, user) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return fmt.Errorf("LOG_FILE_UNSAFE")
		}
	}
	return nil
}

func secureFileACL(handle windows.Handle) error {
	user, err := currentSID()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func openWindowsFile(path string, create bool) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("LOG_FILE_INVALID")
	}
	creation := uint32(windows.OPEN_EXISTING)
	if create {
		creation = windows.OPEN_ALWAYS
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, creation, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, fmt.Errorf("LOG_FILE_UNSAFE")
	}
	info, err := fileInfo(handle)
	if err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || info.NumberOfLinks != 1 {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("LOG_FILE_UNSAFE")
	}
	if err := verifyPrivateACL(handle); err != nil {
		// Newly created files inherit the protected directory's owner-only ACL;
		// verify ownership before installing the required private descriptor.
		security, secErr := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		var owner *windows.SID
		var ownerErr error
		if secErr == nil {
			owner, _, ownerErr = security.Owner()
		}
		user, userErr := currentSID()
		if secErr != nil || ownerErr != nil || userErr != nil || !windows.EqualSid(owner, user) {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("LOG_FILE_UNSAFE")
		}
		if err := secureFileACL(handle); err != nil {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("LOG_FILE_UNSAFE")
		}
	}
	file := os.NewFile(uintptr(handle), path)
	if _, err := file.Seek(0, 2); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("LOG_FILE_UNSAFE")
	}
	return file, nil
}

func (f *RotatingFile) Write(data []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, os.ErrClosed
	}
	if len(data) > maxRecord {
		return 0, fmt.Errorf("LOG_RECORD_TOO_LARGE")
	}
	info, err := f.file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size()+int64(len(data)) > maxFileBytes {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}
	return f.file.Write(data)
}

func (f *RotatingFile) rotate() error {
	if err := f.file.Sync(); err != nil {
		return err
	}
	for i := 1; i <= backups; i++ {
		if _, err := os.Lstat(fmt.Sprintf("%s.%d", f.path, i)); err == nil {
			file, openErr := openWindowsFile(fmt.Sprintf("%s.%d", f.path, i), false)
			if openErr != nil {
				return fmt.Errorf("LOG_ROTATION_UNSAFE")
			}
			info, statErr := file.Stat()
			_ = file.Close()
			if statErr != nil || info.Size() > maxFileBytes {
				return fmt.Errorf("LOG_ROTATION_UNSAFE")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("LOG_ROTATION_FAILED")
		}
	}
	if err := f.file.Close(); err != nil {
		return err
	}
	for i := backups; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", f.path, i)
		if _, err := os.Lstat(old); err == nil {
			if i == backups {
				if err := os.Remove(old); err != nil {
					return fmt.Errorf("LOG_ROTATION_FAILED")
				}
			} else if err := os.Rename(old, fmt.Sprintf("%s.%d", f.path, i+1)); err != nil {
				return fmt.Errorf("LOG_ROTATION_FAILED")
			}
		}
	}
	if err := os.Rename(f.path, f.path+".1"); err != nil {
		return fmt.Errorf("LOG_ROTATION_FAILED")
	}
	file, err := openWindowsFile(f.path, true)
	if err != nil {
		return err
	}
	f.file = file
	return nil
}

func (f *RotatingFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	var err error
	if f.file != nil {
		err = f.file.Sync()
		closeErr := f.file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if f.lock != nil {
		_ = windows.UnlockFileEx(windows.Handle(f.lock.Fd()), 0, 1, 0, &windows.Overlapped{})
		if closeErr := f.lock.Close(); err == nil {
			err = closeErr
		}
	}
	return err
}
