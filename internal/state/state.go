// Package state provides a small JSON-file key/value store, used by the runner
// to remember the status each reporter was last told about a check so alerts
// fire on change instead of every run. It is safe for concurrent use within one
// process.
package state

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry is one stored record: when it was written, and the check status that
// was reported at that time.
type Entry struct {
	Time   time.Time `json:"time"`
	Status string    `json:"status,omitempty"`
}

// UnmarshalJSON also accepts the legacy bare RFC3339 string form written by
// syscheckr <= 0.1.6, so an existing state file still loads.
func (e *Entry) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &e.Time)
	}
	type plain Entry // shed this method, avoiding infinite recursion
	return json.Unmarshal(data, (*plain)(e))
}

// Store is a concurrency-safe JSON-backed key/entry store.
type Store struct {
	mu   sync.Mutex
	path string
	// ponytail: entries for deleted checks/reporters are never pruned. The file
	// is a handful of bytes per check; add pruning if that ever stops being true.
	entries map[string]Entry
}

// Open loads the store at path, creating an empty one if the file is absent.
// An empty path yields an in-memory store that is never persisted.
func Open(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]Entry{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s.entries); err != nil {
			// This is a suppression cache, not a source of truth. Refusing to
			// start would take every check down over a damaged file; starting
			// empty costs at most one duplicate alert per check.
			slog.Warn("discarding unreadable state file, alerts may repeat once",
				"path", path, "error", err)
			s.entries = map[string]Entry{}
		}
	}
	return s, nil
}

// Get returns the stored entry for key and whether it was present.
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

// Set records an entry in memory. Callers batch several Sets then Flush once,
// since every Flush rewrites the whole file.
func (s *Store) Set(key string, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = e
}

// Flush writes the store to disk. It is a no-op for an in-memory store.
func (s *Store) Flush() error {
	if s.path == "" {
		return nil
	}
	// Hold the lock across marshal + write + rename so concurrent persists
	// (e.g. overlapping daemon schedule groups, which share one Store) cannot
	// race. The temp file uses a unique name so even an unlocked future caller
	// could not clobber another's in-flight write.
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	dir := filepath.Dir(s.path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create state dir: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp state: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp state: %w", err)
	}
	return os.Rename(tmpName, s.path)
}
