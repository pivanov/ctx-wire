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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"ctx-wire/internal/agent"
	"ctx-wire/internal/commandpolicy"
	"ctx-wire/internal/filter"
	"ctx-wire/internal/gain"
	"ctx-wire/internal/recent"
	"ctx-wire/internal/scrub"
	"ctx-wire/internal/shim"
	"ctx-wire/internal/stripstack"
	"ctx-wire/internal/tee"
	"ctx-wire/internal/telemetry"
)

// maxCapture bounds the bytes retained in memory per stream so a runaway
// command cannot exhaust memory. Output beyond the cap is dropped from the
// in-memory result and flagged as truncated; the full output is still spooled
// to disk. It is a var so tests can shrink it. The on-disk spool is unaffected.
var maxCapture = 10 << 20 // 10 MiB

// explicitRangeCeiling bounds how many lines an explicit, bounded read
// (sed -n 'A,Bp', head/tail -n N) may carry past the per-filter line cap. A
// deliberate slice up to this size honors the agent's own bound; beyond it the
// normal cap + spool applies, so a huge "explicit" range (sed -n '1,99999p')
// still cannot flood context.
const explicitRangeCeiling = 300

// EnvSource is set to "hook" by `ctx-wire run --agent ...` (how a rewrite hook or
// plugin invokes us), so gain can record the true entry point. Process-tree
// agent detection is not a reliable hook signal on its own.
const EnvSource = "CTX_WIRE_SOURCE"

// Run executes name+args, applies the matching filter from reg, scrubs all
// output to the process stdio, and returns the child's exit code. A non-nil
// error indicates ctx-wire itself failed to launch the command (distinct from
// the command exiting non-zero, which is reported via the returned code).
func Run(ctx context.Context, reg *filter.Registry, name string, args []string) (int, error) {
	// Resolve once, strictly: if name resolves only to a ctx-wire shim with no
	// real binary, fail cleanly with 127 instead of re-executing the shim (which
	// would bounce back into it). Covers both the bypass and filtered paths.
	execName, err := shim.ResolveRealStrict(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire: %v\n", err)
		return 127, nil
	}
	// Record that a shim wired into us up front, before the bypass/filter branch,
	// so the usage signal counts EVERY shim invocation. Bypassed commands return
	// early below and would otherwise never be recorded, which would let a real
	// steering user read zero recorded shim use (and mislead the auto-prune).
	if shimName := os.Getenv(shim.EnvName); shimName != "" {
		_ = shim.RecordUse(shimName, scrub.Command(name, args))
	}
	if shouldBypass(name, args) {
		return runInherited(ctx, execName, args)
	}
	cmdline := commandLine(name, args)
	scrubbedCmd := scrub.Command(name, args)
	spool := tee.NewSpool(scrubbedCmd)

	// A configured full-context file (skill / instruction doc) must reach the
	// agent whole: stream it scrubbed but skip both the filter cap and the
	// passthrough ceiling. Scrubbing is preserved (streamLive still wraps
	// scrub.NewWriter); only capping is bypassed.
	if commandpolicy.IsFullFileRead(name, args) {
		return streamLive(ctx, execName, args, scrubbedCmd, spool, os.Stdout, os.Stderr, true)
	}

	matched := reg.Find(cmdline)

	// No filter: stream output live (line-buffered, scrubbed) so long-running
	// commands surface progress instead of buffering until exit.
	if matched == nil {
		return streamLive(ctx, execName, args, scrubbedCmd, spool, os.Stdout, os.Stderr, false)
	}

	// A filter needs the whole output, so buffer (bounded), then emit.
	out, errOut, hint, code, err := runBuffered(ctx, reg, matched, execName, args, cmdline, scrubbedCmd, spool)
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
func streamLive(ctx context.Context, name string, args []string, scrubbedCmd string, spool *tee.Spool, stdout, stderr io.Writer, ceilingOff bool) (int, error) {
	emitOut := &countWriter{w: stdout}
	emitErr := &countWriter{w: stderr}
	// The passthrough ceiling sits between the scrubber and the agent: the head
	// streams live, each stream's tail is kept, and the middle of an oversized
	// dump is omitted with a marker. The spool branch below is NOT limited, so
	// the full scrubbed output stays recoverable whenever the ceiling fires.
	var outLim, errLim *ceilWriter
	outDst, errDst := io.Writer(emitOut), io.Writer(emitErr)
	if head, tail, enabled := passthroughCeiling(); enabled && !ceilingOff {
		ceil := newStreamCeiling(head, tail)
		outLim, errLim = ceil.writer(emitOut), ceil.writer(emitErr)
		outDst, errDst = outLim, errLim
	}
	outScrub := scrub.NewWriter(outDst)
	errScrub := scrub.NewWriter(errDst)
	rawOut := &counter{}
	rawErr := &counter{}

	code, err := execChild(ctx, name, args,
		io.MultiWriter(outScrub, spool, rawOut),
		io.MultiWriter(errScrub, spool, rawErr))

	// Flush held-back bytes regardless of outcome.
	_ = outScrub.Close()
	_ = errScrub.Close()
	truncated := false
	if outLim != nil {
		ot, outErr := outLim.flush()
		et, errErr := errLim.flush()
		truncated = ot || et
		if outErr != nil || errErr != nil {
			// A broken stdout/stderr pipe loses the tail from the live stream, but the
			// full scrubbed output is in the spool and the fetch hint below goes to
			// stderr. Surface the failure instead of swallowing it.
			fmt.Fprintf(stderr, "ctx-wire: ceiling flush write failed (output spooled, recover via fetch): out=%v err=%v\n", outErr, errErr)
		}
	}

	if err != nil {
		_, _ = spool.Finalize(false)
		return code, err
	}
	recordGain(scrubbedCmd, "", "passthrough", rawOut.n+rawErr.n, emitOut.n+emitErr.n, code)
	// Keep the spool on failure or truncation, but only POINT at it when the
	// ceiling actually omitted bytes. Passthrough already streamed the full scrubbed
	// output live, so on a plain failure with nothing omitted the agent already has
	// everything and a "[full output: ...]" footer would be pure net-negative cost.
	if path, ok := spool.Finalize(code != 0 || truncated); ok {
		if truncated {
			fmt.Fprintln(stderr, tee.Hint(path))
		}
	}
	return code, nil
}

// runBuffered runs the command capturing output into bounded buffers, applies
// the matching filter (if any), scrubs the result, and spools the full scrubbed
// output to disk (kept on failure or truncation). It returns the strings to
// deliver rather than writing them, so both the CLI and MCP paths can reuse it.
func runBuffered(ctx context.Context, reg *filter.Registry, matched *filter.CompiledFilter, name string, args []string, cmdline, scrubbedCmd string, spool *tee.Spool) (stdoutText, stderrText, hint string, code int, err error) {
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
	emptyTailFallback := false
	switch f := matched; {
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
		opts := filter.ApplyOptions{
			SuppressSyntheticSuccess: failed,
			KeepTailOnTruncate:       failed,
			TruncateLevel:            filter.ResolveTruncateLevel(),
		}
		// An explicit, bounded line request (sed -n 'A,Bp', head/tail -n N) up to
		// the ceiling honors the agent's own bound instead of the filter's cap, so
		// a deliberate slice is not re-capped. Scrub and truncate_lines_at still apply.
		if span, ok := commandpolicy.ExplicitLineSpan(name, args); ok && span <= explicitRangeCeiling {
			opts.MaxLinesOverride = &span
		}
		applied := applySafe(f, text, opts)
		if jsonText, jsonMode, ok := jsonGuard(out, applied.Truncated, f.FilterStderr, f.ReducesJSON()); ok {
			mode = jsonMode
			stdoutText = jsonText
			stderrText = scrub.Scrub(errOut)
			filterTruncated = jsonMode == jsonModeCapped
		} else {
			filterTruncated = applied.Truncated
			stdoutText = withTrailingNewline(scrub.Scrub(maybeStripStack(applied.Output, &filterTruncated)))
			if !f.FilterStderr {
				// Most language runtimes print stack traces to stderr, so strip it
				// too when the filter did not already merge it into stdout.
				stderrText = scrub.Scrub(maybeStripStack(errOut, &filterTruncated))
			}
			// Safety net: a command whose filter stripped every line would otherwise
			// reach the agent as empty output. Fall back to the tail of the raw
			// filtered input, but only when stderrText is also empty: for the common
			// stderr-on-failure tools the error is already visible there, so a raw
			// stdout tail would just re-add the noise the filter correctly removed.
			// stderrText is empty exactly when filter_stderr consumed the stream, the
			// error went to stdout, or the streams were merged with 2>&1.
			// Filters with on_empty are naturally exempt (they emit a non-empty
			// message, so stdoutText=="" is false). Legitimately-empty output (e.g.
			// grep no-match) is excluded by text != "" (raw was empty too).
			if strings.TrimSpace(stdoutText) == "" &&
				strings.TrimSpace(stderrText) == "" &&
				strings.TrimSpace(text) != "" {
				stdoutText = withTrailingNewline(scrub.Scrub(tailLines(text, emptyTailLines)))
				emptyTailFallback = true
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
	if emptyTailFallback {
		meta = append(meta, "[ctx-wire: filter emptied the output; showing raw tail]")
	}

	// Dedup: if an eligible read-only command re-ran with byte-identical output,
	// substitute a short recoverable reference for the body. Account the saved
	// bytes against the real raw output size, keep the prior retained entry as the
	// recoverable copy (do not record a new one), and discard the spool. Skipped
	// when this run was truncated, so a truncation notice is never silently lost.
	if !truncated {
		if ref, ok := maybeDedup(name, args, scrubbedCmd, stdoutText+stderrText, code); ok {
			recordGain(scrubbedCmd, filterName, "dedup", outCap.total+errCap.total, len(ref), code)
			_, _ = spool.Finalize(false)
			return ref, "", "", code, nil
		}
	}

	// Spool retention + net-negative guard. The recovery footer earns its bytes
	// only when content was actually omitted. On a failure with no truncation, if
	// the filtered output plus the footer is not smaller than the full scrubbed
	// raw, the raw is complete and no larger, so emit it instead and drop the
	// footer and spool. Truncation and the empty-tail fallback always keep the
	// footer (genuine recovery). The fallback emits the already-scrubbed raw, never
	// unsanitized bytes.
	recovery := truncated || emptyTailFallback
	if path, ok := spool.Finalize(code != 0 || recovery); ok {
		footer := tee.Hint(path)
		rawStdout, rawStderr := scrub.Scrub(out), scrub.Scrub(errOut)
		switch {
		case recovery:
			// Genuine recovery (truncation / empty-tail): always show the pointer.
			meta = append(meta, footer)
		case len(stdoutText)+len(stderrText) >= len(rawStdout)+len(rawStderr):
			// The filter did not shrink the output, so the full scrubbed raw is no
			// larger and is complete: emit it (the agent already has everything) and
			// skip the pointer. Always scrubbed, never raw bytes.
			stdoutText, stderrText = rawStdout, rawStderr
		case len(stdoutText)+len(stderrText)+len(footer) < len(rawStdout)+len(rawStderr):
			// The filter saved more than the footer costs: keep the recovery pointer.
			meta = append(meta, footer)
		default:
			// The filter helped, but by fewer bytes than the footer would add: keep
			// the smaller filtered output and drop the pointer so the footer can
			// never make a failure net-negative. The spool stays on disk as a
			// best-effort recovery copy.
		}
	}

	recordGain(scrubbedCmd, filterName, mode, outCap.total+errCap.total, len(stdoutText)+len(stderrText), code)
	recordRecent(scrubbedCmd, filterName, mode, outCap, errCap, stdoutText, stderrText, code)

	hint = strings.Join(meta, "\n")
	return stdoutText, stderrText, hint, code, nil
}

// emptyTailLines bounds how many trailing lines of raw output the empty-output
// fallback surfaces when a filter empties a command's output. The full scrubbed
// output is always spooled, so this only needs to be enough to show the error.
const emptyTailLines = 20

// tailLines returns the last n lines of s (a trailing newline is ignored).
func tailLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// recordGain appends a gain entry, best-effort. Telemetry must never break a run.
func recordGain(cmdline, filterName, mode string, rawBytes, emittedBytes, exitCode int) {
	// Shim-use recording moved to the top of Run so it also counts bypassed
	// commands; recordGain now only handles gain telemetry.
	if !gain.Enabled() {
		return
	}
	ag := agent.Current()
	if err := gain.RecordWithMeta(cmdline, filterName, mode, ag, gainSource(), rawBytes, emittedBytes, exitCode); err == nil {
		_, _ = telemetry.RecordCommand(cmdline, ag, rawBytes, emittedBytes)
	}
}

// gainSource records how ctx-wire was reached, so hook-vs-shim savings can be
// compared (the benchmark for narrowing shim coverage). The shim sets
// CTX_WIRE_SHIM; the `run --agent` wrapper form (used by hooks/plugins, rarely
// by hand) sets EnvSource=hook. An attributed agent alone is NOT a hook signal:
// a bare `ctx-wire run` typed inside an agent session also resolves an agent via
// process-tree detection, so anything without those markers is a plain run.
func gainSource() string {
	switch {
	case os.Getenv(shim.EnvName) != "":
		return "shim"
	case os.Getenv(EnvSource) == "hook":
		return "hook"
	case os.Getenv(EnvSource) == "mcp":
		// The MCP server sets EnvSource=mcp so run_command savings are attributed
		// to the MCP reach-path instead of a generic "run".
		return "mcp"
	default:
		return "run"
	}
}

// retentionOpts configures the recent-outputs store; off until main wires it
// from config. Only the buffered (filtered) path records; streamed passthrough
// output is not in memory to retain.
var retentionOpts recent.Options

// SetRetention configures the recent-outputs store used by `ctx-wire inspect`
// (and, later, dedup). Disabled by default.
func SetRetention(o recent.Options) { retentionOpts = o }

// recordRecent stores the just-emitted command output, best-effort. No-op when
// retention is off. The raw (pre-filter) body is scrubbed and stored only when
// the raw tier is enabled; the emitted text is already scrubbed.
func recordRecent(cmd, filterName, mode string, outCap, errCap *capWriter, stdoutText, stderrText string, code int) {
	if !retentionOpts.Enabled {
		return
	}
	var raw string
	if retentionOpts.RawBodies {
		raw = scrub.Scrub(outCap.String()) + scrub.Scrub(errCap.String())
	}
	recent.Record(retentionOpts, recent.Entry{
		TS:        time.Now().UTC().Format(time.RFC3339Nano),
		Command:   cmd,
		Filter:    filterName,
		Mode:      mode,
		RawBytes:  outCap.total + errCap.total,
		EmitBytes: len(stdoutText) + len(stderrText),
		Exit:      code,
		Emitted:   stdoutText + stderrText,
		Raw:       raw,
	})
}

// DedupOptions configures repeat-command dedup.
type DedupOptions struct {
	Enabled bool
	Recency time.Duration
}

var dedupOpts DedupOptions

// SetDedup configures repeat-command dedup. Off by default; main wires it from
// config and ensures the recent store is recording when it is on.
func SetDedup(o DedupOptions) { dedupOpts = o }

// maybeDedup returns a recoverable reference to substitute for emitted when an
// eligible read-only command re-ran with byte-identical output recently. ok is
// false otherwise. The command still ran; this only avoids re-emitting the body.
// It never dedups a failed command (code != 0): an error result should always be
// shown.
func maybeDedup(name string, args []string, scrubbedCmd, emitted string, code int) (string, bool) {
	// Gating on retentionOpts.Enabled ties dedup to the store being operational,
	// so CTX_WIRE_RETENTION=0 (which clears it via ApplyEnv) disables dedup too,
	// even when prior entries still sit on disk.
	if !dedupOpts.Enabled || !retentionOpts.Enabled || emitted == "" || code != 0 || dedupDisabledByEnv() {
		return "", false
	}
	if !commandpolicy.IsDedupEligible(name, args) {
		return "", false
	}
	prev, ok := recent.LastMatch(scrubbedCmd, dedupOpts.Recency, time.Now())
	if !ok || prev.Hash != recent.Hash(emitted) {
		return "", false
	}
	lines := strings.Count(strings.TrimRight(emitted, "\n"), "\n") + 1
	when := prev.TS
	if t, err := time.Parse(time.RFC3339Nano, prev.TS); err == nil {
		when = t.Local().Format("15:04:05")
	}
	return fmt.Sprintf("[ctx-wire: unchanged since %s (%d lines); `ctx-wire inspect --list` then `ctx-wire inspect <n>` to recover it, or re-run with --no-dedup]\n", when, lines), true
}

func dedupDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("CTX_WIRE_NO_DEDUP"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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
	execName, err := shim.ResolveRealStrict(name)
	if err != nil {
		return "", 127, fmt.Errorf("ctx-wire: %w", err)
	}
	cmdline := commandLine(name, args)
	scrubbedCmd := scrub.Command(name, args)
	spool := tee.NewSpool(scrubbedCmd)
	matched := reg.Find(cmdline)
	out, errOut, hint, code, err := runBuffered(ctx, reg, matched, execName, args, cmdline, scrubbedCmd, spool)
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

// maybeStripStack collapses library stack frames in s when the opt-in
// [output] strip_stacktraces is enabled. When it collapses anything it sets
// *truncated so the raw (scrubbed) trace stays spooled and the recovery hint
// fires. A no-op when disabled or when s has no recognizable stack trace.
func maybeStripStack(s string, truncated *bool) string {
	if stripstack.Enabled() {
		if stripped, did := stripstack.Strip(s); did {
			*truncated = true
			return stripped
		}
	}
	return s
}

// withTrailingNewline ensures non-empty filtered output ends with exactly one newline.
func withTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

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
	if !truncated || filterStderr || reducesJSON || !filter.IsCompleteJSON(out) {
		return "", "", false
	}
	if len(out) <= filter.MaxJSONPassthrough {
		return withTrailingNewline(scrub.Scrub(out)), jsonModeWhole, true
	}
	return withTrailingNewline(jsonOversizeMarker(len(out))), jsonModeCapped, true
}

func jsonOversizeMarker(n int) string {
	return fmt.Sprintf("[ctx-wire: %d-byte JSON document omitted (over the %d-byte passthrough ceiling); full log spooled]", n, filter.MaxJSONPassthrough)
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

// RunRaw runs name+args with stdin/stdout/stderr inherited, the environment
// unmodified, and no filtering or scrubbing, returning the command's own exit
// code (128+signal when signaled). It is the byte-exact passthrough used by
// `run --shim` when no agent is detected, matching the Unix shell shim's
// `exec "$real"`. It deliberately does NOT use newCommand (which injects
// CTX_WIRE_DISABLE_SHIMS and sets up process groups): a true passthrough must
// leave the child's view of the world untouched, including stdin so a statusline
// doing `input=$(cat)` still receives its piped JSON.
func RunRaw(ctx context.Context, name string, args []string) int {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	code, err := runAndExitCode(cmd)
	if err != nil {
		// The resolver guarantees a real exe exists, so a start failure here is
		// rare; surface it as "command not found" rather than a generic 1.
		fmt.Fprintf(os.Stderr, "ctx-wire: failed to run %q: %v\n", name, err)
		return 127
	}
	return code
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
