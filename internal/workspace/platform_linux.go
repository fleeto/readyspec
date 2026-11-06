package workspace

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func createRoot(dir string) error { return os.MkdirAll(dir, 0700) }

func secureRoot(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		return err
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("workspace must be owned by the current user")
	}
	return f.Chmod(0700)
}

func lockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrBusy
	}
	return err
}

func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

func replace(root *os.Root, old, name string) error { return root.Rename(old, name) }

func syncDir(root *os.Root, dir string) error {
	f, err := root.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
