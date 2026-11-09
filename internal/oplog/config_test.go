package oplog

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) {
	return 0, errors.New("private underlying writer error")
}

func TestFanoutFallsBackOnceWithoutPrintingWriterError(t *testing.T) {
	var fallback bytes.Buffer
	writer := &fanoutWriter{fallback: &fallback, file: failedWriter{}}
	for _, record := range []string{"first\n", "second\n"} {
		if _, err := writer.Write([]byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(fallback.String(), "private underlying") || strings.Count(fallback.String(), "log file write failed") != 1 || !strings.Contains(fallback.String(), "first\n") || !strings.Contains(fallback.String(), "second\n") {
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
	if closer != nil {
		t.Fatal("unexpected log file")
	}
	logger.Info("must be filtered", "event", "filtered")
	logger.Error("safe failure", "event", "worker.stopped", "reason_code", "RUN_WORKER_STOPPED")
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

func TestConfigureWritesToFileAndStderr(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
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
