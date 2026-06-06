// Package runner executes a command, filters its output through the matching
// filter, and propagates the exit code. It is the core token-saving path:
// verbose output is compressed, secrets are scrubbed before anything is printed
// or persisted, and on failure the full (scrubbed) output is teed to disk.
//
// Commands that need a live terminal or stream indefinitely (editors, pagers,
// REPLs, `-f`/`--follow`, `--watch`) bypass capture and run with inherited
// stdio so they are never broken to save tokens.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/commandpolicy"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/scrub"
	"ctx-wire/internal/shim"
	"ctx-wire/internal/tee"
	"ctx-wire/internal/telemetry"
)

// maxCapture bounds the bytes retained in memory per stream so a runaway
// command cannot exhaust memory. Output beyond the cap is dropped from the
// in-memory result and flagged as truncated; the full output is still spooled
// to disk. It is a var so tests can shrink it. The on-disk spool is unaffected.
var maxCapture = 10 << 20 // 10 MiB

// Run executes name+args, applies the matching filter from reg, scrubs all
// output to the process stdio, and returns the child's exit code. A non-nil
// error indicates ctx-wire itself failed to launch the command (distinct from
// the command exiting non-zero, which is reported via the returned code).
func Run(ctx context.Context, reg *filter.Registry, name string, args []string) (int, error) {
	if shouldBypass(name, args) {
		execName, _ := shim.ResolveReal(name)
		return runInherited(ctx, execName, args)
	}
	cmdline := commandLine(name, args)
	scrubbedCmd := scrub.Command(name, args)
	spool := tee.NewSpool(scrubbedCmd)
	execName, _ := shim.ResolveReal(name)

	matched := reg.Find(cmdline)

	// No filter: stream output live (line-buffered, scrubbed) so long-running
	// commands surface progress instead of buffering until exit.
	if matched == nil {
		return streamLive(ctx, execName, args, scrubbedCmd, spool, os.Stdout, os.Stderr)
	}

	// A filter needs the whole output, so buffer (bounded), then emit.
	out, errOut, hint, code, err := runBuffered(ctx, reg, execName, args, cmdline, scrubbedCmd, spool)
	if err != nil {
		return code, err
	}
	if out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	if errOut != "" {
		fmt.Fprint(os.Stderr, errOut)
	}
	if hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}
	return code, nil
}

func commandLine(name string, args []string) string {
	return strings.TrimSpace(name + " " + strings.Join(args, " "))
}

// streamLive runs the command and streams its output live to the given writers,
// scrubbing in-flight (line-buffered, with multi-line secrets held back). The
// full scrubbed output is spooled to disk and kept if the command fails. Used
// for the passthrough path, where there is no filter that needs whole output.
func streamLive(ctx context.Context, name string, args []string, scrubbedCmd string, spool *tee.Spool, stdout, stderr io.Writer) (int, error) {
	emitOut := &countWriter{w: stdout}
	emitErr := &countWriter{w: stderr}
	outScrub := scrub.NewWriter(emitOut)
	errScrub := scrub.NewWriter(emitErr)
	rawOut := &counter{}
	rawErr := &counter{}

	code, err := execChild(ctx, name, args,
		io.MultiWriter(outScrub, spool, rawOut),
		io.MultiWriter(errScrub, spool, rawErr))

	// Flush held-back bytes regardless of outcome.
	_ = outScrub.Close()
	_ = errScrub.Close()

	if err != nil {
		_, _ = spool.Finalize(false)
		return code, err
	}
	recordGain(scrubbedCmd, "", "passthrough", rawOut.n+rawErr.n, emitOut.n+emitErr.n, code)
	if path, ok := spool.Finalize(code != 0); ok {
		fmt.Fprintln(stderr, tee.Hint(path))
	}
	return code, nil
}

// runBuffered runs the command capturing output into bounded buffers, applies
// the matching filter (if any), scrubs the result, and spools the full scrubbed
// output to disk (kept on failure or truncation). It returns the strings to
// deliver rather than writing them, so both the CLI and MCP paths can reuse it.
func runBuffered(ctx context.Context, reg *filter.Registry, name string, args []string, cmdline, scrubbedCmd string, spool *tee.Spool) (stdoutText, stderrText, hint string, code int, err error) {
	outCap := &capWriter{max: maxCapture}
	errCap := &capWriter{max: maxCapture}

	code, err = execChild(ctx, name, args,
		io.MultiWriter(outCap, spool),
		io.MultiWriter(errCap, spool))
	if err != nil {
		_, _ = spool.Finalize(false)
		return "", "", "", code, err
	}

	out := outCap.String()
	errOut := errCap.String()

	filterName := ""
	mode := "passthrough"
	filterTruncated := false
	switch f := reg.Find(cmdline); {
	case f == nil:
		stdoutText = scrub.Scrub(out)
		stderrText = scrub.Scrub(errOut)
	default:
		filterName = f.Name
		mode = "filtered"
		text := out
		if f.FilterStderr {
			text = out + errOut
		}
		failed := code != 0
		applied := applySafe(f, text, filter.ApplyOptions{
			SuppressSyntheticSuccess: failed,
			KeepTailOnTruncate:       failed,
		})
		if jsonText, jsonMode, ok := jsonGuard(out, applied.Truncated, f.FilterStderr, f.ReducesJSON()); ok {
			mode = jsonMode
			stdoutText = jsonText
			stderrText = scrub.Scrub(errOut)
			filterTruncated = jsonMode == jsonModeCapped
		} else {
			filterTruncated = applied.Truncated
			stdoutText = withTrailingNewline(scrub.Scrub(applied.Output))
			if !f.FilterStderr {
				stderrText = scrub.Scrub(errOut)
			}
		}
	}

	truncated := outCap.truncated || errCap.truncated || filterTruncated
	// Truncation notices and the spool hint are diagnostic metadata, not command
	// output. They go on the hint channel (written to stderr by the CLI), so they
	// never contaminate stdout when ctx-wire run is a pipe producer or inside a
	// command substitution.
	var meta []string
	if outCap.truncated || errCap.truncated {
		meta = append(meta, fmt.Sprintf("[ctx-wire: in-memory output truncated at %d bytes per stream; full log spooled]", maxCapture))
	}
	if filterTruncated {
		meta = append(meta, "[ctx-wire: filter output truncated; full log spooled]")
	}

	recordGain(scrubbedCmd, filterName, mode, outCap.total+errCap.total, len(stdoutText)+len(stderrText), code)

	if path, ok := spool.Finalize(code != 0 || truncated); ok {
		meta = append(meta, tee.Hint(path))
	}
	hint = strings.Join(meta, "\n")
	return stdoutText, stderrText, hint, code, nil
}

// recordGain appends a gain entry, best-effort. Telemetry must never break a run.
func recordGain(cmdline, filterName, mode string, rawBytes, emittedBytes, exitCode int) {
	if shimName := os.Getenv(shim.EnvName); shimName != "" {
		_ = shim.RecordUse(shimName, cmdline)
	}
	if !gain.Enabled() {
		return
	}
	ag := agent.Current()
	if err := gain.RecordWithMeta(cmdline, filterName, mode, ag, rawBytes, emittedBytes, exitCode); err == nil {
		_, _ = telemetry.RecordCommand(cmdline, ag, rawBytes, emittedBytes)
	}
}

// Capture runs the command and returns its filtered, scrubbed output as a single
// string plus the exit code. Used by the MCP server, where output is returned as
// a tool result rather than written to stdio. Interactive or streaming commands
// are rejected (they cannot be captured into a tool result), and the child is
// killed if ctx is cancelled.
func Capture(ctx context.Context, reg *filter.Registry, name string, args []string) (string, int, error) {
	if shouldBypass(name, args) {
		return "", -1, fmt.Errorf("ctx-wire: %q is interactive or streaming and cannot be run via run_command; run it directly in a terminal", name)
	}
	cmdline := commandLine(name, args)
	scrubbedCmd := scrub.Command(name, args)
	spool := tee.NewSpool(scrubbedCmd)
	execName, _ := shim.ResolveReal(name)
	out, errOut, hint, code, err := runBuffered(ctx, reg, execName, args, cmdline, scrubbedCmd, spool)
	if err != nil {
		return "", code, err
	}
	var b strings.Builder
	b.WriteString(out)
	b.WriteString(errOut)
	if hint != "" {
		b.WriteString(hint)
		b.WriteByte('\n')
	}
	return b.String(), code, nil
}

// execChild runs name+args with the given output writers, propagating ctx
// cancellation to the child process.
func execChild(ctx context.Context, name string, args []string, stdout, stderr io.Writer) (int, error) {
	cmd := newCommand(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	code, err := runAndExitCode(cmd)
	if err != nil {
		return code, fmt.Errorf("ctx-wire: failed to run %q: %w", name, err)
	}
	return code, nil
}

// applySafe runs the filter pipeline, falling back to the unfiltered text if the
// filter panics. The fallback is still scrubbed by the caller, so a filter bug
// degrades token savings but never breaks the command or leaks secrets.
func applySafe(f *filter.CompiledFilter, text string, opts filter.ApplyOptions) (result filter.ApplyResult) {
	defer func() {
		if r := recover(); r != nil {
			result = filter.ApplyResult{Output: text}
		}
	}()
	return filter.ApplyWithMetaOptions(f, text, opts)
}

// withTrailingNewline ensures non-empty filtered output ends with exactly one newline.
func withTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// maxJSONPassthrough bounds how much complete JSON the content-based guarantee
// emits verbatim. A valid JSON document up to this size is passed through whole;
// a larger one is replaced (never cut mid-structure) so savings are preserved.
// A var (not const) so tests can shrink it to exercise the oversize path.
var maxJSONPassthrough = 1 << 20 // 1 MiB

const (
	jsonModeWhole  = "json"
	jsonModeCapped = "json-capped"
)

// jsonGuard implements the documented "JSON payloads are not reduced" guarantee
// by content. When a filter has truncated a complete, valid JSON document on
// stdout (and is not one that intentionally reduces JSON, e.g. jq), the
// truncation almost certainly produced invalid JSON that would break a
// downstream parser (a statusline's jq, a piped consumer). It returns the text
// to emit instead: the whole scrubbed document under the ceiling, or a
// replacement notice for an oversize one, never a mid-structure cut. ok is false
// when the guard does not apply and normal filtering should stand.
func jsonGuard(out string, truncated, filterStderr, reducesJSON bool) (text, mode string, ok bool) {
	if !truncated || filterStderr || reducesJSON || !isCompleteJSON(out) {
		return "", "", false
	}
	if len(out) <= maxJSONPassthrough {
		return withTrailingNewline(scrub.Scrub(out)), jsonModeWhole, true
	}
	return withTrailingNewline(jsonOversizeMarker(len(out))), jsonModeCapped, true
}

// isCompleteJSON reports whether s is a single complete JSON object or array.
// The cheap first-byte gate keeps json.Valid off ordinary output, so noise that
// merely starts with '{'/'[' (a bracketed log line, a brace expansion) is not
// mistaken for JSON.
func isCompleteJSON(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return false
	}
	return json.Valid([]byte(s))
}

func jsonOversizeMarker(n int) string {
	return fmt.Sprintf("[ctx-wire: %d-byte JSON document omitted (over the %d-byte passthrough ceiling); full log spooled]", n, maxJSONPassthrough)
}

func runAndExitCode(cmd *exec.Cmd) (int, error) {
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		// A signal-killed child reports ExitCode() == -1 (a shell surfaces 255);
		// translate it to the conventional 128+signal (137 for SIGKILL) so agents
		// reading exit codes can tell a kill/timeout from a generic failure.
		if code, ok := signalExitCode(ee); ok {
			return code, nil
		}
		return ee.ExitCode(), nil
	}
	return 1, err
}

// shouldBypass reports whether the command should run with inherited stdio
// rather than being captured and filtered.
func shouldBypass(name string, args []string) bool {
	bypass, _ := ClassifyBypass(name, args)
	return bypass
}

// ClassifyBypass reports whether a command would bypass capture (run with
// inherited stdio) and, if so, a human-readable reason. It is the single source
// of truth for the bypass decision, used by both the runner and ctx-wire
// explain so the diagnosis can never drift from runtime behavior.
func ClassifyBypass(name string, args []string) (bool, string) {
	return commandpolicy.ClassifyBypass(name, args)
}

func runInherited(ctx context.Context, name string, args []string) (int, error) {
	cmd := newCommand(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	code, err := runAndExitCode(cmd)
	if err != nil {
		return code, fmt.Errorf("ctx-wire: failed to run %q: %w", name, err)
	}
	return code, nil
}

// capWriter accumulates up to max bytes and discards the rest, always reporting
// a full write so the os/exec output copier never blocks or errors. It bounds
// memory for commands that produce huge output.
type capWriter struct {
	buf       bytes.Buffer
	max       int
	total     int // total bytes written, including those discarded past the cap
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.total += len(p)
	if room := w.max - w.buf.Len(); room > 0 {
		if room >= len(p) {
			w.buf.Write(p)
		} else {
			w.buf.Write(p[:room])
			w.truncated = true
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return len(p), nil
}

func (w *capWriter) String() string { return w.buf.String() }

// countWriter forwards writes to w and tallies the bytes (emitted output).
type countWriter struct {
	w io.Writer
	n int
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// counter tallies bytes written without forwarding them (raw input).
type counter struct{ n int }

func (c *counter) Write(p []byte) (int, error) {
	c.n += len(p)
	return len(p), nil
}
