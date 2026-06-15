package confutil

import (
	"strings"
	"testing"
	"time"
)

func TestStringAndRequired(t *testing.T) {
	m := New("c", map[string]any{"a": "hello", "empty": "", "nilval": nil})

	if got := m.StringDefault("a", "def"); got != "hello" {
		t.Errorf("StringDefault present = %q", got)
	}
	if got := m.StringDefault("missing", "def"); got != "def" {
		t.Errorf("StringDefault missing = %q, want def", got)
	}
	if m.Err() != nil {
		t.Fatalf("unexpected err: %v", m.Err())
	}

	// Required on present-but-empty should fail.
	m2 := New("c", map[string]any{"a": ""})
	m2.Required("a")
	if m2.Err() == nil || !strings.Contains(m2.Err().Error(), "must not be empty") {
		t.Errorf("empty Required err = %v", m2.Err())
	}

	// Required on missing should fail.
	m3 := New("c", nil)
	m3.Required("a")
	if m3.Err() == nil || !strings.Contains(m3.Err().Error(), "is required") {
		t.Errorf("missing Required err = %v", m3.Err())
	}

	// nil value treated as absent (e.g. unset ${ENV}).
	m4 := New("c", map[string]any{"nilval": nil})
	if m4.Has("nilval") {
		t.Error("nil value should be treated as absent")
	}
	m4.Required("nilval")
	if m4.Err() == nil || !strings.Contains(m4.Err().Error(), "is required") {
		t.Errorf("nil Required err = %v", m4.Err())
	}
}

func TestStringWrongType(t *testing.T) {
	m := New("c", map[string]any{"a": 123})
	m.String("a")
	if m.Err() == nil || !strings.Contains(m.Err().Error(), "expected string") {
		t.Errorf("err = %v", m.Err())
	}
}

func TestFloatCoercion(t *testing.T) {
	m := New("c", map[string]any{"f": 1.5, "i": 7, "i64": int64(9)})
	if got := m.Float("f", 0); got != 1.5 {
		t.Errorf("float = %v", got)
	}
	if got := m.Float("i", 0); got != 7 {
		t.Errorf("int->float = %v", got)
	}
	if got := m.Float("i64", 0); got != 9 {
		t.Errorf("int64->float = %v", got)
	}
	if got := m.Float("missing", 42); got != 42 {
		t.Errorf("default = %v", got)
	}
	if m.Err() != nil {
		t.Fatalf("unexpected err: %v", m.Err())
	}

	bad := New("c", map[string]any{"x": "nope"})
	bad.Float("x", 0)
	if bad.Err() == nil {
		t.Error("expected error for non-numeric float")
	}
}

func TestIntCoercion(t *testing.T) {
	m := New("c", map[string]any{"i": 3, "f": 4.0, "i64": int64(5)})
	if m.Int("i", 0) != 3 || m.Int("f", 0) != 4 || m.Int("i64", 0) != 5 {
		t.Errorf("int coercion failed")
	}
	if m.Int("missing", 11) != 11 {
		t.Errorf("int default failed")
	}
	if m.Err() != nil {
		t.Fatalf("unexpected err: %v", m.Err())
	}
}

func TestBool(t *testing.T) {
	m := New("c", map[string]any{"b": true})
	if !m.Bool("b", false) {
		t.Error("bool present failed")
	}
	if m.Bool("missing", true) != true {
		t.Error("bool default failed")
	}
	bad := New("c", map[string]any{"b": "yes"})
	bad.Bool("b", false)
	if bad.Err() == nil {
		t.Error("expected error for non-bool")
	}
}

func TestDuration(t *testing.T) {
	m := New("c", map[string]any{"d": "5m"})
	if got := m.Duration("d", 0); got != 5*time.Minute {
		t.Errorf("duration = %v", got)
	}
	if got := m.Duration("missing", time.Second); got != time.Second {
		t.Errorf("duration default = %v", got)
	}
	if m.Err() != nil {
		t.Fatalf("unexpected err: %v", m.Err())
	}

	bad := New("c", map[string]any{"d": "notaduration"})
	bad.Duration("d", 0)
	if bad.Err() == nil || !strings.Contains(bad.Err().Error(), "invalid duration") {
		t.Errorf("err = %v", bad.Err())
	}

	wrongType := New("c", map[string]any{"d": 5})
	wrongType.Duration("d", 0)
	if wrongType.Err() == nil {
		t.Error("expected error for non-string duration")
	}
}

func TestStringMap(t *testing.T) {
	m := New("c", map[string]any{
		"headers": map[string]any{"X-A": "1", "X-B": "2"},
	})
	got := m.StringMap("headers")
	if len(got) != 2 || got["X-A"] != "1" || got["X-B"] != "2" {
		t.Errorf("StringMap = %v", got)
	}
	if m.StringMap("missing") != nil {
		t.Error("missing StringMap should be nil")
	}
	if m.Err() != nil {
		t.Fatalf("unexpected err: %v", m.Err())
	}

	badVal := New("c", map[string]any{"headers": map[string]any{"k": 5}})
	badVal.StringMap("headers")
	if badVal.Err() == nil {
		t.Error("expected error for non-string map value")
	}

	badType := New("c", map[string]any{"headers": "notamap"})
	badType.StringMap("headers")
	if badType.Err() == nil {
		t.Error("expected error for non-map")
	}
}

func TestFirstErrorWins(t *testing.T) {
	m := New("c", map[string]any{"a": 1, "b": 2})
	m.String("a") // first failure
	m.String("b") // second failure ignored
	if m.Err() == nil || !strings.Contains(m.Err().Error(), `"a"`) {
		t.Errorf("expected first error about a, got %v", m.Err())
	}
}
