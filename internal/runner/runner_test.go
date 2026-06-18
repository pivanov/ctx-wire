package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/tee"
)

// TestMain disables gain recording so runner tests never write to the real
// telemetry log on the developer's machine.
func TestMain(m *testing.M) {
	os.Setenv("CTX_WIRE_GAIN", "0")
	os.Exit(m.Run())
}

func TestShouldBypass(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		{"editor bypassed", "vim", []string{"file.txt"}, true},
		{"pager bypassed", "less", []string{"log"}, true},
		{"full path editor bypassed", "/usr/bin/nano", nil, true},
		{"follow flag bypassed", "tail", []string{"-f", "app.log"}, true},
		{"docker logs follow bypassed", "docker", []string{"logs", "--follow", "c1"}, true},
		{"watch flag bypassed", "kubectl", []string{"get", "pods", "-w"}, true},
		{"normal build captured", "dotnet", []string{"build"}, false},
		{"normal test captured", "go", []string{"test", "./..."}, false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldBypass(tt.cmd, tt.args); got != tt.want {
				t.Errorf("shouldBypass(%q, %v) = %v, want %v", tt.cmd, tt.args, got, tt.want)
			}
		})
	}
}

func TestRunBufferedPassthroughScrubs(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	out, _, _, code, err := runBuffered(context.Background(), reg, reg.Find("printf ..."), "printf",
		[]string{"deploy token ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa done"},
		"printf ...", "printf ...", tee.NewSpool("printf"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if strings.Contains(out, "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Errorf("token leaked: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected redaction: %q", out)
	}
}

func TestRunBufferedPropagatesExitCode(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	_, _, _, code, err := runBuffered(context.Background(), reg, reg.Find("sh -c exit 7"), "sh",
		[]string{"-c", "exit 7"}, "sh -c exit 7", "sh -c exit 7", tee.NewSpool("sh"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

func TestRunBufferedDoesNotInventOnEmptyOKOnFailure(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	out, errOut, _, code, err := runBuffered(context.Background(), reg, reg.Find("bun lint"), "sh",
		[]string{"-c", "echo 'error: lint failed' >&2; exit 1"},
		"bun lint", "bun lint", tee.NewSpool("bun lint"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	combined := out + errOut
	if strings.Contains(combined, "bun: ok") {
		t.Fatalf("failure output must not contain synthetic ok: %q", combined)
	}
	if !strings.Contains(combined, "error: lint failed") {
		t.Fatalf("failure output lost stderr: %q", combined)
	}
}

func TestRunBufferedDoesNotUseSuccessMatchOnFailure(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	out, errOut, _, code, err := runBuffered(context.Background(), reg, reg.Find("bun build"), "sh",
		[]string{"-c", "echo '✓ built in 1.23s'; exit 1"},
		"bun build", "bun build", tee.NewSpool("bun build"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	combined := out + errOut
	if strings.Contains(combined, "bun: ok") {
		t.Fatalf("failure output must not use success match_output: %q", combined)
	}
	if !strings.Contains(combined, "✓ built in 1.23s") {
		t.Fatalf("failure output lost stdout: %q", combined)
	}
}

// pythonTrace has three consecutive site-packages frames (a collapsible run)
// between the app frame and the error message.
const pythonTrace = "Traceback (most recent call last):\n" +
	"  File \"/app/main.py\", line 10, in <module>\n" +
	"    run()\n" +
	"  File \"/usr/lib/python3.11/site-packages/requests/api.py\", line 59, in get\n" +
	"    return request()\n" +
	"  File \"/usr/lib/python3.11/site-packages/requests/sessions.py\", line 587, in request\n" +
	"    resp = self.send(prep)\n" +
	"  File \"/usr/lib/python3.11/site-packages/urllib3/connectionpool.py\", line 790, in urlopen\n" +
	"    raise MaxRetryError()\n" +
	"ConnectionError: boom\n"

func writeTrace(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "trace.txt")
	if err := os.WriteFile(p, []byte(pythonTrace), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunBufferedStripsStacktraceOnStdout(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_STRIP_STACKTRACES", "1")
	reg := mustRegistry(t)
	p := writeTrace(t)
	out, _, hint, _, err := runBuffered(context.Background(), reg, reg.Find("go test ./..."), "cat",
		[]string{p}, "go test ./...", "go test ./...", tee.NewSpool("go"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if !strings.Contains(out, "library frames hidden") {
		t.Fatalf("stdout trace not collapsed:\n%s", out)
	}
	if strings.Contains(out, "site-packages") {
		t.Fatalf("library frames leaked on stdout:\n%s", out)
	}
	if !strings.Contains(out, "/app/main.py") {
		t.Fatalf("app frame must be kept:\n%s", out)
	}
	if hint == "" {
		t.Errorf("expected a spool/recovery hint after a collapse")
	}
}

func TestRunBufferedStripsStacktraceOnUnmergedStderr(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	t.Setenv("CTX_WIRE_STRIP_STACKTRACES", "1")
	reg := mustRegistry(t)
	p := writeTrace(t)
	// The go filter does not set filter_stderr, so stderr is stripped on its own
	// path (the case that regressed before stripstack was applied to stderr too).
	_, errOut, _, _, err := runBuffered(context.Background(), reg, reg.Find("go test ./..."), "sh",
		[]string{"-c", "cat '" + p + "' >&2"}, "go test ./...", "go test ./...", tee.NewSpool("go"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if !strings.Contains(errOut, "library frames hidden") {
		t.Fatalf("stderr trace not collapsed:\n%s", errOut)
	}
	if strings.Contains(errOut, "site-packages") {
		t.Fatalf("library frames leaked on stderr:\n%s", errOut)
	}
	if !strings.Contains(errOut, "/app/main.py") {
		t.Fatalf("app frame must be kept on stderr:\n%s", errOut)
	}
}

func TestRunBufferedStacktraceStrippingOffByDefault(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	// CTX_WIRE_STRIP_STACKTRACES unset -> opt-in feature stays off.
	reg := mustRegistry(t)
	p := writeTrace(t)
	out, _, _, _, err := runBuffered(context.Background(), reg, reg.Find("go test ./..."), "cat",
		[]string{p}, "go test ./...", "go test ./...", tee.NewSpool("go"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if strings.Contains(out, "library frames hidden") {
		t.Fatalf("must not strip when disabled:\n%s", out)
	}
	if !strings.Contains(out, "site-packages") {
		t.Fatalf("frames should be intact when disabled:\n%s", out)
	}
}

func TestRunBufferedFailureTailFallbackOnEmptiedFilter(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	// cargo is filter_stderr: a failed build whose only lines are noise cargo
	// strips (Compiling/Checking) would otherwise reach the agent as empty , a
	// failure with no visible reason. The fallback must surface the raw tail.
	out, _, hint, code, err := runBuffered(context.Background(), reg, reg.Find("cargo build"), "sh",
		[]string{"-c", "echo '   Compiling foo v0.1.0'; echo '   Checking bar v0.2.0'; exit 1"},
		"cargo build", "cargo build", tee.NewSpool("cargo build"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("emptied failed filter must fall back to the raw tail, got empty stdout")
	}
	if !strings.Contains(out, "Compiling foo") {
		t.Fatalf("fallback lost the raw tail: %q", out)
	}
	if !strings.Contains(hint, "filter emptied the output") {
		t.Fatalf("missing fallback hint: %q", hint)
	}
}

// TestRunBufferedEmptyTailFallbackOnSuccessfulFilter verifies that a SUCCESSFUL
// command (exit 0) whose matched filter strips all output to empty still gets
// the raw-tail fallback and keeps its spool for full-raw recovery. This is the
// symmetric sibling of the failure-fallback test above: the `failed &&` guard
// was the original bug (a docker filter that only emitted cache/layer lines
// would silently return empty on success, causing the agent to retry).
func TestRunBufferedEmptyTailFallbackOnSuccessfulFilter(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	reg := mustRegistry(t)
	// docker filter strips all #N [...] / #N CACHED lines with no on_empty.
	// A successful build whose output is only cache hits would be emptied.
	out, _, hint, code, err := runBuffered(context.Background(), reg, reg.Find("docker build ."), "sh",
		[]string{"-c", "echo '#1 [internal] load build definition from Dockerfile'; echo '#2 CACHED [2/5] WORKDIR /app'"},
		"docker build .", "docker build .", tee.NewSpool("docker build"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("emptied successful filter must fall back to the raw tail, got empty stdout")
	}
	if !strings.Contains(out, "load build definition") {
		t.Fatalf("fallback lost the raw tail: %q", out)
	}
	if !strings.Contains(hint, "filter emptied the output") {
		t.Fatalf("missing fallback hint: %q", hint)
	}
	// The spool must be kept (full-raw recovery on success).
	if !strings.Contains(hint, "[full output:") {
		t.Fatalf("spool must be kept when emptyTailFallback fires: %q", hint)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool file to be kept, got %d", len(entries))
	}
}

func TestRunBufferedNoFallbackWhenFailedCommandIsGenuinelyEmpty(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	out, _, hint, code, err := runBuffered(context.Background(), reg, reg.Find("cargo build"), "sh",
		[]string{"-c", "exit 1"},
		"cargo build", "cargo build", tee.NewSpool("cargo build"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("genuinely-empty failure must stay empty, got %q", out)
	}
	if strings.Contains(hint, "filter emptied the output") {
		t.Fatalf("must not synthesize a tail when there was no output: %q", hint)
	}
}

func TestRunBufferedNoFallbackWhenStderrCarriesError(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	// make is NOT filter_stderr: its stdout noise is stripped, but the real error
	// is on stderr and passes raw. The fallback must stay silent , re-adding the
	// stripped stdout noise next to an already-informative stderr is pure cost.
	out, errOut, hint, code, err := runBuffered(context.Background(), reg, reg.Find("make"), "sh",
		[]string{"-c", "echo \"make[1]: Entering directory '/x'\"; echo 'make: *** [all] Error 2' >&2; exit 1"},
		"make", "make", tee.NewSpool("make"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.Contains(hint, "filter emptied the output") {
		t.Fatalf("must not fall back when stderr already carries the error: %q", hint)
	}
	if !strings.Contains(errOut, "Error 2") {
		t.Fatalf("stderr error must be preserved: %q", errOut)
	}
	if strings.Contains(out, "Entering directory") {
		t.Fatalf("stripped stdout noise must not reappear on stdout: %q", out)
	}
}

func TestRunBufferedJQCompleteJSONPassesWhole(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	// A complete-JSON array that pretty-prints to far more than jq's 40-line cap.
	// With reduce_json removed, jq rides jsonGuard like every other filter: the
	// whole document arrives (<= 1 MiB), never cut mid-structure.
	var b strings.Builder
	b.WriteString("[\n")
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteString(",\n")
		}
		b.WriteString("  {\"id\": ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("}")
	}
	b.WriteString("\n]\n")
	dir := t.TempDir()
	p := filepath.Join(dir, "data.json")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, _, code, err := runBuffered(context.Background(), reg, reg.Find("jq ."), "cat",
		[]string{p}, "jq .", "jq .", tee.NewSpool("jq ."))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !json.Valid([]byte(strings.TrimSpace(out))) {
		t.Fatalf("jq complete-JSON output must arrive parseable, got:\n%s", out)
	}
	if !strings.Contains(out, `"id": 49`) {
		t.Fatalf("jq output was cut mid-structure, missing the last element:\n%s", out)
	}
}

func TestRunBufferedJQRawTextStillCapped(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	// jq -r emits raw text, not JSON. IsCompleteJSON is false, so jsonGuard does
	// NOT fire and the line caps still apply , this is where jq's caps earn it.
	out, _, _, code, err := runBuffered(context.Background(), reg, reg.Find("jq -r .[]"), "sh",
		[]string{"-c", "i=1; while [ $i -le 100 ]; do echo value-$i; i=$((i+1)); done"},
		"jq -r .[]", "jq -r .[]", tee.NewSpool("jq -r"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if n := strings.Count(out, "\n"); n > 45 {
		t.Fatalf("jq -r raw text must stay capped (max_lines=40), got %d lines:\n%s", n, out)
	}
	if strings.Contains(out, "value-100") {
		t.Fatalf("capped raw output should not contain the last line, got:\n%s", out)
	}
}

func TestRunBufferedLaunchError(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	_, _, _, _, err := runBuffered(context.Background(), reg, reg.Find("ctx-wire-no-such-binary-xyz"), "ctx-wire-no-such-binary-xyz",
		nil, "ctx-wire-no-such-binary-xyz", "ctx-wire-no-such-binary-xyz", tee.NewSpool("missing"))
	if err == nil {
		t.Error("expected launch error for missing binary")
	}
}

func TestStreamLiveSlowOutput(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	var out, errOut bytes.Buffer
	code, err := streamLive(context.Background(), "sh",
		[]string{"-c", "for i in 1 2 3 4 5; do echo line$i; sleep 0.02; done"},
		"sh -c loop", tee.NewSpool("slow"), &out, &errOut)
	if err != nil {
		t.Fatalf("streamLive: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	for i := 1; i <= 5; i++ {
		if !strings.Contains(out.String(), "line"+string(rune('0'+i))) {
			t.Errorf("missing line%d in streamed output: %q", i, out.String())
		}
	}
}

func TestStreamLiveSpoolsOnFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	var out, errOut bytes.Buffer
	code, err := streamLive(context.Background(), "sh",
		[]string{"-c", "for i in $(seq 1 50); do echo error line with PASSWORD=hunter2 detail; done; exit 2"},
		"sh -c fail", tee.NewSpool("fail"), &out, &errOut)
	if err != nil {
		t.Fatalf("streamLive: %v", err)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if strings.Contains(out.String(), "hunter2") {
		t.Errorf("secret leaked to live stream: %q", out.String())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool file on failure, got %d", len(entries))
	}
	data, _ := os.ReadFile(dir + "/" + entries[0].Name())
	if strings.Contains(string(data), "hunter2") {
		t.Error("secret leaked into spool file")
	}
}

// setMaxCapture temporarily shrinks the in-memory cap; the returned func
// restores it. Lets bounded-output tests run with little data.
func setMaxCapture(n int) func() {
	old := maxCapture
	maxCapture = n
	return func() { maxCapture = old }
}

func TestCaptureBoundedResultFullSpool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	defer setMaxCapture(64 << 10)() // 64 KiB cap
	reg := mustRegistry(t)
	// ~180 KiB of output, well over the 64 KiB cap.
	out, code, err := Capture(context.Background(), reg, "sh",
		[]string{"-c", "yes hello-world | head -n 30000"})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(out) > maxCapture+4096 {
		t.Errorf("in-memory result not bounded: %d bytes (cap %d)", len(out), maxCapture)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation note in bounded output")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool file, got %d", len(entries))
	}
	if info, _ := entries[0].Info(); info.Size() <= int64(maxCapture) {
		t.Errorf("spool should hold full output, got %d bytes (cap %d)", info.Size(), maxCapture)
	}
}

func TestCaptureFilterTruncationKeepsFullSpool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	reg := mustRegistry(t)

	file := filepath.Join(t.TempDir(), "large.txt")
	var body strings.Builder
	for i := 0; i < 220; i++ {
		body.WriteString("line-")
		body.WriteString(strconv.Itoa(i))
		body.WriteString(" contents\n")
	}
	if err := os.WriteFile(file, []byte(body.String()), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	out, code, err := Capture(context.Background(), reg, "cat", []string{file})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "filter output truncated") {
		t.Errorf("expected filter truncation note, got %q", out)
	}
	if !strings.Contains(out, "[full output:") {
		t.Errorf("expected full output hint, got %q", out)
	}
	if strings.Contains(out, "line-200") {
		t.Errorf("filtered output should not include capped tail line: %q", out)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool file, got %d", len(entries))
	}
	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read spool: %v", err)
	}
	if !strings.Contains(string(data), "line-200 contents") {
		t.Errorf("spool should contain full output, got %q", data)
	}
}

func TestCaptureOver10MiBBoundedWithFullSpool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping >10MiB test in short mode")
	}
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	defer setMaxCapture(1 << 20)() // 1 MiB cap keeps the in-memory scrub cheap
	reg := mustRegistry(t)
	// ~12 MiB of output: 850k lines of 14 bytes (13 chars + newline).
	out, code, err := Capture(context.Background(), reg, "sh",
		[]string{"-c", "yes aaaaaaaaaaaaa | head -n 850000"})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(out) > maxCapture+4096 {
		t.Errorf("in-memory result not bounded: %d bytes", len(out))
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool file, got %d", len(entries))
	}
	if info, _ := entries[0].Info(); info.Size() <= 10<<20 {
		t.Errorf("spool should hold the full >10MiB output, got %d bytes", info.Size())
	}
}

func TestCaptureSpoolScrubsMultiLineSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", dir)
	reg := mustRegistry(t)
	// Exit non-zero so the spool is retained (kept on failure), then assert the
	// multi-line secret was redacted in the persisted log.
	script := "for i in $(seq 1 40); do echo connecting; done; " +
		"printf '%s\\n' '-----BEGIN RSA PRIVATE KEY-----' 'MIIBVgSECRETKEYBODY' '-----END RSA PRIVATE KEY-----'; " +
		"exit 1"
	out, code, err := Capture(context.Background(), reg, "sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.Contains(out, "SECRETKEYBODY") {
		t.Errorf("secret leaked in returned output: %q", out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 spool file, got %d", len(entries))
	}
	data, _ := os.ReadFile(dir + "/" + entries[0].Name())
	if strings.Contains(string(data), "SECRETKEYBODY") {
		t.Error("multi-line secret leaked into spool file")
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Error("expected redaction marker in spool file")
	}
}

func TestCaptureCancellationKillsChild(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _, _ = Capture(ctx, reg, "sh", []string{"-c", "sleep 30"})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("cancellation did not kill child promptly: took %v", elapsed)
	}
}

func TestCaptureCancellationKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group cancellation is Unix-specific")
	}
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "sleep.pid")
	ctx, cancel := context.WithCancel(context.Background())
	script := "sleep 30 & echo $! > " + shellQuotePath(pidFile) + "; wait"
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(pidFile); err == nil {
				cancel()
				close(done)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		close(done)
	}()
	_, _, _ = Capture(ctx, reg, "sh", []string{"-c", script})
	<-done

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}
	pid := strings.TrimSpace(string(data))
	for i := 0; i < 50; i++ {
		if exec.Command("kill", "-0", pid).Run() != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild process %s still alive after cancellation", pid)
}

// TestGainLogNoSplitFlagLeak proves a split secret flag value never reaches the
// gain log when a command is run through ctx-wire.
func TestGainLogNoSplitFlagLeak(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	gainFile := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN", "") // re-enable (TestMain disabled it process-wide)
	t.Setenv("CTX_WIRE_GAIN_FILE", gainFile)
	reg := mustRegistry(t)

	_, _, err := Capture(context.Background(), reg, "true",
		[]string{"--password", "hunter2", "--token", "abc123", "--env", "prod"})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	data, err := os.ReadFile(gainFile)
	if err != nil {
		t.Fatalf("read gain log: %v", err)
	}
	for _, secret := range []string{"hunter2", "abc123"} {
		if strings.Contains(string(data), secret) {
			t.Errorf("secret %q leaked into gain log: %s", secret, data)
		}
	}
	if !strings.Contains(string(data), "REDACTED") {
		t.Errorf("expected redaction in gain log: %s", data)
	}
	if !strings.Contains(string(data), "prod") {
		t.Errorf("expected non-secret arg 'prod' preserved: %s", data)
	}
}

// TestGainRecordsCurrentAgent proves CTX_WIRE_AGENT set by a hook or shim is
// recorded on the gain entry, so savings can be attributed per agent.
func TestGainRecordsCurrentAgent(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	gainFile := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN", "") // re-enable (TestMain disabled it process-wide)
	t.Setenv("CTX_WIRE_GAIN_FILE", gainFile)
	t.Setenv("CTX_WIRE_AGENT", "Claude") // value an outer hook/shim would export
	reg := mustRegistry(t)

	if _, _, err := Capture(context.Background(), reg, "true", []string{"hello"}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	data, err := os.ReadFile(gainFile)
	if err != nil {
		t.Fatalf("read gain log: %v", err)
	}
	if !strings.Contains(string(data), `"agent":"claude"`) {
		t.Errorf("expected normalized agent recorded in gain log: %s", data)
	}
}

// TestGainOmitsAgentWhenUnset proves an unattributed run records no agent field.
func TestGainOmitsAgentWhenUnset(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	gainFile := filepath.Join(t.TempDir(), "gain.jsonl")
	t.Setenv("CTX_WIRE_GAIN", "")
	t.Setenv("CTX_WIRE_GAIN_FILE", gainFile)
	t.Setenv("CTX_WIRE_AGENT", "")         // no explicit agent
	t.Setenv("CTX_WIRE_AGENT_DETECT", "0") // and disable process-tree detection, so the test is deterministic wherever it runs
	reg := mustRegistry(t)

	if _, _, err := Capture(context.Background(), reg, "true", []string{"hello"}); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	data, err := os.ReadFile(gainFile)
	if err != nil {
		t.Fatalf("read gain log: %v", err)
	}
	if strings.Contains(string(data), `"agent"`) {
		t.Errorf("unattributed run should omit the agent field: %s", data)
	}
}

func shellQuotePath(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestCaptureRejectsInteractive(t *testing.T) {
	reg := mustRegistry(t)
	_, _, err := Capture(context.Background(), reg, "vim", []string{"file.txt"})
	if err == nil {
		t.Error("expected Capture to reject an interactive command")
	}
}

func TestCapWriterBounds(t *testing.T) {
	w := &capWriter{max: 10}
	n, err := w.Write([]byte("0123456789ABCDEF"))
	if err != nil || n != 16 {
		t.Fatalf("Write returned (%d, %v), want (16, nil)", n, err)
	}
	if got := w.String(); got != "0123456789" {
		t.Errorf("capped buffer = %q, want first 10 bytes", got)
	}
	if !w.truncated {
		t.Error("expected truncated flag to be set")
	}
}

func mustRegistry(t *testing.T) *filter.Registry {
	t.Helper()
	reg, err := filter.LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	return reg
}
