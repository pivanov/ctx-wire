// Package selfupdate upgrades ctx-wire by downloading the latest release binary
// from public GitHub Releases, verifying its SHA-256 checksum, and atomically
// replacing the running binary with a backup-and-rollback.
//
// It runs in two ways: the explicit `ctx-wire update` command, and an opt-out
// background updater (MaybeBackgroundUpdate) that checks at most a few times a
// day on human-facing commands and applies any newer release in a detached
// process. The background path is disabled by `[update] auto = false` in config
// or the CTX_WIRE_NO_AUTOUPDATE env var, and never runs for dev builds.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
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
// memory (a ctx-wire archive is a few MB). A var so tests can shrink it.
var maxDownload = 64 << 20

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
	return io.ReadAll(io.LimitReader(resp.Body, int64(maxDownload)))
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
	// Windows releases ship as a .zip with ctx-wire.exe; every other platform a
	// .tar.gz. extractBinary dispatches on the archive format, and replaceSelf's
	// rename-based swap works on a running binary on both Unix and Windows.
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	asset := fmt.Sprintf("ctx-wire_%s_%s_%s.%s", version, runtime.GOOS, runtime.GOARCH, ext)
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

// extractBinary returns the ctx-wire executable bytes from a release archive,
// dispatching on format: a Windows .zip (PK magic) or a .tar.gz for every other
// platform.
func extractBinary(archive []byte) ([]byte, error) {
	if len(archive) >= 2 && archive[0] == 'P' && archive[1] == 'K' {
		return extractBinaryZip(archive)
	}
	return extractBinaryTarGz(archive)
}

func extractBinaryTarGz(archive []byte) ([]byte, error) {
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
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if isCtxWireBinary(hdr.Name) {
			return readArchiveEntry(io.LimitReader(tr, int64(maxDownload+1)))
		}
	}
	return nil, fmt.Errorf("ctx-wire binary not found in the release archive")
}

func extractBinaryZip(archive []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	for _, f := range zr.File {
		if !isCtxWireBinary(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		data, err := readArchiveEntry(io.LimitReader(rc, int64(maxDownload+1)))
		rc.Close()
		return data, err
	}
	return nil, fmt.Errorf("ctx-wire binary not found in the release archive")
}

func isCtxWireBinary(name string) bool {
	base := filepath.Base(name)
	return base == "ctx-wire" || base == "ctx-wire.exe"
}

// readArchiveEntry reads r (a LimitReader capped at maxDownload+1) fully and
// rejects both an empty entry and one that hits the cap, so a truncated binary
// is never installed.
func readArchiveEntry(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("archive contained an empty binary")
	}
	if len(data) > maxDownload {
		return nil, fmt.Errorf("binary in archive exceeds %d bytes; refusing a possibly-truncated install", maxDownload)
	}
	return data, nil
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

// compareVersions compares two versions (leading "v" ignored). It returns -1 if
// a < b, 0 if equal, 1 if a > b. An unparseable/"dev" version sorts below any
// real version. Equal numeric cores are then ordered by pre-release per semver:
// a stable release outranks any pre-release of the same core (so 0.1.2-rc1 <
// 0.1.2), which is what lets an RC dogfood roll forward to the stable release.
func compareVersions(a, b string) int {
	pa, prea, oka := parseVersion(a)
	pb, preb, okb := parseVersion(b)
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
	return comparePre(prea, preb)
}

// comparePre orders two pre-release identifiers by semver precedence. The empty
// identifier (a normal release) ranks ABOVE any pre-release, so 1.2.3 > 1.2.3-rc1.
// Otherwise each dot-separated field is compared: numeric fields numerically,
// alphanumeric fields lexically, with a numeric field ranking below an
// alphanumeric one; when one identifier is a prefix of the other, the longer one
// wins. (Single-token identifiers like "rc10" compare lexically per semver; use
// a dotted "rc.10" if you need numeric ordering past rc9.)
func comparePre(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1 // stable outranks a pre-release
	}
	if b == "" {
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aNum := numericID(as[i])
		bn, bNum := numericID(bs[i])
		switch {
		case aNum && bNum:
			if an != bn {
				return sign(an - bn)
			}
		case aNum:
			return -1 // numeric identifiers rank below alphanumeric ones
		case bNum:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	return sign(len(as) - len(bs)) // a longer identifier set outranks its prefix
}

// numericID reports whether a pre-release field is a non-negative integer and
// returns its value.
func numericID(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// parseVersion splits a version into its numeric [major, minor, patch] core and
// its pre-release identifier (the text after "-", before any "+" build metadata,
// which semver ignores for precedence). ok is false for a "dev" or otherwise
// unparseable version. Examples: "v0.1.2" -> {0,1,2},"" ; "0.1.2-rc1" -> {0,1,2},"rc1".
func parseVersion(v string) (core [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 { // build metadata never affects precedence
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 { // split off the pre-release identifier
		pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || parts[0] == "" {
		return [3]int{}, "", false
	}
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, "", false
		}
		core[i] = n
	}
	return core, pre, true
}
