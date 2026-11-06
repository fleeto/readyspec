package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestJournalSyncCrashSubprocessHelper(t *testing.T) {
	dir := os.Getenv("READYSPEC_JOURNAL_CRASH_ROOT")
	if dir == "" {
		return
	}
	action := os.Getenv("READYSPEC_JOURNAL_CRASH_ACTION")
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	const journal = "runs/demo/events.ndjson"
	switch action {
	case "file_sync":
		if _, _, err := store.RecoverJSONLines(journal); err != nil {
			t.Fatal(err)
		}
		store.syncFile = func(file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			os.Exit(0)
			return nil
		}
		_ = store.AppendJSONLine(journal, map[string]int{"seq": 1})
	case "directory_sync":
		if _, _, err := store.RecoverJSONLines(journal); err != nil {
			t.Fatal(err)
		}
		store.syncDirectory = func(root *os.Root, dir string) error {
			if err := syncDir(root, dir); err != nil {
				return err
			}
			os.Exit(0)
			return nil
		}
		_ = store.AppendJSONLine(journal, map[string]int{"seq": 1})
	case "tail_recovery_sync":
		if err := store.root.MkdirAll("runs/demo", 0700); err != nil {
			t.Fatal(err)
		}
		if err := store.root.WriteFile(journal, []byte("{\"seq\":1}\n{\"seq\":"), 0600); err != nil {
			t.Fatal(err)
		}
		store.syncFile = func(file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			os.Exit(0)
			return nil
		}
		_, _, _ = store.RecoverJSONLines(journal)
	default:
		t.Fatalf("unknown journal crash action %q", action)
	}
	_ = lease
	t.Fatal("crash action returned without terminating subprocess")
}

func TestJournalRecoveryAcrossSubprocessSyncCrashes(t *testing.T) {
	for _, action := range []string{"file_sync", "directory_sync", "tail_recovery_sync"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestJournalSyncCrashSubprocessHelper$")
			cmd.Env = append(os.Environ(), "READYSPEC_JOURNAL_CRASH_ROOT="+dir, "READYSPEC_JOURNAL_CRASH_ACTION="+action)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("crash helper failed: %v\n%s", err, output)
			}
			store, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			lease, err := store.TryLock()
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			lines, truncated, err := store.RecoverJSONLines("runs/demo/events.ndjson")
			if err != nil || truncated || len(lines) != 1 {
				t.Fatalf("recovered records=%d truncated=%v err=%v", len(lines), truncated, err)
			}
			if action != "tail_recovery_sync" {
				if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err != nil {
					t.Fatalf("append after crash recovery: %v", err)
				}
			}
		})
	}
}

func TestJournalFileSyncFailureRequiresRecoveryBeforeAppend(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	if _, _, err := store.RecoverJSONLines("runs/demo/events.ndjson"); err != nil {
		t.Fatal(err)
	}
	syncFailure := errors.New("injected file sync failure")
	store.syncFile = func(*os.File) error { return syncFailure }
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 1}); !errors.Is(err, syncFailure) {
		t.Fatalf("AppendJSONLine() error = %v, want injected sync failure", err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); !errors.Is(err, ErrJournalNeedsRecovery) {
		t.Fatalf("append after failed sync error = %v, want recovery required", err)
	}

	store.syncFile = func(file *os.File) error { return file.Sync() }
	lines, truncated, err := store.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil || truncated || len(lines) != 1 {
		t.Fatalf("recovery after sync failure: records=%d truncated=%v err=%v", len(lines), truncated, err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err != nil {
		t.Fatalf("append after successful recovery: %v", err)
	}
}

func TestJournalDirectorySyncFailureRequiresRecoveryBeforeAppend(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	if _, _, err := store.RecoverJSONLines("runs/demo/events.ndjson"); err != nil {
		t.Fatal(err)
	}
	syncFailure := errors.New("injected directory sync failure")
	store.syncDirectory = func(*os.Root, string) error { return syncFailure }
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 1}); !errors.Is(err, syncFailure) {
		t.Fatalf("AppendJSONLine() error = %v, want injected directory sync failure", err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); !errors.Is(err, ErrJournalNeedsRecovery) {
		t.Fatalf("append after failed directory sync error = %v, want recovery required", err)
	}

	store.syncDirectory = syncDir
	lines, truncated, err := store.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil || truncated || len(lines) != 1 {
		t.Fatalf("recovery after directory sync failure: records=%d truncated=%v err=%v", len(lines), truncated, err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err != nil {
		t.Fatalf("append after successful recovery: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.root.Name(), "runs", "demo", "events.ndjson")); err != nil {
		t.Fatal(err)
	}
}

func TestJournalTailRecoverySyncFailureKeepsAppendBlocked(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	lease, err := store.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	if err := store.root.MkdirAll("runs/demo", 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.root.WriteFile("runs/demo/events.ndjson", []byte("{\"seq\":1}\n{\"seq\":"), 0600); err != nil {
		t.Fatal(err)
	}
	syncFailure := errors.New("injected recovery sync failure")
	store.syncFile = func(*os.File) error { return syncFailure }
	if _, _, err := store.RecoverJSONLines("runs/demo/events.ndjson"); !errors.Is(err, syncFailure) {
		t.Fatalf("RecoverJSONLines() error = %v, want injected sync failure", err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); !errors.Is(err, ErrJournalNeedsRecovery) {
		t.Fatalf("append after failed recovery sync error = %v, want recovery required", err)
	}

	store.syncFile = func(file *os.File) error { return file.Sync() }
	lines, truncated, err := store.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil || truncated || len(lines) != 1 {
		t.Fatalf("retry tail recovery: records=%d truncated=%v err=%v", len(lines), truncated, err)
	}
	if err := store.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err != nil {
		t.Fatalf("append after successful recovery retry: %v", err)
	}
}
