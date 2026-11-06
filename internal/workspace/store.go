// Package workspace provides confined persistence and an OS-owned workspace lease.
package workspace

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
)

var (
	ErrBusy      = errors.New("WORKSPACE_BUSY")
	ErrNotLocked = errors.New("workspace write requires an exclusive lease")
)

type Store struct {
	mu     sync.RWMutex
	root   *os.Root
	lease  *Lease
	closed bool
}

type Lease struct {
	store *Store
	file  *os.File
}

func Open(dir string) (*Store, error) {
	if err := createRoot(dir); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("workspace root must not be a symbolic link")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root}
	if err := secureRoot(root); err != nil {
		root.Close()
		return nil, err
	}
	if err := root.MkdirAll("runtime", 0700); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.lease != nil {
		err = s.lease.closeLocked()
	}
	return errors.Join(err, s.root.Close())
}

// TryLock acquires the persistent lock inode. Never unlink the lock file:
// replacing it could let another process acquire a different inode's lock.
func (s *Store) TryLock() (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fs.ErrClosed
	}
	if s.lease != nil {
		return nil, ErrBusy
	}
	if info, err := s.root.Lstat("runtime/.lock"); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("workspace lock must be a regular file")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	f, err := s.root.OpenFile("runtime/.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	lease := &Lease{store: s, file: f}
	s.lease = lease
	return lease, nil
}

func (l *Lease) Close() error {
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	return l.closeLocked()
}

func (l *Lease) closeLocked() error {
	if l.file == nil {
		return nil
	}
	err := errors.Join(unlockFile(l.file), l.file.Close())
	l.file = nil
	l.store.lease = nil
	return err
}

// WriteJSON is atomic with respect to readers and requires this Store's lease.
// Paths use slash separators on every platform and must remain inside the root.
func (s *Store) WriteJSON(name string, value any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fs.ErrClosed
	}
	if s.lease == nil {
		return ErrNotLocked
	}
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid workspace path")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode workspace JSON: %w", err)
	}
	dir := path.Dir(name)
	if err := s.root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp := path.Join(dir, ".readyspec-"+rand.Text()+".tmp")
	f, err := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer s.root.Remove(temp)
	_, writeErr := f.Write(append(raw, '\n'))
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	if err := replace(s.root, temp, name); err != nil {
		return err
	}
	return syncDir(s.root, dir)
}

func (s *Store) ReadJSON(name string, target any) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return fs.ErrClosed
	}
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid workspace path")
	}
	raw, err := s.root.ReadFile(name)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
