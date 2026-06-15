package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSeenAndMarkPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	if s.Seen("k", time.Hour, now) {
		t.Fatal("nothing recorded yet, should not be seen")
	}
	if err := s.Mark("k", now); err != nil {
		t.Fatal(err)
	}
	if !s.Seen("k", time.Hour, now.Add(30*time.Minute)) {
		t.Fatal("should be seen within window")
	}
	if s.Seen("k", time.Hour, now.Add(2*time.Hour)) {
		t.Fatal("should be expired outside window")
	}

	// Reopen and confirm persistence.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("k"); !ok {
		t.Fatal("state did not persist across reopen")
	}
}

func TestInMemoryStore(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.Mark("x", now); err != nil {
		t.Fatal(err)
	}
	if !s.Seen("x", 0, now) {
		t.Fatal("zero window means ever-seen")
	}
}
