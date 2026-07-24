package check

import "testing"

// TestSpecsCoverCheckTypes ensures every registered check type has a spec and
// that a check can be built from that spec — which fails if the factory grew a
// required field the spec doesn't list.
func TestSpecsCoverCheckTypes(t *testing.T) {
	for _, typ := range Types() {
		fields, ok := Specs[typ]
		if !ok {
			t.Errorf("check type %q has no entry in Specs", typ)
			continue
		}
		cfg := map[string]any{}
		for _, f := range fields {
			switch {
			case f.Required:
				cfg[f.Key] = dummyValue(f.Key)
			case f.Default != nil:
				cfg[f.Key] = f.Default
			}
		}
		if _, err := New(typ, "t", cfg); err != nil {
			t.Errorf("building %q from its spec failed (spec drifted from factory?): %v", typ, err)
		}
	}
}

func dummyValue(key string) string {
	if key == "url" {
		return "http://example.test"
	}
	return "x"
}
