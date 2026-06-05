// Package selfupdate implements `ctx-wire update`: an explicit, opt-in upgrade
// that downloads the latest release binary from public GitHub Releases, verifies
// its SHA-256 checksum, and atomically replaces the running binary with a
// backup-and-rollback. It never checks automatically and never phones home on
// its own; it runs only when the user asks.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the public GitHub repository releases are pulled from.
const Repo = "pivanov/ctx-wire"

// maxDownload bounds a release asset so a bad/huge response cannot exhaust
// memory (a ctx-wire archive is a few MB).
const maxDownload = 64 << 20

// Options controls an update run.
type Options struct {
	Current string // the running binary's version (e.g. "0.1.0" or "dev")
	Check   bool   // report whether an update exists without applying it
}

// Result describes the outcome.
type Result struct {
	Current  string
	Latest   string
	UpToDate bool // already on the latest release
	Updated  bool // a newer binary was installed
}

// httpGet is the HTTP fetcher, a var so tests can stub the network.
var httpGet = realHTTPGet

func realHTTPGet(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ctx-wire-update")
	req.Header.Set("Accept", "application/octet-stream, application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

// Update checks the latest release and, unless Check is set, upgrades the running
// binary to it. It is a no-op (UpToDate) when already current.
func Update(opts Options) (*Result, error) {
	latest, err := LatestVersion()
	if err != nil {
		return nil, err
	}
	res := &Result{Current: opts.Current, Latest: latest}
	if !isNewer(opts.Current, latest) {
		res.UpToDate = true
		return res, nil
	}
	if opts.Check {
		return res, nil
	}

	version := strings.TrimPrefix(latest, "v")
	asset := fmt.Sprintf("ctx-wire_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", Repo, latest)

	archive, err := httpGet(base + asset)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset, err)
	}
	sumFile, err := httpGet(base + asset + ".sha256")
	if err != nil {
		return nil, fmt.Errorf("download checksum: %w", err)
	}
	if err := verifyChecksum(archive, sumFile); err != nil {
		return nil, err
	}
	bin, err := extractBinary(archive)
	if err != nil {
		return nil, err
	}
	if err := replaceSelf(bin); err != nil {
		return nil, err
	}
	res.Updated = true
	return res, nil
}

// LatestVersion returns the latest release tag (e.g. "v0.1.0").
func LatestVersion() (string, error) {
	data, err := httpGet(fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", Repo))
	if err != nil {
		return "", fmt.Errorf("could not reach GitHub releases (is %s public with a release?): %w", Repo, err)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		return "", fmt.Errorf("parse release metadata: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no published release found for %s", Repo)
	}
	return rel.TagName, nil
}

// verifyChecksum confirms archive matches the SHA-256 in a shasum-style file
// ("<hex>  <name>").
func verifyChecksum(archive, sumFile []byte) error {
	want := strings.TrimSpace(string(sumFile))
	if i := strings.IndexAny(want, " \t"); i >= 0 {
		want = want[:i]
	}
	want = strings.ToLower(want)
	if len(want) != 64 {
		return fmt.Errorf("malformed checksum file")
	}
	sum := sha256.Sum256(archive)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("checksum mismatch: archive may be corrupt or tampered (want %s, got %s)", want, got)
	}
	return nil
}

// extractBinary returns the ctx-wire executable bytes from a release .tar.gz.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
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
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base == "ctx-wire" || base == "ctx-wire.exe" {
			data, err := io.ReadAll(io.LimitReader(tr, maxDownload))
			if err != nil {
				return nil, err
			}
			if len(data) == 0 {
				return nil, fmt.Errorf("archive contained an empty binary")
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("ctx-wire binary not found in the release archive")
}

// replaceSelf atomically swaps the running binary for newBin, keeping a backup
// to roll back on failure. The new file is written in the target's own directory
// so the rename is atomic on the same filesystem. Renaming a running binary is
// safe on Unix and Windows; the live process keeps the old image. It is a var so
// tests can stub the destructive replace.
var replaceSelf = func(newBin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)

	tmp, err := os.CreateTemp(dir, ".ctx-wire-update-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (try re-running the installer, or with sudo): %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	bak := exe + ".bak"
	if err := os.Rename(exe, bak); err != nil {
		return fmt.Errorf("back up current binary: %w", err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		_ = os.Rename(bak, exe) // roll back
		return fmt.Errorf("install new binary: %w", err)
	}
	_ = os.Remove(bak) // best-effort cleanup
	return nil
}

// isNewer reports whether latest is a newer release than current. A non-release
// current ("dev" or unparseable) is always treated as older so an update is
// offered.
func isNewer(current, latest string) bool {
	return compareVersions(current, latest) < 0
}

// compareVersions compares two dotted versions (leading "v" ignored). It returns
// -1 if a < b, 0 if equal, 1 if a > b. An unparseable/"dev" version sorts below
// any real version.
func compareVersions(a, b string) int {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 { // drop pre-release/build metadata
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || parts[0] == "" {
		return [3]int{}, false
	}
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}
