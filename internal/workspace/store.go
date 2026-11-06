// Package workspace provides confined persistence and an OS-owned workspace lease.
package workspace

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
)

var (
	ErrBusy                 = errors.New("WORKSPACE_BUSY")
	ErrNotLocked            = errors.New("workspace write requires an exclusive lease")
	ErrJournalNeedsRecovery = errors.New("journal must be recovered before appending")
	ErrJournalCorrupt       = errors.New("journal contains a corrupt complete record")
)

type Store struct {
	mu               sync.RWMutex
	root             *os.Root
	lease            *Lease
	closed           bool
	journalRecovered map[string]bool
	journalCorrupt   map[string]bool
	syncFile         func(*os.File) error
	syncDirectory    func(*os.Root, string) error
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
	s := &Store{
		root: root, journalRecovered: map[string]bool{}, journalCorrupt: map[string]bool{},
		syncFile:      func(file *os.File) error { return file.Sync() },
		syncDirectory: syncDir,
	}
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

// Remove removes one confined workspace entry while the caller holds the lease.
func (s *Store) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fs.ErrClosed
	}
	if s.lease == nil {
		return ErrNotLocked
	}
	if err := validWorkspacePath(name); err != nil {
		return err
	}
	if err := s.root.Remove(name); err != nil {
		return err
	}
	return syncDir(s.root, path.Dir(name))
}

// ReadDir returns immediate entries beneath the workspace root.
func (s *Store) ReadDir(name string) ([]fs.DirEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fs.ErrClosed
	}
	if name != "." {
		if err := validWorkspacePath(name); err != nil {
			return nil, err
		}
	}
	dir, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

// AppendJSONLine appends one JSON record and syncs it before returning. An
// existing journal must first pass RecoverJSONLines on this Store instance.
func (s *Store) AppendJSONLine(name string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fs.ErrClosed
	}
	if s.lease == nil {
		return ErrNotLocked
	}
	if err := validWorkspacePath(name); err != nil {
		return err
	}
	if s.journalCorrupt[name] {
		return ErrJournalCorrupt
	}
	if info, err := s.root.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("journal must be a regular file")
		}
		if !s.journalRecovered[name] {
			return ErrJournalNeedsRecovery
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	dir := path.Dir(name)
	if err := s.root.MkdirAll(dir, 0700); err != nil {
		return err
	}
	_, statErr := s.root.Lstat(name)
	created := errors.Is(statErr, fs.ErrNotExist)
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	line := append(raw, '\n')
	for len(line) > 0 {
		n, writeErr := f.Write(line)
		line = line[n:]
		if writeErr != nil {
			err = writeErr
			break
		}
		if n == 0 {
			err = io.ErrShortWrite
			break
		}
	}
	if err == nil {
		err = s.syncFile(f)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil && created {
		err = s.syncDirectory(s.root, dir)
	}
	if err != nil {
		s.journalRecovered[name] = false
		return err
	}
	s.journalRecovered[name] = true
	return nil
}

// RecoverJSONLines validates complete records and discards only an incomplete
// unterminated tail. Corrupt newline-terminated records poison the journal.
func (s *Store) RecoverJSONLines(name string) ([][]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false, fs.ErrClosed
	}
	if s.lease == nil {
		return nil, false, ErrNotLocked
	}
	if err := validWorkspacePath(name); err != nil {
		return nil, false, err
	}
	if s.journalCorrupt[name] {
		return nil, false, ErrJournalCorrupt
	}
	raw, err := s.root.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		s.journalRecovered[name] = true
		return [][]byte{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 {
		s.journalRecovered[name] = true
		return [][]byte{}, false, nil
	}
	validEnd := len(raw)
	truncated := false
	if validEnd > 0 && raw[validEnd-1] != '\n' {
		lastNewline := bytes.LastIndexByte(raw, '\n')
		validEnd = lastNewline + 1
		truncated = true
	}
	prefix := raw[:validEnd]
	lines := bytes.Split(prefix, []byte{'\n'})
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	records := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if len(line) == 0 || !json.Valid(line) {
			s.journalCorrupt[name] = true
			return nil, false, ErrJournalCorrupt
		}
		records = append(records, append([]byte(nil), line...))
	}
	if truncated {
		f, err := s.root.OpenFile(name, os.O_WRONLY, 0)
		if err != nil {
			return nil, false, err
		}
		if err := f.Truncate(int64(validEnd)); err != nil {
			_ = f.Close()
			return nil, false, err
		}
		if err := errors.Join(s.syncFile(f), f.Close()); err != nil {
			return nil, false, err
		}
	}
	s.journalRecovered[name] = true
	return records, truncated, nil
}

func validWorkspacePath(name string) error {
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return fmt.Errorf("invalid workspace path")
	}
	return nil
}
