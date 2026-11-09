package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockedCloser struct{ release <-chan struct{} }

type startupWriter struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	started chan struct{}
	once    sync.Once
}

func (w *startupWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buffer.Write(value)
	text := w.buffer.String()
	w.mu.Unlock()
	if strings.Contains(text, "ReadySpec Web UI:") {
		w.once.Do(func() { close(w.started) })
	}
	return n, err
}

var _ io.Writer = (*startupWriter)(nil)

func (c blockedCloser) Close() error {
	<-c.release
	return nil
}

func TestWaitUntilUsesSharedDeadlineAndWorkerCap(t *testing.T) {
	finished := make(chan struct{})
	deadline := time.Now().Add(80 * time.Millisecond)
	start := time.Now()
	if waitUntil(finished, deadline, 4*time.Second) {
		t.Fatal("unfinished worker reported stopped")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("wait exceeded shared deadline: %s", elapsed)
	}
	finished = make(chan struct{})
	close(finished)
	if !waitUntil(finished, time.Now().Add(time.Second), time.Second) {
		t.Fatal("completed worker was not observed")
	}
}

func TestLogFlushBudgetDoesNotBlockServiceExit(t *testing.T) {
	release := make(chan struct{})
	closer := blockedCloser{release: release}
	start := time.Now()
	err := closeLogFileBounded(closer, time.Now().Add(60*time.Millisecond))
	if err == nil || err.Error() != "LOG_FLUSH_TIMEOUT" {
		t.Fatalf("close result=%v, want bounded flush timeout", err)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Fatalf("log flush exceeded shutdown budget: %s", time.Since(start))
	}
	close(release)
}

func TestBoundedDurationNeverExceedsRemainingBudget(t *testing.T) {
	if got := boundedDuration(100*time.Millisecond, 3*time.Second); got != 100*time.Millisecond {
		t.Fatalf("bounded duration=%s", got)
	}
	if got := boundedDuration(4*time.Second, 3*time.Second); got != 3*time.Second {
		t.Fatalf("bounded duration=%s", got)
	}
	if got := boundedDuration(-time.Second, 3*time.Second); got != time.Nanosecond {
		t.Fatalf("expired deadline duration=%s", got)
	}
}

func TestServeSignalStopsWorkerAndHTTPWithinSharedBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workspaceDir := t.TempDir()
	output := &startupWriter{started: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		result <- serve(ctx, serveOptions{
			listen: "127.0.0.1:0", workspace: workspaceDir, token: "shutdown-test-token-long-enough-1234567890",
			noBrowser: true, logLevel: "error", logFormat: "json",
		}, true, output)
	}()
	select {
	case <-output.started:
	case <-time.After(5 * time.Second):
		t.Fatal("service did not reach its serving state")
	}
	started := time.Now()
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("graceful shutdown returned error: %v", err)
		}
	case <-time.After(5*time.Second + 250*time.Millisecond):
		t.Fatal("HTTP and worker shutdown exceeded the five-second budget")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("shutdown exceeded five seconds: %s", elapsed)
	}
}
