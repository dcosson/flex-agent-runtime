// Package sessionlog provides a JSONL file tailer that polls for new lines.
package sessionlog

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	defaultPollInterval = 500 * time.Millisecond
)

// Tailer polls a JSONL file for new lines and calls OnLine for each.
type Tailer struct {
	path         string
	onLine       func(line []byte)
	pollInterval time.Duration

	mu      sync.Mutex
	started bool
	stopped bool
}

// Option configures the Tailer.
type Option func(*Tailer)

// WithPollInterval sets the polling interval.
func WithPollInterval(d time.Duration) Option {
	return func(t *Tailer) {
		t.pollInterval = d
	}
}

// New creates a new session log tailer.
func New(path string, onLine func(line []byte), opts ...Option) *Tailer {
	t := &Tailer{
		path:         path,
		onLine:       onLine,
		pollInterval: defaultPollInterval,
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Start begins tailing the log file. Blocks until the context is cancelled.
// Handles the file not existing yet — waits for it to appear.
func (t *Tailer) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return fmt.Errorf("tailer already started")
	}
	t.started = true
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.stopped = true
		t.mu.Unlock()
	}()

	var (
		offset  int64
		partial []byte
	)

	ticker := time.NewTicker(t.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			newOffset, newPartial, err := t.poll(offset, partial)
			if err != nil {
				// File might not exist yet — keep polling
				continue
			}
			offset = newOffset
			partial = newPartial
		}
	}
}

func (t *Tailer) poll(offset int64, partial []byte) (int64, []byte, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return offset, partial, err
	}
	defer f.Close()

	// Check file size
	info, err := f.Stat()
	if err != nil {
		return offset, partial, err
	}

	// File was truncated — reset
	if info.Size() < offset {
		offset = 0
		partial = nil
	}

	// No new data
	if info.Size() == offset {
		return offset, partial, nil
	}

	// Seek to where we left off
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, partial, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024) // Up to 1MB per line

	var bytesRead int64
	for scanner.Scan() {
		line := scanner.Bytes()
		bytesRead += int64(len(line)) + 1 // +1 for newline

		if len(partial) > 0 {
			// Prepend any partial line from the previous poll
			full := make([]byte, len(partial)+len(line))
			copy(full, partial)
			copy(full[len(partial):], line)
			partial = nil
			if t.onLine != nil {
				t.onLine(full)
			}
		} else {
			cp := make([]byte, len(line))
			copy(cp, line)
			if t.onLine != nil {
				t.onLine(cp)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return offset + bytesRead, partial, err
	}

	// Check if we read to the end of the file or if there's a partial line
	newOffset := offset + bytesRead
	remaining := info.Size() - newOffset
	if remaining > 0 {
		// There's data remaining that didn't end with a newline — partial line
		buf := make([]byte, remaining)
		if _, err := f.Seek(newOffset, io.SeekStart); err == nil {
			if n, err := f.Read(buf); err == nil {
				partial = buf[:n]
				newOffset += int64(n)
			}
		}
	}

	return newOffset, partial, nil
}
