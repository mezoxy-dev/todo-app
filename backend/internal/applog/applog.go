// Package applog is the in-memory log of the todo app: a ring buffer of the
// latest entries that the log panel of the UI reads over the API. Everything
// that is added is also written to the console with log/slog.
//
// Neither the entries nor the console output may ever contain the API key of
// the AI service; the callers see to that.
package applog

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// The levels of an entry.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// The sources of an entry: the request handlers, the AI worker, the server itself.
const (
	SourceAPI    = "api"
	SourceAI     = "ai"
	SourceSystem = "system"
)

// Entry is one line of the log, with the details that can be unfolded.
type Entry struct {
	Seq     int64     `json:"seq"` // 1, 2, 3, ...; a reader continues from the last one it has seen
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`             // LevelInfo, LevelWarn or LevelError
	Source  string    `json:"source"`            // SourceAPI, SourceAI or SourceSystem
	Message string    `json:"message"`           // one line
	Details string    `json:"details,omitempty"` // several lines; optional
}

// Buffer holds the latest entries. It is safe for concurrent use.
type Buffer struct {
	mu      sync.RWMutex
	ring    []Entry // entry n (n >= 1) lives at ring[(n-1) % len(ring)] while it is among the newest len(ring)
	lastSeq int64   // Seq of the newest entry; 0 while the buffer is empty
}

// New returns a buffer that keeps the latest capacity entries; once it is full,
// every new entry pushes the oldest one out. A capacity below 1 is taken as 1.
func New(capacity int) *Buffer {
	return &Buffer{ring: make([]Entry, max(capacity, 1))}
}

// Add appends an entry, gives it the next sequence number and the current time
// (UTC), writes it to the console with slog at its level, and returns it. The
// caller makes sure that message is one line (see Quote) and that nothing in
// message or details is secret.
func (b *Buffer) Add(level, source, message, details string) Entry {
	e := Entry{Level: level, Source: source, Message: message, Details: details}

	b.mu.Lock()
	b.lastSeq++
	e.Seq = b.lastSeq
	e.Time = time.Now().UTC()
	b.ring[(e.Seq-1)%int64(len(b.ring))] = e
	b.mu.Unlock()

	attrs := []slog.Attr{slog.String("source", source), slog.Int64("seq", e.Seq)}
	if details != "" {
		attrs = append(attrs, slog.String("details", details))
	}
	slog.LogAttrs(context.Background(), slogLevel(level), message, attrs...)
	return e
}

// slogLevel maps the level of an entry to the level of the console record.
func slogLevel(level string) slog.Level {
	switch level {
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// After returns the entries with a Seq greater than after, oldest first, and
// the Seq of the newest entry the buffer has (0 for an empty buffer), which is
// where the reader continues. If there are more than limit of them, only the
// newest limit are returned and truncated is true. Entries that the buffer has
// already dropped are gone; they do not count as truncated. The slice is never
// nil. A negative after reads from the oldest entry; a limit below 0 is taken
// as 0.
func (b *Buffer) After(after int64, limit int) (entries []Entry, lastSeq int64, truncated bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	lastSeq = b.lastSeq
	if after >= lastSeq { // also guards after+1 below against overflow
		return []Entry{}, lastSeq, false
	}
	capacity := int64(len(b.ring))
	oldest := max(lastSeq-capacity+1, 1) // Seq of the oldest entry still held
	first := max(after+1, oldest)
	if count := lastSeq - first + 1; count > int64(max(limit, 0)) {
		first = lastSeq - int64(max(limit, 0)) + 1
		truncated = true
	}

	entries = make([]Entry, 0, lastSeq-first+1)
	for seq := first; seq <= lastSeq; seq++ {
		entries = append(entries, b.ring[(seq-1)%capacity])
	}
	return entries, lastSeq, truncated
}

// Quote puts a user-supplied text, such as the title of a todo, between the
// Turkish quotation marks the log messages use. A title may hold line breaks and
// runs of white space, a message is one line, so these become single spaces.
func Quote(s string) string {
	return "“" + strings.Join(strings.Fields(s), " ") + "”"
}
