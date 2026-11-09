// Package oplog configures the process-wide structured operational logger.
package oplog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Level  string
	Format string
	File   string
}

type fanoutWriter struct {
	fallback *queuedWriter
	file     *queuedWriter
}

func (w *fanoutWriter) Write(record []byte) (int, error) {
	n, err := w.fallback.Write(record)
	if err != nil {
		return n, err
	}
	_, _ = w.file.Write(record)
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
	format = strings.ToLower(format)
	if format != "text" && format != "json" {
		return nil, nil, fmt.Errorf("LOG_CONFIG_INVALID: log format must be text or json")
	}
	options := &slog.HandlerOptions{Level: level}
	fallbackQueue := newQueuedWriter(fallback, format, nil)
	var destination io.Writer = fallbackQueue
	var fileQueue *queuedWriter
	var file io.Closer
	filePath := choose(config.File, os.Getenv("READYSPEC_LOG_FILE"), "")
	if filePath != "" {
		opened, err := OpenFile(filePath)
		if err != nil {
			_ = fallbackQueue.Close()
			return nil, nil, err
		}
		file = opened
		fallbackQueue.format = format
		fileQueue = newQueuedFileWriter(opened, format, func() {
			_, _ = fallbackQueue.Write([]byte("ReadySpec: operational log file write failed; continuing on stderr\n"))
		}, func(count uint64) {
			_, _ = fallbackQueue.Write(formatDropRecord(format, count))
		})
		destination = &fanoutWriter{fallback: fallbackQueue, file: fileQueue}
	}
	var handler slog.Handler
	switch format {
	case "text":
		handler = slog.NewTextHandler(destination, options)
	case "json":
		handler = slog.NewJSONHandler(destination, options)
	}
	return slog.New(handler), &queuedCloser{fileQueue: fileQueue, fallback: fallbackQueue, file: file}, nil
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
