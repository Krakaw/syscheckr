package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Krakaw/syscheckr/internal/version"
	"github.com/spf13/cobra"
)

// ghRepo is the owner/name slug used to resolve releases and download assets.
// Mirrors REPO in scripts/install.sh so both install paths stay in sync.
const ghRepo = "Krakaw/syscheckr"

func updateCmd() *cobra.Command {
	var tag string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Download the latest release and replace this binary in place",
		Long: "Resolves the latest published release (or --version), downloads the build for this " +
			"OS/arch, verifies its checksum, and atomically overwrites the running executable.\n\n" +
			"If syscheckr lives in a root-owned directory (e.g. /usr/local/bin), re-run with sudo.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd.Context(), os.Stdout, tag)
		},
	}
	cmd.Flags().StringVar(&tag, "version", "", "release tag to install (e.g. v0.1.3); default: latest")
	return cmd
}

// runUpdate performs the self-update: resolve tag -> download -> verify -> swap.
func runUpdate(ctx context.Context, out io.Writer, wantTag string) error {
	client := &http.Client{Timeout: 60 * time.Second}

	osName, arch := runtime.GOOS, runtime.GOARCH

	resolved, err := resolveTag(ctx, client, wantTag)
	if err != nil {
		return err
	}

	// An explicit --version is a deliberate (re)install — e.g. repairing a
	// corrupted binary — so only short-circuit when resolving the latest.
	if wantTag == "" && sameVersion(resolved, version.Version) {
		fmt.Fprintf(out, "Already on %s — nothing to do.\n", resolved)
		return nil
	}

	asset := fmt.Sprintf("syscheckr-%s-%s-%s.tar.gz", resolved, osName, arch)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s", ghRepo, resolved)

	fmt.Fprintf(out, "Updating syscheckr %s -> %s (%s/%s)\n", version.Version, resolved, osName, arch)

	archive, err := download(ctx, client, base+"/"+asset)
	if err != nil {
		return fmt.Errorf("download %s: %w (does release %s have a %s/%s build?)", asset, err, resolved, osName, arch)
	}

	if err := verifyChecksum(ctx, client, out, base+"/checksums.txt", asset, archive); err != nil {
		return err
	}

	bin, err := extractBinary(archive, fmt.Sprintf("syscheckr-%s-%s-%s/syscheckr", resolved, osName, arch))
	if err != nil {
		return err
	}

	dest, err := replaceSelf(bin)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Updated %s to %s\n", dest, resolved)
	return nil
}

// resolveTag returns the explicit tag (normalized with a leading "v") when one
// is given, otherwise queries the GitHub API for the latest release tag.
func resolveTag(ctx context.Context, client *http.Client, wantTag string) (string, error) {
	if wantTag != "" {
		return normalizeTag(wantTag), nil
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", ghRepo)
	body, err := download(ctx, client, url)
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("parse latest release: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("could not determine the latest release tag")
	}
	return rel.TagName, nil
}

// verifyChecksum confirms the archive's SHA-256 matches the entry for asset in
// checksums.txt. A missing checksums.txt is a best-effort skip (warned), mirroring
// scripts/install.sh; a present-but-mismatched checksum is a hard failure.
func verifyChecksum(ctx context.Context, client *http.Client, out io.Writer, url, asset string, archive []byte) error {
	sums, err := download(ctx, client, url)
	if err != nil {
		fmt.Fprintf(out, "warning: checksums.txt not found — skipping verification\n")
		return nil
	}
	want, ok := checksumFor(string(sums), asset)
	if !ok {
		fmt.Fprintf(out, "warning: %s not listed in checksums.txt — skipping verification\n", asset)
		return nil
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum verification failed for %s: got %s, want %s", asset, got, want)
	}
	fmt.Fprintln(out, "Checksum OK")
	return nil
}

// checksumFor finds the hex digest for asset in `sha256sum`-style output
// (lines of "<hex>  <filename>").
func checksumFor(sums, asset string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == asset {
			return fields[0], true
		}
	}
	return "", false
}

// extractBinary reads the gzip+tar archive and returns the bytes of the entry
// at wantPath (the syscheckr executable inside the release directory).
func extractBinary(archive []byte, wantPath string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if filepath.Clean(hdr.Name) == filepath.Clean(wantPath) {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("read binary from archive: %w", err)
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", wantPath)
}

// replaceSelf atomically overwrites the running executable with bin and returns
// the path that was replaced. It writes to a temp file in the same directory so
// the final os.Rename is atomic on the same filesystem; on Linux a running
// binary can be replaced this way without disturbing the live process.
func replaceSelf(bin []byte) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".syscheckr-update-*")
	if err != nil {
		return "", fmt.Errorf("cannot write to %s (try re-running with sudo): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close new binary: %w", err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		return "", fmt.Errorf("replace %s (try re-running with sudo): %w", exe, err)
	}
	return exe, nil
}

// download GETs url and returns the response body, failing on non-2xx status.
func download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// normalizeTag ensures a release tag carries the leading "v" used by tags/assets.
func normalizeTag(tag string) string {
	if strings.HasPrefix(tag, "v") {
		return tag
	}
	return "v" + tag
}

// sameVersion reports whether a release tag and a build version string refer to
// the same release, ignoring a leading "v" on either side.
func sameVersion(tag, ver string) bool {
	return strings.TrimPrefix(tag, "v") == strings.TrimPrefix(ver, "v")
}
