package selfupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	replaceHelperEnv = "CTX_WIRE_REPLACE_HELPER"
	replaceMarker    = "NEW-CTX-WIRE-IMAGE-AFTER-SWAP"
)

// TestReplaceSelfHelper is the subprocess body. Running from a copied binary, it
// calls the real replaceSelf on its own os.Executable() while it is still
// executing, then exits. The env guard keeps it inert during a normal test run.
func TestReplaceSelfHelper(t *testing.T) {
	if os.Getenv(replaceHelperEnv) != "1" {
		t.Skip("helper subprocess only")
	}
	if err := replaceSelf([]byte(replaceMarker)); err != nil {
		t.Fatalf("replaceSelf on live process: %v", err)
	}
}

// TestReplaceSelfLiveProcess proves the real replaceSelf swaps a genuinely
// running executable, not just a static file. It copies this test binary to a
// temp dir, runs it as the helper (which calls replaceSelf on itself while
// live), and asserts the on-disk image was replaced. This is the Windows
// running-exe constraint (you cannot overwrite a running .exe, only rename it);
// the rename path also works on Unix, so the test runs everywhere.
func TestReplaceSelfLiveProcess(t *testing.T) {
	if os.Getenv(replaceHelperEnv) == "1" {
		return // we are the helper; the parent drives the assertions
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "helper")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper, data, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(helper, "-test.run=^TestReplaceSelfHelper$", "-test.v")
	cmd.Env = append(os.Environ(), replaceHelperEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper subprocess failed: %v\n%s", err, out)
	}

	got, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != replaceMarker {
		t.Errorf("on-disk image after swap was not replaced: got %d bytes (%q...), want the new marker",
			len(got), string(got[:min(len(got), 32)]))
	}
}
