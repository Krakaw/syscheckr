// Package confutil provides typed accessors over the raw map[string]any config
// blocks produced by YAML decoding. Factories use it to read their settings
// with consistent error messages and type coercion.
package confutil

import (
	"fmt"
	"time"
)

// Map wraps a raw config map and tracks the first error encountered so callers
// can read several fields then check Err once.
type Map struct {
	raw  map[string]any
	err  error
	name string // owning check/reporter name, for error context
}

// New wraps a (possibly nil) raw config map for an entity called name.
func New(name string, raw map[string]any) *Map {
	if raw == nil {
		raw = map[string]any{}
	}
	return &Map{raw: raw, name: name}
}

// Err returns the first error encountered, or nil.
func (m *Map) Err() error { return m.err }

func (m *Map) fail(key string, format string, args ...any) {
	if m.err == nil {
		m.err = fmt.Errorf("%s: config %q: %s", m.name, key, fmt.Sprintf(format, args...))
	}
}

// Has reports whether key is present with a non-nil value. A key whose value is
// nil (e.g. an unset ${ENV} that expanded to empty) is treated as absent so
// factories report a clean "required"/"empty" message instead of a type error.
func (m *Map) Has(key string) bool {
	v, ok := m.raw[key]
	return ok && v != nil
}

// String returns a required string value.
func (m *Map) String(key string) string {
	return m.StringDefault(key, "")
}

// StringDefault returns a string value or def when absent.
func (m *Map) StringDefault(key, def string) string {
	if !m.Has(key) {
		return def
	}
	v := m.raw[key]
	s, ok := v.(string)
	if !ok {
		m.fail(key, "expected string, got %T", v)
		return def
	}
	return s
}

// Required marks key as mandatory, recording an error if it is missing or empty.
func (m *Map) Required(key string) string {
	if !m.Has(key) {
		m.fail(key, "is required")
		return ""
	}
	s := m.String(key)
	if s == "" {
		m.fail(key, "must not be empty")
	}
	return s
}

// Float returns a numeric value as float64, or def when absent.
func (m *Map) Float(key string, def float64) float64 {
	if !m.Has(key) {
		return def
	}
	v := m.raw[key]
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		m.fail(key, "expected number, got %T", v)
		return def
	}
}

// Int returns an integer value, or def when absent.
func (m *Map) Int(key string, def int) int {
	if !m.Has(key) {
		return def
	}
	v := m.raw[key]
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		m.fail(key, "expected integer, got %T", v)
		return def
	}
}

// Bool returns a boolean value, or def when absent.
func (m *Map) Bool(key string, def bool) bool {
	if !m.Has(key) {
		return def
	}
	b, ok := m.raw[key].(bool)
	if !ok {
		m.fail(key, "expected bool, got %T", m.raw[key])
		return def
	}
	return b
}

// Duration parses a Go duration string (e.g. "5m", "10s"), or returns def when
// absent.
func (m *Map) Duration(key string, def time.Duration) time.Duration {
	if !m.Has(key) {
		return def
	}
	v := m.raw[key]
	s, ok := v.(string)
	if !ok {
		m.fail(key, "expected duration string, got %T", v)
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		m.fail(key, "invalid duration: %v", err)
		return def
	}
	return d
}

// StringMap returns a map[string]string value (e.g. HTTP headers), or nil.
func (m *Map) StringMap(key string) map[string]string {
	if !m.Has(key) {
		return nil
	}
	raw, ok := m.raw[key].(map[string]any)
	if !ok {
		m.fail(key, "expected map, got %T", m.raw[key])
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		s, ok := val.(string)
		if !ok {
			m.fail(key, "value for %q must be a string, got %T", k, val)
			continue
		}
		out[k] = s
	}
	return out
}
