package workspace_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fleeto/readyspec/internal/workspace"
)

func TestExclusiveLeaseAndReacquisition(t *testing.T) {
	dir := t.TempDir()
	a, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	lease, err := a.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := b.TryLock(); !errors.Is(err, workspace.ErrBusy) {
		t.Fatalf("expected busy workspace, got %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := b.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
}

func TestWorkspaceLockSurvivesProcessCrash(t *testing.T) {
	if dir := os.Getenv("READYSPEC_TEST_LOCK_ROOT"); dir != "" {
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
		fmt.Println("locked")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestWorkspaceLockSurvivesProcessCrash$")
	cmd.Env = append(os.Environ(), "READYSPEC_TEST_LOCK_ROOT="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("helper did not acquire lock: %q, %v", line, err)
	}
	s, err := workspace.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.TryLock(); !errors.Is(err, workspace.ErrBusy) {
		t.Fatalf("cross-process exclusion failed: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatalf("OS did not release lock after process death: %v", err)
	}
	defer lease.Close()
}

func TestAtomicJSONPreservesPreviousValueOnEncodingFailure(t *testing.T) {
	s, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteJSON("runs/demo/report.json", map[string]string{"verdict": "UNKNOWN"}); !errors.Is(err, workspace.ErrNotLocked) {
		t.Fatalf("unlocked write: %v", err)
	}
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := s.WriteJSON("runs/demo/report.json", map[string]string{"verdict": "UNKNOWN"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteJSON("runs/demo/report.json", make(chan int)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	var got map[string]string
	if err := s.ReadJSON("runs/demo/report.json", &got); err != nil {
		t.Fatal(err)
	}
	if got["verdict"] != "UNKNOWN" {
		t.Fatalf("previous report lost: %v", got)
	}
}

func TestRejectsPathsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	s, err := workspace.Open(filepath.Join(dir, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lease, err := s.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, name := range []string{"../outside.json", filepath.Join(dir, "outside.json")} {
		if err := s.WriteJSON(name, map[string]bool{"escape": true}); err == nil {
			t.Fatalf("accepted path %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "outside.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped write: %v", err)
	}
}
