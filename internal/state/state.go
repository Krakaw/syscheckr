// Package state provides a small JSON-file key/value store with timestamps,
// used by reporters for dedupe (e.g. avoid filing duplicate Linear tickets) and
// by the daemon for last-run bookkeeping. It is safe for concurrent use within
// one process.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is a concurrency-safe JSON-backed key/time store.
type Store struct {
	mu      sync.Mutex
	path    string
	entries map[string]time.Time
}

// Open loads the store at path, creating an empty one if the file is absent.
// An empty path yields an in-memory store that is never persisted.
func Open(path string) (*Store, error) {
	s := &Store{path: path, entries: map[string]time.Time{}}
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
			return nil, fmt.Errorf("parse state %s: %w", path, err)
		}
	}
	return s, nil
}

// Get returns the stored timestamp for key and whether it was present.
func (s *Store) Get(key string) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.entries[key]
	return t, ok
}

// Seen reports whether key was recorded within the last `within` duration. A
// non-positive window means "ever recorded".
func (s *Store) Seen(key string, within time.Duration, now time.Time) bool {
	t, ok := s.Get(key)
	if !ok {
		return false
	}
	if within <= 0 {
		return true
	}
	return now.Sub(t) < within
}

// Mark records key at time now and persists the store.
func (s *Store) Mark(key string, now time.Time) error {
	s.mu.Lock()
	s.entries[key] = now
	s.mu.Unlock()
	return s.persist()
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	// Hold the lock across marshal + write + rename so concurrent persists
	// (e.g. overlapping daemon schedule groups sharing one reporter) cannot
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
