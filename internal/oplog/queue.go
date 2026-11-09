package oplog

import (
	"fmt"
	"io"
	"sync"
)

const (
	maxQueuedRecords = 1024
	maxQueuedBytes   = 4 << 20
	maxQueuedRecord  = 4 << 10
)

type queuedWriter struct {
	mu          sync.Mutex
	ready       *sync.Cond
	destination io.Writer
	format      string
	onFailure   func()
	onDrop      func(uint64)
	queue       [][]byte
	queuedBytes int
	pending     int
	dropped     uint64
	closed      bool
	failed      bool
	done        chan struct{}
	failureOnce sync.Once
}

func newQueuedWriter(destination io.Writer, format string, onFailure func()) *queuedWriter {
	return newQueuedWriterWithDrops(destination, format, onFailure, nil)
}

func newQueuedFileWriter(destination io.Writer, format string, onFailure func(), onDrop func(uint64)) *queuedWriter {
	return newQueuedWriterWithDrops(destination, format, onFailure, onDrop)
}

func newQueuedWriterWithDrops(destination io.Writer, format string, onFailure func(), onDrop func(uint64)) *queuedWriter {
	writer := &queuedWriter{
		destination: destination,
		format:      format,
		onFailure:   onFailure,
		onDrop:      onDrop,
		done:        make(chan struct{}),
	}
	writer.ready = sync.NewCond(&writer.mu)
	go writer.run()
	return writer
}

func (w *queuedWriter) Write(record []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, fmt.Errorf("LOG_WRITER_CLOSED")
	}
	if w.failed {
		w.dropped++
		w.ready.Signal()
		return len(record), nil
	}
	if len(record) == 0 {
		return 0, nil
	}
	if len(record) > maxQueuedRecord {
		record = oversizedRecord(w.format, len(record))
	}
	if w.pending >= maxQueuedRecords || w.queuedBytes+len(record) > maxQueuedBytes {
		w.dropped++
		w.ready.Signal()
		return len(record), nil
	}
	copyOfRecord := append([]byte(nil), record...)
	w.queue = append(w.queue, copyOfRecord)
	w.queuedBytes += len(copyOfRecord)
	w.pending++
	w.ready.Signal()
	return len(record), nil
}

func oversizedRecord(format string, bytes int) []byte {
	if format == "json" {
		return []byte(fmt.Sprintf(`{"level":"WARN","event":"log.record_omitted","reason_code":"LOG_RECORD_TOO_LARGE","omitted_bytes":%d}`+"\n", bytes))
	}
	return []byte(fmt.Sprintf("level=WARN event=log.record_omitted reason_code=LOG_RECORD_TOO_LARGE omitted_bytes=%d\n", bytes))
}

func (w *queuedWriter) Close() error {
	w.mu.Lock()
	w.closed = true
	w.ready.Broadcast()
	w.mu.Unlock()
	<-w.done
	return nil
}

func (w *queuedWriter) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && w.dropped == 0 && !w.closed {
			w.ready.Wait()
		}
		if len(w.queue) == 0 && w.dropped > 0 {
			dropped := w.dropped
			w.dropped = 0
			failed := w.failed
			w.mu.Unlock()
			if failed && w.onDrop != nil {
				w.onDrop(dropped)
			} else {
				if err := w.writeDirect(w.dropRecord(dropped)); err != nil {
					w.mu.Lock()
					w.failed = true
					w.mu.Unlock()
					if w.onFailure != nil {
						w.failureOnce.Do(w.onFailure)
					}
					if w.onDrop != nil {
						w.onDrop(dropped)
					}
				}
			}
			continue
		}
		if len(w.queue) == 0 && w.closed {
			w.mu.Unlock()
			return
		}
		record := w.queue[0]
		w.queue[0] = nil
		w.queue = w.queue[1:]
		w.mu.Unlock()

		writeErr := w.writeDirect(record)

		w.mu.Lock()
		w.queuedBytes -= len(record)
		w.pending--
		if writeErr != nil && !w.failed {
			w.failed = true
			w.dropped += uint64(len(w.queue) + 1)
			w.pending -= len(w.queue)
			w.queue = nil
			w.queuedBytes = 0
		}
		w.ready.Broadcast()
		w.mu.Unlock()
		if writeErr != nil && w.onFailure != nil {
			w.failureOnce.Do(w.onFailure)
		}
	}
}

func (w *queuedWriter) dropRecord(count uint64) []byte {
	return formatDropRecord(w.format, count)
}

func formatDropRecord(format string, count uint64) []byte {
	if format == "json" {
		return []byte(fmt.Sprintf(`{"level":"WARN","event":"log.records_dropped","count":%d}`+"\n", count))
	}
	return []byte(fmt.Sprintf("level=WARN event=log.records_dropped count=%d\n", count))
}

func (w *queuedWriter) writeDirect(record []byte) error {
	n, err := w.destination.Write(record)
	if err == nil && n != len(record) {
		err = io.ErrShortWrite
	}
	return err
}

type queuedCloser struct {
	fileQueue *queuedWriter
	fallback  *queuedWriter
	file      io.Closer
	once      sync.Once
}

func (c *queuedCloser) Close() error {
	var closeErr error
	c.once.Do(func() {
		if c.fileQueue != nil {
			_ = c.fileQueue.Close()
		}
		if c.file != nil {
			closeErr = c.file.Close()
		}
		if c.fallback != nil {
			_ = c.fallback.Close()
		}
	})
	return closeErr
}
