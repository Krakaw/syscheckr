package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	cases := map[string]string{
		"v0.1.3": "v0.1.3",
		"0.1.3":  "v0.1.3",
	}
	for in, want := range cases {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		tag, ver string
		want     bool
	}{
		{"v0.1.3", "0.1.3", true},  // tag is v-prefixed, version is not
		{"0.1.3", "v0.1.3", true},  // and the reverse
		{"v0.1.3", "0.1.4", false}, // different patch
		{"v0.1.3", "dev", false},   // dev build is never "current"
	}
	for _, c := range cases {
		if got := sameVersion(c.tag, c.ver); got != c.want {
			t.Errorf("sameVersion(%q, %q) = %v, want %v", c.tag, c.ver, got, c.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc123  syscheckr-v0.1.3-linux-amd64.tar.gz\n" +
		"def456  syscheckr-v0.1.3-linux-arm64.tar.gz\n"

	if got, ok := checksumFor(sums, "syscheckr-v0.1.3-linux-arm64.tar.gz"); !ok || got != "def456" {
		t.Errorf("checksumFor(arm64) = %q, %v; want def456, true", got, ok)
	}
	if _, ok := checksumFor(sums, "syscheckr-v0.1.3-darwin-amd64.tar.gz"); ok {
		t.Error("checksumFor(absent asset) = ok; want not found")
	}
}

func TestExtractBinary(t *testing.T) {
	const want = "#!/fake/elf\x00binary-bytes"
	const path = "syscheckr-v0.1.3-linux-amd64/syscheckr"

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
	}{
		{"syscheckr-v0.1.3-linux-amd64/README", "noise"},
		{path, want},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractBinary(buf.Bytes(), path)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if string(got) != want {
		t.Errorf("extractBinary = %q, want %q", got, want)
	}

	if _, err := extractBinary(buf.Bytes(), "syscheckr-v0.1.3-linux-amd64/missing"); err == nil {
		t.Error("extractBinary(missing) = nil error; want not-found error")
	}
}
