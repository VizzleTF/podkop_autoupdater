// Package logger writes timestamped log lines to a file and rotates the
// file in place when it grows past a soft line limit. Mirrors the
// behavior of rotate_log() in podkop_updater.sh.
package logger

import (
	"bufio"
	"fmt"
	"os"
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
	if err := os.MkdirAll(parentDir(path), 0o755); err != nil {
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

func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "."
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
	count, err := countLines(l.path)
	if err != nil || count <= l.maxLines {
		return
	}
	tail, err := readLastLines(l.path, l.keepLines)
	if err != nil {
		return
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tail), 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}

func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for s.Scan() {
		n++
	}
	return n, s.Err()
}

func readLastLines(path string, want int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	lines := make([]string, 0, want)
	for s.Scan() {
		lines = append(lines, s.Text())
		if len(lines) > want*2 {
			lines = lines[len(lines)-want:]
		}
	}
	if err := s.Err(); err != nil {
		return "", err
	}
	if len(lines) > want {
		lines = lines[len(lines)-want:]
	}
	return strings.Join(lines, "\n") + "\n", nil
}
