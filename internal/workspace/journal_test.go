package workspace_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fleeto/readyspec/internal/workspace"
)

func TestJournalAppendRequiresLeaseAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	s, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 1}); !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("append without lease: %v", err)
	}
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for seq := 1; seq <= 2; seq++ {
		if err := s.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": seq}); err != nil {
			t.Fatal(err)
		}
	}
	lines, truncated, err := s.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(lines) != 2 {
		t.Fatalf("recovered %d lines, truncated=%v", len(lines), truncated)
	}
	for i, line := range lines {
		var record map[string]int
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["seq"] != i+1 {
			t.Fatalf("line %d sequence = %d", i, record["seq"])
		}
	}
}

func TestJournalRecoveryTruncatesOnlyAnIncompleteTail(t *testing.T) {
	dir := t.TempDir()
	s, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	name := filepath.Join(dir, "runs", "demo", "events.ndjson")
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	validPrefix := []byte("{\"seq\":1}\n")
	if err := os.WriteFile(name, append(append([]byte{}, validPrefix...), []byte("{\"seq\":")...), 0600); err != nil {
		t.Fatal(err)
	}
	lines, truncated, err := s.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(lines) != 1 {
		t.Fatalf("incomplete tail recovery: %d lines, truncated=%v", len(lines), truncated)
	}
	recovered, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(recovered) != string(validPrefix) {
		t.Fatalf("recovery left unexpected bytes: %q", recovered)
	}
	if err := s.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err != nil {
		t.Fatal(err)
	}
	lines, truncated, err = s.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil || truncated || len(lines) != 2 {
		t.Fatalf("append after recovery: %d lines, truncated=%v, err=%v", len(lines), truncated, err)
	}
}

func TestJournalRecoveryRejectsCorruptCompleteRecord(t *testing.T) {
	dir := t.TempDir()
	s, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	name := filepath.Join(dir, "runs", "demo", "events.ndjson")
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("{\"seq\":1}\nnot-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecoverJSONLines("runs/demo/events.ndjson"); err == nil {
		t.Fatal("accepted a corrupt complete record")
	}
	if err := s.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": 2}); err == nil {
		t.Fatal("appended after journal corruption")
	}
}

func TestJournalConcurrentAppendsRemainComplete(t *testing.T) {
	dir := t.TempDir()
	s, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	const count = 64
	var wg sync.WaitGroup
	errCh := make(chan error, count)
	for seq := 1; seq <= count; seq++ {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			errCh <- s.AppendJSONLine("runs/demo/events.ndjson", map[string]int{"seq": seq})
		}(seq)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	lines, truncated, err := s.RecoverJSONLines("runs/demo/events.ndjson")
	if err != nil || truncated || len(lines) != count {
		t.Fatalf("recovered %d lines, truncated=%v, err=%v", len(lines), truncated, err)
	}
	seen := make(map[int]bool, count)
	for _, line := range lines {
		var event struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		if seen[event.Seq] {
			t.Fatalf("duplicate sequence %d", event.Seq)
		}
		seen[event.Seq] = true
	}
}

func TestJournalRejectsWorkspaceEscape(t *testing.T) {
	s, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, name := range []string{"../outside.ndjson", `runs\outside.ndjson`} {
		if err := s.AppendJSONLine(name, map[string]int{"seq": 1}); err == nil {
			t.Fatalf("accepted escaping path %q", name)
		}
		if _, _, err := s.RecoverJSONLines(name); err == nil {
			t.Fatalf("recovered escaping path %q", name)
		}
	}
}
