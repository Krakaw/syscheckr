package report

import (
	"path/filepath"
	"testing"
)

// TestSpecsCoverReporterTypes ensures every registered reporter type has a spec
// and can be built from it — catching a required field that drifted out of sync
// with its factory.
func TestSpecsCoverReporterTypes(t *testing.T) {
	for _, typ := range Types() {
		fields, ok := Specs[typ]
		if !ok {
			t.Errorf("reporter type %q has no entry in Specs", typ)
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
		// linear opens a state store on construction; keep it out of the repo.
		if typ == "linear" {
			cfg["state_path"] = filepath.Join(t.TempDir(), "state.json")
		}
		if _, err := New(typ, "t", cfg); err != nil {
			t.Errorf("building %q from its spec failed (spec drifted from factory?): %v", typ, err)
		}
	}
}

func dummyValue(key string) string {
	if key == "url" || key == "webhook_url" {
		return "http://example.test/hook"
	}
	return "x"
}
