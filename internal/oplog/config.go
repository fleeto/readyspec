// Package oplog configures the process-wide structured operational logger.
package oplog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

type Config struct {
	Level  string
	Format string
	File   string
}

type fanoutWriter struct {
	mu              sync.Mutex
	fallback        io.Writer
	file            io.Writer
	failureReported bool
}

func (w *fanoutWriter) Write(record []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.fallback.Write(record)
	if err != nil {
		return n, err
	}
	fileN, fileErr := w.file.Write(record)
	if fileErr == nil && fileN != len(record) {
		fileErr = io.ErrShortWrite
	}
	if fileErr != nil && !w.failureReported {
		w.failureReported = true
		_, _ = io.WriteString(w.fallback, "ReadySpec: operational log file write failed; continuing on stderr\n")
	}
	return n, nil
}

// Configure validates settings and installs a standard-library structured
// logger. Explicit values take precedence over the corresponding environment
// variables; defaults are info and text.
func Configure(config Config, fallback io.Writer) (*slog.Logger, io.Closer, error) {
	levelValue := choose(config.Level, os.Getenv("READYSPEC_LOG_LEVEL"), "info")
	format := choose(config.Format, os.Getenv("READYSPEC_LOG_FORMAT"), "text")
	var level slog.Level
	switch strings.ToLower(levelValue) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, nil, fmt.Errorf("LOG_CONFIG_INVALID: log level must be debug, info, warn, or error")
	}
	options := &slog.HandlerOptions{Level: level}
	var destination io.Writer = fallback
	var closer io.Closer
	filePath := choose(config.File, os.Getenv("READYSPEC_LOG_FILE"), "")
	if filePath != "" {
		file, err := OpenFile(filePath)
		if err != nil {
			return nil, nil, err
		}
		closer = file
		destination = &fanoutWriter{fallback: fallback, file: file}
	}
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text":
		handler = slog.NewTextHandler(destination, options)
	case "json":
		handler = slog.NewJSONHandler(destination, options)
	default:
		if closer != nil {
			_ = closer.Close()
		}
		return nil, nil, fmt.Errorf("LOG_CONFIG_INVALID: log format must be text or json")
	}
	return slog.New(handler), closer, nil
}

func choose(explicit, environment, fallback string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	if strings.TrimSpace(environment) != "" {
		return strings.TrimSpace(environment)
	}
	return fallback
}
