package oplog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("private underlying writer error")
}

type gatedWriter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	output  bytes.Buffer
}

func (w *gatedWriter) Write(value []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	return w.output.Write(value)
}

func TestFanoutFallsBackOnceWithoutPrintingWriterError(t *testing.T) {
	var fallback bytes.Buffer
	fallbackQueue := newQueuedWriter(&fallback, "text", nil)
	fileQueue := newQueuedFileWriter(failedWriter{}, "text", func() {
		_, _ = fallbackQueue.Write([]byte("ReadySpec: operational log file write failed; continuing on stderr\n"))
	}, func(count uint64) {
		_, _ = fallbackQueue.Write(formatDropRecord("text", count))
	})
	writer := &fanoutWriter{fallback: fallbackQueue, file: fileQueue}
	for _, record := range []string{"first\n", "second\n"} {
		if _, err := writer.Write([]byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	_ = fileQueue.Close()
	_ = fallbackQueue.Close()
	if strings.Contains(fallback.String(), "private underlying") || strings.Count(fallback.String(), "log file write failed") != 1 || !strings.Contains(fallback.String(), "first\n") || !strings.Contains(fallback.String(), "second\n") || !strings.Contains(fallback.String(), "event=log.records_dropped count=2") {
		t.Fatalf("unexpected degraded output: %q", fallback.String())
	}
	var _ io.Writer = writer
}

func TestConfigureJSONLevelAndPrecedence(t *testing.T) {
	var output bytes.Buffer
	logger, closer, err := Configure(Config{Level: "error", Format: "json"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if closer == nil {
		t.Fatal("configured queue must be flushable")
	}
	logger.Info("must be filtered", "event", "filtered")
	logger.Error("safe failure", "event", "worker.stopped", "reason_code", "RUN_WORKER_STOPPED")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "filtered") || !strings.Contains(output.String(), `"reason_code":"RUN_WORKER_STOPPED"`) {
		t.Fatalf("unexpected structured log output: %s", output.String())
	}
}

func TestConfigureRejectsUnknownSettings(t *testing.T) {
	for _, config := range []Config{{Level: "trace"}, {Format: "yaml"}} {
		if _, _, err := Configure(config, &bytes.Buffer{}); err == nil {
			t.Fatalf("Configure(%+v) unexpectedly succeeded", config)
		}
	}
	if slog.LevelInfo.String() != "INFO" {
		t.Fatal("unexpected slog level contract")
	}
}

func TestConfigureQueuesSlowWriterAndReportsAggregatedDrops(t *testing.T) {
	writer := &gatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	logger, closer, err := Configure(Config{Level: "info", Format: "json"}, writer)
	if err != nil {
		t.Fatal(err)
	}
	if closer == nil {
		t.Fatal("queued stderr writer must be flushable")
	}
	logged := make(chan struct{})
	go func() {
		logger.Info("first")
		close(logged)
	}()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		close(writer.release)
		_ = closer.Close()
		t.Fatal("writer did not receive first record")
	}
	select {
	case <-logged:
	case <-time.After(time.Second):
		close(writer.release)
		_ = closer.Close()
		t.Fatal("logging call blocked behind slow output writer")
	}
	for i := 0; i < 1200; i++ {
		logger.Info("queued", "sequence", i)
	}
	closed := make(chan error, 1)
	go func() { closed <- closer.Close() }()
	close(writer.release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued writer did not flush after release")
	}
	if !strings.Contains(writer.output.String(), `"event":"log.records_dropped"`) || !strings.Contains(writer.output.String(), `"count":`) {
		t.Fatalf("overflow did not emit an aggregated drop record: %s", writer.output.String())
	}
}

func TestConfigureReplacesOversizedRecordWithoutLeakingItsContents(t *testing.T) {
	var output bytes.Buffer
	logger, closer, err := Configure(Config{Level: "info", Format: "json"}, &output)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "oversized-secret-fixture"
	logger.Info(strings.Repeat("x", maxQueuedRecord)+secret, "event", "fixture.event")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(output.String())
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("replacement log is not valid JSON: %v: %q", err, line)
	}
	if record["event"] != "log.record_omitted" || record["reason_code"] != "LOG_RECORD_TOO_LARGE" || strings.Contains(line, secret) {
		t.Fatalf("oversized record was not safely replaced: %s", line)
	}
}

func TestConfigureWritesToFileAndStderr(t *testing.T) {
	dir := privateLogTestDir(t)
	path := filepath.Join(dir, "service.jsonl")
	var stderr bytes.Buffer
	logger, closer, err := Configure(Config{Level: "info", Format: "json", File: path}, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("accepted", "event", "submission.accepted", "run_id", "run-fixture")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileBytes), `"event":"submission.accepted"`) || stderr.String() != string(fileBytes) {
		t.Fatalf("file/stderr outputs differ or lack structured event: file=%q stderr=%q", fileBytes, stderr.String())
	}
}
