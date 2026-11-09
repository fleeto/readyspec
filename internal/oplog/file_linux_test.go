//go:build linux

package oplog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestOpenFileEnforcesPrivateOwnerAndSingleWriter(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "service.log")
	first, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("log mode=%o, want 600", info.Mode().Perm())
	}
	second, err := OpenFile(path)
	if err == nil {
		second.Close()
		t.Fatal("second writer unexpectedly acquired the log")
	}
}

func TestOpenFileRejectsUnsafePathsAndExistingTargets(t *testing.T) {
	t.Run("symlink target", func(t *testing.T) {
		dir := privateTempDir(t)
		target := filepath.Join(dir, "target")
		if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "service.log")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenFile(link); err == nil {
			t.Fatal("symlink log target was accepted")
		}
		content, err := os.ReadFile(target)
		if err != nil || string(content) != "keep" {
			t.Fatalf("target changed: %q err=%v", content, err)
		}
	})
	t.Run("unsafe directory", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := OpenFile(filepath.Join(dir, "service.log")); err == nil {
			t.Fatal("group/world accessible directory was accepted")
		}
	})
	t.Run("symlink directory component", func(t *testing.T) {
		base := privateTempDir(t)
		real := filepath.Join(base, "real")
		if err := os.Mkdir(real, 0700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(base, "linked")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenFile(filepath.Join(link, "service.log")); err == nil {
			t.Fatal("symlinked directory component was accepted")
		}
	})
	t.Run("hard link", func(t *testing.T) {
		dir := privateTempDir(t)
		path := filepath.Join(dir, "service.log")
		if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, filepath.Join(dir, "alias")); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenFile(path); err == nil {
			t.Fatal("multiply-linked file was accepted")
		}
	})
}

func TestRotatingFileCapsBackupsAndRejectsUnsafeBackup(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, "service.log")
	file, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	chunk := []byte(strings.Repeat("x", maxRecord-1) + "\n")
	for i := 0; i < (maxFileBytes/maxRecord)*4+20; i++ {
		if _, err := file.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1", path + ".2", path + ".3"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("missing rotation member %s: %v", name, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode=%o", name, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".4"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected fourth backup: %v", err)
	}
	if err := os.Remove(path + ".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "elsewhere"), path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxFileBytes); err != nil {
		t.Fatal(err)
	}
	file, err = OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(chunk); err == nil {
		t.Fatal("rotation moved a symlink backup")
	}
}
