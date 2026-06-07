package runner

import (
	"context"
	"os"
	"runtime"
	"testing"
)

// TestRunRawInheritsStdin proves byte-exact passthrough forwards piped stdin.
// The canonical case is a statusline doing `input=$(cat)`: if RunRaw drops stdin,
// the consumer reads nothing. The matcher exits 0 only if it actually saw the
// piped input, so the exit code alone proves inheritance (no output parsing, no
// line-ending pitfalls). Runs on every OS.
func TestRunRawInheritsStdin(t *testing.T) {
	name, args := "grep", []string{"hello"}
	if runtime.GOOS == "windows" {
		name, args = "findstr", []string{"hello"}
	}

	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = wIn.WriteString("hello world\n"); _ = wIn.Close() }()

	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = rIn, devnull
	code := RunRaw(context.Background(), name, args)
	os.Stdin, os.Stdout = oldIn, oldOut

	if code != 0 {
		t.Errorf("matcher exit=%d, want 0: stdin was not inherited by the child", code)
	}
}

func TestRunRawPropagatesExitCode(t *testing.T) {
	name, args := "sh", []string{"-c", "exit 7"}
	if runtime.GOOS == "windows" {
		name, args = "cmd", []string{"/c", "exit 7"}
	}
	if code := RunRaw(context.Background(), name, args); code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}
