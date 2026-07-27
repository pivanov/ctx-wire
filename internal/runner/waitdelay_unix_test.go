//go:build !windows

package runner

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ctx-wire/internal/tee"
)

// shrinkWaitDelay makes the descendant-holds-stdout path fire in milliseconds
// instead of the production 3 seconds, so the regression tests below are fast
// and not timing-flaky.
func shrinkWaitDelay(t *testing.T, d time.Duration) {
	t.Helper()
	prev := WaitDelay
	WaitDelay = d
	t.Cleanup(func() { WaitDelay = prev })
}

// holdStdoutScript is a successful command that prints its real output, then
// leaves a descendant holding stdout open well past WaitDelay before exiting 0.
// This is the exact shape that used to be reported as a ctx-wire failure.
const holdStdoutScript = `echo real-output; sleep 5 & exit 0`

// TestErrWaitDelayIsNotAFailure pins the three things the bug destroyed: the
// exit code, the captured output, and the spool. A partial fix that only
// corrected the exit code would still fail this test.
func TestErrWaitDelayIsNotAFailure(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	shrinkWaitDelay(t, 150*time.Millisecond)
	reg := mustRegistry(t)

	stdoutText, _, _, code, err := runBuffered(
		context.Background(), reg, nil, "/bin/sh",
		[]string{"-c", holdStdoutScript},
		"sh -c hold", "sh -c hold", tee.NewSpool("waitdelay"))

	// 1. ctx-wire must not invent a failure.
	if err != nil {
		t.Fatalf("a successful command whose descendant held stdout must not produce a wrapper error, got %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (the command exited successfully)", code)
	}
	// 2. The command's own output must survive.
	if !strings.Contains(stdoutText, "real-output") {
		t.Errorf("captured output was discarded; stdout = %q, want it to contain %q", stdoutText, "real-output")
	}
}

// TestErrWaitDelaySignalsIncompleteIO proves the outcome is reported as a
// distinct signal rather than an error, so the runner can state precisely what
// was dropped instead of emitting a generic wrapper failure.
func TestErrWaitDelaySignalsIncompleteIO(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	shrinkWaitDelay(t, 150*time.Millisecond)

	outCap := &capWriter{max: maxCapture}
	code, incompleteIO, err := execChild(context.Background(), "/bin/sh",
		[]string{"-c", holdStdoutScript}, outCap, os.Stderr)
	if err != nil {
		t.Fatalf("execChild returned a wrapper error for a successful command: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !incompleteIO {
		t.Fatal("incompleteIO = false; the descendant held the pipe past WaitDelay, so the caller must be told what was dropped")
	}
	// 3. The captured output is still intact on this path too.
	if !strings.Contains(outCap.String(), "real-output") {
		t.Errorf("captured output = %q, want it to contain %q", outCap.String(), "real-output")
	}
}

// spooledBodies returns the contents of every spool file under dir. It inspects
// the real tee directory rather than calling Finalize directly, so the test
// asserts what the runner actually decided to retain instead of what the test
// asked for.
func spooledBodies(t *testing.T, dir string) []string {
	t.Helper()
	var bodies []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr == nil {
			bodies = append(bodies, string(b))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk tee dir: %v", err)
	}
	return bodies
}

func containsOutput(bodies []string, want string) bool {
	for _, b := range bodies {
		if strings.Contains(b, want) {
			return true
		}
	}
	return false
}

// TestErrWaitDelayRetainsSpoolBuffered proves the third casualty is repaired in
// the real buffered lifecycle: an incomplete-IO run exits 0 with no truncation,
// which previously meant Finalize(false) and a discarded spool. The captured
// bytes must stay recoverable on disk.
func TestErrWaitDelayRetainsSpoolBuffered(t *testing.T) {
	teeDir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", teeDir)
	shrinkWaitDelay(t, 150*time.Millisecond)
	reg := mustRegistry(t)

	_, _, hint, code, err := runBuffered(
		context.Background(), reg, nil, "/bin/sh",
		[]string{"-c", holdStdoutScript},
		"sh -c hold", "sh -c hold", tee.NewSpool("waitdelay-buffered"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !containsOutput(spooledBodies(t, teeDir), "real-output") {
		t.Error("the runner discarded the spool on an incomplete-IO run; captured output must stay recoverable")
	}
	// Retention must be silent: no fetch pointer, or a backgrounded descendant
	// would inflate the redemption counters the tuning work reads.
	if strings.Contains(hint, "ctx-wire fetch") {
		t.Errorf("incomplete-IO retention must not emit a fetch pointer, got hint %q", hint)
	}
	if !strings.Contains(hint, "remained open past the grace period") {
		t.Errorf("hint = %q, want it to state what was actually dropped", hint)
	}
}

// TestErrWaitDelayRetainsSpoolStreaming is the same assertion for the
// passthrough path, which keeps the spool only on failure or truncation.
func TestErrWaitDelayRetainsSpoolStreaming(t *testing.T) {
	teeDir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", teeDir)
	shrinkWaitDelay(t, 150*time.Millisecond)

	var stdout, stderr bytes.Buffer
	code, err := streamLive(context.Background(), "/bin/sh",
		[]string{"-c", holdStdoutScript}, "sh -c hold",
		tee.NewSpool("waitdelay-stream"), &stdout, &stderr, false)
	if err != nil {
		t.Fatalf("streamLive: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !containsOutput(spooledBodies(t, teeDir), "real-output") {
		t.Error("streamLive discarded the spool on an incomplete-IO run")
	}
	// The note goes to stderr, never stdout: injecting it into stdout would break
	// the byte-exactness this path exists to guarantee.
	if !strings.Contains(stderr.String(), "remained open past the grace period") {
		t.Errorf("stderr = %q, want the incomplete-IO note", stderr.String())
	}
	if strings.Contains(stdout.String(), "remained open past the grace period") {
		t.Error("the note leaked into stdout; the passthrough path must stay byte-exact")
	}
	if strings.Contains(stderr.String(), "ctx-wire fetch") {
		t.Errorf("incomplete-IO retention must be silent, got stderr %q", stderr.String())
	}
}

// TestErrWaitDelayOnlyOnSuccess is the safety proof the fix rests on: a real
// failure must never be reclassified as incomplete I/O. Go guarantees it (an
// *ExitError from a nonzero exit wins over ErrWaitDelay); this pins it against
// a future refactor that reorders runAndExitCode's error handling.
func TestErrWaitDelayOnlyOnSuccess(t *testing.T) {
	shrinkWaitDelay(t, 150*time.Millisecond)

	// A FAILING command that also leaves a descendant holding stdout.
	outCap := &capWriter{max: maxCapture}
	code, incompleteIO, err := execChild(context.Background(), "/bin/sh",
		[]string{"-c", `echo out; sleep 5 & exit 3`}, outCap, os.Stderr)
	if err != nil {
		t.Fatalf("a nonzero exit is a command failure, not a wrapper error: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3: a real failure must keep its status", code)
	}
	if incompleteIO {
		t.Error("incompleteIO = true for a command that exited 3; a genuine failure must never be reclassified as incomplete I/O")
	}
}

// TestCancelledContextIsNotIncompleteIO covers the other half of the guarantee:
// os/exec returns ErrWaitDelay only when no Cancel call has occurred, and
// ctx-wire sets cmd.Cancel, so a cancelled context must not surface as
// incomplete I/O either.
func TestCancelledContextIsNotIncompleteIO(t *testing.T) {
	shrinkWaitDelay(t, 150*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	outCap := &capWriter{max: maxCapture}
	_, incompleteIO, _ := execChild(ctx, "/bin/sh",
		[]string{"-c", `sleep 5`}, outCap, os.Stderr)
	if incompleteIO {
		t.Error("incompleteIO = true for a cancelled context; cancellation is not a WaitDelay expiry")
	}
}

// TestRunAndExitCodeWaitDelayReportsRealStatus checks the unit directly: the
// error is surfaced for classification, and the exit status comes from the
// completed ProcessState rather than the hardcoded 1 the bug used.
func TestRunAndExitCodeWaitDelayReportsRealStatus(t *testing.T) {
	shrinkWaitDelay(t, 150*time.Millisecond)

	cmd := newCommand(context.Background(), "/bin/sh", "-c", holdStdoutScript)
	cmd.Stdout = &capWriter{max: maxCapture}
	cmd.Stderr = os.Stderr

	code, err := runAndExitCode(cmd)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected ErrWaitDelay to be surfaced for classification, got %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 from the completed ProcessState (not the old hardcoded 1)", code)
	}
}
