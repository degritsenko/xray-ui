package app

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	pending string
	secrets []string
	out     io.Writer
}

var logUUID = regexp.MustCompile(`(?i)[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}`)
var logURL = regexp.MustCompile(`https?://[^\s]+`)

func NewLogBuffer(out io.Writer) *LogBuffer { return &LogBuffer{out: out} }
func (l *LogBuffer) SetSecrets(s []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.secrets = append([]string(nil), s...)
}
func (l *LogBuffer) addLocked(message string) {
	for _, s := range l.secrets {
		if len(s) >= 8 {
			message = strings.ReplaceAll(message, s, "[redacted]")
		}
	}
	message = logUUID.ReplaceAllString(message, "[UUID redacted]")
	message = logURL.ReplaceAllString(message, "[URL redacted]")
	if len(message) > 4096 {
		message = message[:4096] + "…"
	}
	l.entries = append(l.entries, LogEntry{Time: time.Now().UTC(), Message: message})
	if len(l.entries) > 300 {
		l.entries = l.entries[len(l.entries)-300:]
	}
	if l.out != nil {
		_, _ = fmt.Fprintln(l.out, message)
	}
}
func (l *LogBuffer) Add(message string) { l.mu.Lock(); defer l.mu.Unlock(); l.addLocked(message) }
func (l *LogBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pending += string(p)
	for {
		index := strings.IndexByte(l.pending, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(l.pending[:index])
		l.pending = l.pending[index+1:]
		if line != "" {
			l.addLocked(line)
		}
	}
	if len(l.pending) > 128*1024 {
		l.pending = ""
		l.addLocked("Oversized log line omitted")
	}
	return len(p), nil
}
func (l *LogBuffer) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]LogEntry(nil), l.entries...)
}
