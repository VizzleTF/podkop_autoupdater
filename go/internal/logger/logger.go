// Package logger writes timestamped log lines to a file and rotates the
// file in place when it grows past a soft line limit.
package logger

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Logger struct {
	mu        sync.Mutex
	path      string
	maxLines  int
	keepLines int
}

var defaultLogger *Logger

// Init configures the package-level logger.
func Init(path string, maxLines, keepLines int) error {
	if maxLines <= 0 {
		maxLines = 200
	}
	if keepLines <= 0 || keepLines >= maxLines {
		keepLines = maxLines / 2
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("logger: mkdir parent: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("logger: open: %w", err)
	}
	f.Close()
	defaultLogger = &Logger{path: path, maxLines: maxLines, keepLines: keepLines}
	return nil
}

// Logf writes one unleveled line. Mirrors bash log().
func Logf(format string, args ...any) {
	if defaultLogger == nil {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		return
	}
	defaultLogger.write(fmt.Sprintf(format, args...))
}

// Errf logs with "Error: " prefix.
func Errf(format string, args ...any) {
	Logf("Error: "+format, args...)
}

func (l *Logger) write(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	ts := time.Now().Format("2006-01-02 15:04:05")
	full := ts + " " + line + "\n"

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintln(os.Stderr, "logger: open:", err)
		return
	}
	if _, err := f.WriteString(full); err != nil {
		fmt.Fprintln(os.Stderr, "logger: write:", err)
	}
	f.Close()

	l.rotateIfNeeded()
}

func (l *Logger) rotateIfNeeded() {
	tail, total, err := readTail(l.path, l.keepLines)
	if err != nil || total <= l.maxLines {
		return
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tail), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}

// readTail scans path once and returns the last `keep` lines joined with
// newlines (plus a trailing newline) along with the total line count.
func readTail(path string, keep int) (tail string, total int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	ring := make([]string, 0, keep)
	for s.Scan() {
		total++
		if len(ring) < keep {
			ring = append(ring, s.Text())
		} else {
			copy(ring, ring[1:])
			ring[keep-1] = s.Text()
		}
	}
	if err := s.Err(); err != nil {
		return "", total, err
	}
	return strings.Join(ring, "\n") + "\n", total, nil
}
