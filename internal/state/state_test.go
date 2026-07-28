package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetAndFlushPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	if _, ok := s.Get("k"); ok {
		t.Fatal("nothing recorded yet")
	}
	s.Set("k", Entry{Time: now, Status: "crit"})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s2.Get("k")
	if !ok {
		t.Fatal("state did not persist across reopen")
	}
	if got.Status != "crit" || !got.Time.Equal(now) {
		t.Fatalf("got %+v, want crit at %v", got, now)
	}
}

// State files written by syscheckr <= 0.1.6 stored bare timestamps.
func TestOpenReadsLegacyTimestampEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"linear:disk":"2026-06-15T12:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("legacy state should still load: %v", err)
	}
	got, ok := s.Get("linear:disk")
	if !ok {
		t.Fatal("legacy entry missing")
	}
	if want := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC); !got.Time.Equal(want) {
		t.Fatalf("got %v, want %v", got.Time, want)
	}
	if got.Status != "" {
		t.Fatalf("legacy entry has no status, got %q", got.Status)
	}
}

// Regression: state is a suppression cache. A damaged file must not stop every
// check from running — it starts empty and costs one repeated alert instead.
func TestOpenSurvivesCorruptStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"status:disk": {"time": tru`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("corrupt state should not block startup: %v", err)
	}
	if _, ok := s.Get("status:disk"); ok {
		t.Fatal("corrupt entries should be discarded, not partially kept")
	}
	// The store must still be usable afterwards.
	s.Set("status:disk", Entry{Time: time.Now(), Status: "ok"})
	if err := s.Flush(); err != nil {
		t.Fatalf("store unusable after discarding corrupt state: %v", err)
	}
}

func TestInMemoryStore(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	s.Set("x", Entry{Time: time.Now(), Status: "ok"})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("x"); !ok {
		t.Fatal("in-memory store should still read back")
	}
}
