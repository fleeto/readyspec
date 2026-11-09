//go:build linux

package oplog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	maxFileBytes = 10 << 20
	maxRecord    = 4 << 10
	backups      = 3
)

// RotatingFile is a single-writer, private Linux log destination. The parent
// directory must already be owned by the current user and have mode 0700.
type RotatingFile struct {
	mu       sync.Mutex
	dirFD    int
	name     string
	file     *os.File
	lockFile *os.File
	closed   bool
}

func OpenFile(path string) (*RotatingFile, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir, name := filepath.Split(abs)
	name = strings.TrimSuffix(name, string(filepath.Separator))
	if name == "" || name == "." || name == ".." {
		return nil, fmt.Errorf("LOG_FILE_INVALID")
	}
	dirFD, err := openPrivateDirectory(filepath.Clean(dir))
	if err != nil {
		return nil, err
	}
	result := &RotatingFile{dirFD: dirFD, name: name}
	lock, err := openCheckedAt(dirFD, name+".lock", true)
	if err != nil {
		unix.Close(dirFD)
		return nil, err
	}
	result.lockFile = lock
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		unix.Close(dirFD)
		return nil, fmt.Errorf("LOG_FILE_IN_USE")
	}
	file, err := openCheckedAt(dirFD, name, true)
	if err != nil {
		result.Close()
		return nil, err
	}
	result.file = file
	return result, nil
}

func openPrivateDirectory(path string) (int, error) {
	root, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			unix.Close(root)
			return -1, fmt.Errorf("LOG_DIRECTORY_INVALID")
		}
		next, openErr := unix.Openat(root, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(root)
		if openErr != nil {
			return -1, fmt.Errorf("LOG_DIRECTORY_UNSAFE")
		}
		root = next
	}
	var st unix.Stat_t
	if err := unix.Fstat(root, &st); err != nil {
		unix.Close(root)
		return -1, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0077 != 0 {
		unix.Close(root)
		return -1, fmt.Errorf("LOG_DIRECTORY_UNSAFE")
	}
	return root, nil
}

func openCheckedAt(dirFD int, name string, create bool) (*os.File, error) {
	flags := unix.O_WRONLY | unix.O_APPEND | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if create {
		flags |= unix.O_CREAT
	}
	fd, err := unix.Openat(dirFD, name, flags, 0600)
	if err != nil {
		return nil, fmt.Errorf("LOG_FILE_UNSAFE")
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		unix.Close(fd)
		return nil, fmt.Errorf("LOG_FILE_UNSAFE")
	}
	if err := unix.Fchmod(fd, 0600); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
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
		name := fmt.Sprintf("%s.%d", f.name, i)
		if err := validateExistingAt(f.dirFD, name); err != nil {
			return err
		}
	}
	if err := f.file.Close(); err != nil {
		return err
	}
	for i := backups; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", f.name, i)
		var stat unix.Stat_t
		if err := unix.Fstatat(f.dirFD, old, &stat, unix.AT_SYMLINK_NOFOLLOW); err == nil {
			if i == backups {
				if err := removeCheckedAt(f.dirFD, old); err != nil {
					return err
				}
			} else if err := unix.Renameat(f.dirFD, old, f.dirFD, fmt.Sprintf("%s.%d", f.name, i+1)); err != nil {
				return err
			}
		} else if !errors.Is(err, unix.ENOENT) {
			return fmt.Errorf("LOG_ROTATION_FAILED")
		}
	}
	if err := unix.Renameat(f.dirFD, f.name, f.dirFD, f.name+".1"); err != nil {
		return fmt.Errorf("LOG_ROTATION_FAILED")
	}
	if err := unix.Fsync(f.dirFD); err != nil {
		return err
	}
	file, err := openCheckedAt(f.dirFD, f.name, true)
	if err != nil {
		return err
	}
	f.file = file
	return nil
}

func removeCheckedAt(dirFD int, name string) error {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("LOG_ROTATION_FAILED")
	}
	var st unix.Stat_t
	statErr := unix.Fstat(fd, &st)
	unix.Close(fd)
	if statErr != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0077 != 0 || st.Size > maxFileBytes {
		return fmt.Errorf("LOG_ROTATION_UNSAFE")
	}
	return unix.Unlinkat(dirFD, name, 0)
}

func validateExistingAt(dirFD int, name string) error {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("LOG_ROTATION_UNSAFE")
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0077 != 0 || st.Size > maxFileBytes {
		return fmt.Errorf("LOG_ROTATION_UNSAFE")
	}
	return nil
}

func (f *RotatingFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	var first error
	if f.file != nil {
		first = f.file.Sync()
		if err := f.file.Close(); first == nil {
			first = err
		}
	}
	if f.lockFile != nil {
		_ = unix.Flock(int(f.lockFile.Fd()), unix.LOCK_UN)
		if err := f.lockFile.Close(); first == nil {
			first = err
		}
	}
	if err := unix.Close(f.dirFD); first == nil {
		first = err
	}
	return first
}

var _ io.WriteCloser = (*RotatingFile)(nil)
