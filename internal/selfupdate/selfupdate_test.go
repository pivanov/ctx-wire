package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"v1.2.3", "1.2.4", -1}, // leading v ignored
		{"1.2.0", "1.10.0", -1}, // numeric, not lexical
		{"2.0.0", "1.9.9", 1},
		{"dev", "0.1.0", -1},           // dev sorts below any release
		{"0.1.0", "dev", 1},            // and vice versa
		{"1.2.3-rc1", "1.2.3", -1},     // an RC is older than its stable release
		{"1.2.3", "1.2.3-rc1", 1},      // and stable is newer than the RC
		{"0.1.2-rc1", "0.1.2-rc2", -1}, // RCs order by identifier
		{"0.1.2-rc2", "0.1.2-rc1", 1},  //
		{"1.2.3-rc1", "1.2.3-rc1", 0},  // equal pre-releases
		{"1.2.3+build", "1.2.3", 0},    // build metadata ignored for precedence
		{"1.2.4-rc1", "1.2.3", 1},      // a higher core wins regardless of pre-release
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	if !isNewer("dev", "v0.1.0") {
		t.Error("dev should be older than any release")
	}
	if isNewer("1.0.0", "1.0.0") {
		t.Error("same version is not newer")
	}
	if !isNewer("1.0.0", "1.0.1") {
		t.Error("1.0.1 should be newer than 1.0.0")
	}
	if !isNewer("0.1.2-rc1", "0.1.2") {
		t.Error("an RC dogfood should roll forward to its stable release")
	}
	if isNewer("0.1.2", "0.1.2-rc1") {
		t.Error("a stable release must not downgrade to an RC")
	}
}

func TestVerifyChecksum(t *testing.T) {
	archive := []byte("pretend-this-is-a-tarball")
	sum := sha256.Sum256(archive)
	good := hex.EncodeToString(sum[:]) + "  ctx-wire_1.0.0_linux_amd64.tar.gz\n"
	if err := verifyChecksum(archive, []byte(good)); err != nil {
		t.Errorf("good checksum rejected: %v", err)
	}
	bad := strings.Repeat("0", 64) + "  x\n"
	if err := verifyChecksum(archive, []byte(bad)); err == nil {
		t.Error("checksum mismatch should be rejected")
	}
	if err := verifyChecksum(archive, []byte("not-a-checksum")); err == nil {
		t.Error("malformed checksum should be rejected")
	}
}

// makeArchive builds a release-shaped .tar.gz containing the binary at
// <dir>/ctx-wire, matching what pack.sh produces.
func makeArchive(t *testing.T, dir string, bin []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// a noise file alongside the binary, to prove we pick the right one
	writeTar(t, tw, dir+"/README.md", []byte("readme"))
	writeTar(t, tw, dir+"/ctx-wire", bin)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTar(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
}

func TestExtractBinary(t *testing.T) {
	want := []byte("BINARY-CONTENTS")
	archive := makeArchive(t, "ctx-wire_1.0.0_linux_amd64", want)
	got, err := extractBinary(archive)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}

	// An archive without the binary is an error, not a silent empty result.
	noBin := makeArchive(t, "x", nil) // ctx-wire entry has zero bytes
	if _, err := extractBinary(noBin); err == nil {
		t.Error("empty binary should be rejected")
	}
}

// TestExtractBinaryZip covers the Windows release format: a .zip carrying
// ctx-wire.exe. extractBinary dispatches on the PK magic.
func TestExtractBinaryZip(t *testing.T) {
	want := []byte("WINDOWS-BINARY-CONTENTS")
	archive := makeZip(t, "ctx-wire_1.0.0_windows_amd64/ctx-wire.exe", want)
	got, err := extractBinary(archive)
	if err != nil {
		t.Fatalf("extractBinary(zip): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}

	// A zip without the binary is an error.
	noBin := makeZip(t, "ctx-wire_1.0.0_windows_amd64/README.md", []byte("readme"))
	if _, err := extractBinary(noBin); err == nil {
		t.Error("zip without ctx-wire.exe should be rejected")
	}
}

// TestExtractBinaryRejectsOversize ensures a binary larger than the cap is
// refused, not silently truncated and installed.
func TestExtractBinaryRejectsOversize(t *testing.T) {
	defer func(old int) { maxDownload = old }(maxDownload)
	maxDownload = 1 << 10 // 1 KiB
	big := bytes.Repeat([]byte("A"), maxDownload+512)
	archive := makeArchive(t, "ctx-wire_1.0.0_linux_amd64", big)
	if _, err := extractBinary(archive); err == nil {
		t.Error("oversize binary should be rejected, not truncated")
	}
}

// makeZip builds a .zip with a single entry, matching what pack.sh produces for
// Windows targets.
func makeZip(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUpdateUpToDate(t *testing.T) {
	restore := stub(t, `{"tag_name":"v1.0.0"}`, nil, nil)
	defer restore()
	res, err := Update(Options{Current: "1.0.0"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !res.UpToDate || res.Updated {
		t.Errorf("expected up-to-date, got %+v", res)
	}
}

func TestUpdateCheckReportsWithoutApplying(t *testing.T) {
	applied := false
	prev := replaceSelf
	replaceSelf = func([]byte) error { applied = true; return nil }
	defer func() { replaceSelf = prev }()
	restore := stub(t, `{"tag_name":"v2.0.0"}`, nil, nil)
	defer restore()

	res, err := Update(Options{Current: "1.0.0", Check: true})
	if err != nil {
		t.Fatalf("Update --check: %v", err)
	}
	if res.UpToDate || res.Updated {
		t.Errorf("--check should not apply: %+v", res)
	}
	if res.Latest != "v2.0.0" {
		t.Errorf("Latest = %q, want v2.0.0", res.Latest)
	}
	if applied {
		t.Error("--check must not replace the binary")
	}
}

func TestUpdateApplies(t *testing.T) {
	binWant := []byte("NEW-CTX-WIRE-BINARY")
	dir := fmt.Sprintf("ctx-wire_2.0.0_%s_%s", runtime.GOOS, runtime.GOARCH)
	archive := makeArchive(t, dir, binWant)
	sum := sha256.Sum256(archive)
	checksum := []byte(hex.EncodeToString(sum[:]) + "  " + dir + ".tar.gz\n")

	var got []byte
	prev := replaceSelf
	replaceSelf = func(b []byte) error { got = b; return nil }
	defer func() { replaceSelf = prev }()
	restore := stub(t, `{"tag_name":"v2.0.0"}`, archive, checksum)
	defer restore()

	res, err := Update(Options{Current: "1.0.0"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !res.Updated || res.Latest != "v2.0.0" {
		t.Errorf("expected applied update to v2.0.0, got %+v", res)
	}
	if !bytes.Equal(got, binWant) {
		t.Errorf("installed bytes = %q, want %q", got, binWant)
	}
}

func TestUpdateRejectsBadChecksum(t *testing.T) {
	dir := fmt.Sprintf("ctx-wire_2.0.0_%s_%s", runtime.GOOS, runtime.GOARCH)
	archive := makeArchive(t, dir, []byte("payload"))
	badSum := []byte(strings.Repeat("a", 64) + "  x\n")
	prev := replaceSelf
	replaceSelf = func([]byte) error { t.Fatal("must not replace on checksum failure"); return nil }
	defer func() { replaceSelf = prev }()
	restore := stub(t, `{"tag_name":"v2.0.0"}`, archive, badSum)
	defer restore()

	if _, err := Update(Options{Current: "1.0.0"}); err == nil {
		t.Error("expected a checksum error")
	}
}

// stub installs an httpGet that returns the release JSON for the API URL, the
// archive for the .tar.gz, and the checksum for the .sha256.
func stub(t *testing.T, releaseJSON string, archive, checksum []byte) func() {
	t.Helper()
	prev := httpGet
	httpGet = func(url string) ([]byte, error) {
		switch {
		case strings.Contains(url, "api.github.com"):
			return []byte(releaseJSON), nil
		case strings.HasSuffix(url, ".sha256"):
			return checksum, nil
		case strings.HasSuffix(url, ".tar.gz"):
			return archive, nil
		}
		return nil, fmt.Errorf("unexpected url %q", url)
	}
	return func() { httpGet = prev }
}
