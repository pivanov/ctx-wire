// Package shim installs small PATH shims that route selected commands through
// ctx-wire without relying on an agent Bash hook.
package shim

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"ctx-wire/internal/paths"
)

const (
	// EnvName is set by managed shims before they exec `ctx-wire run`.
	EnvName = "CTX_WIRE_SHIM"
	// EnvAgent enables shim routing for agent-launched processes. Without this
	// (or an agent-looking parent process), shims exec the real command directly
	// so a user's interactive terminal keeps native colors/progress behavior.
	EnvAgent = "CTX_WIRE_AGENT_SHIMS"
	// EnvForce is a shorter opt-in alias for EnvAgent.
	EnvForce = "CTX_WIRE_SHIMS"
	// EnvDisable forces managed shims to bypass ctx-wire even under an
	// agent-looking parent process.
	EnvDisable = "CTX_WIRE_DISABLE_SHIMS"
	// EnvLog overrides the shim usage log path in tests.
	EnvLog = "CTX_WIRE_SHIM_LOG"
	// EnvDepth is the wrap-recursion counter carried across exec, so a
	// misconfigured PATH degrades to running the real command instead of forking
	// without bound. Shared by the shell shim template and `run --shim`.
	EnvDepth = "CTX_WIRE_SHIM_DEPTH"
	// DepthCap is the maximum number of times ctx-wire may wrap itself before a
	// shim gives up and runs the real command directly.
	DepthCap = 10

	marker = "# ctx-wire shim v1"
)

// DefaultCommands are safe, high-value commands to shim. Deliberately exclude
// shells, language runtimes, and destructive/no-output utilities such as rm/cp.
//
// `cat` is deliberately NOT shimmed: host shell integrations (e.g. Cursor) and
// agents routinely use it as a transport, `eval "$(cat script)"` or
// `data=$(cat file)`, and the hook's attestation gate leaves command
// substitutions un-rewritten, so such a `cat` reaches only the shim. Filtering
// (capping/truncating) that output silently corrupts the captured value. The
// `cat` filter still applies to an agent's explicit `cat file` via the hook;
// see DeprecatedShims for pruning a `cat` shim left by an older install.
var DefaultCommands = []string{
	"agent-browser",
	"awk",
	"base64",
	"biome",
	"bun",
	"bunx",
	"cargo",
	"cut",
	"deno",
	"docker",
	"fd",
	"fdfind",
	"find",
	"gh",
	"git",
	"go",
	"gofmt",
	"grep",
	"head",
	"jq",
	"kubectl",
	"ls",
	"lsof",
	"make",
	"nl",
	"npm",
	"npx",
	"pnpm",
	"rg",
	"ruff",
	"sed",
	"sort",
	"strings",
	"tail",
	"tee",
	"tr",
	"tsc",
	"uniq",
	"wc",
	"xargs",
	"yarn",
}

// DeprecatedShims are commands ctx-wire used to shim but no longer should. The
// self-heal path uninstalls them (across every managed shim dir on PATH) so an
// install that predates their removal stops shimming them, without waiting for a
// manual re-init. Removing a command from DefaultCommands alone never reaches an
// existing user: Install only writes the current list, RefreshManaged keeps any
// managed shim file already on disk current, and UninstallDefault targets only
// the current defaults. Keep removed commands here so they are actually pruned
// and so UninstallDefault can still clean them up.
//
// NEVER remove an entry from this list. ManagedShimDirsOnPATH discovers managed
// dirs by DefaultCommands + DeprecatedShims, so dropping an entry makes a dir
// that holds only that stale shim invisible again and the prune silently stops
// reaching it. (A marker-scan discovery, any PATH dir holding any marker-verified
// shim, would remove this constraint at the cost of stat-ing more files.)
var DeprecatedShims = []string{
	"cat",
}

// InstallReport describes a shim installation pass.
type InstallReport struct {
	Dir       string
	Commands  []string
	Changed   []string
	Unchanged []string
	Missing   []string
	Skipped   []string
}

// UninstallReport describes managed shim removal.
type UninstallReport struct {
	Dir     string
	Removed []string
	Skipped []string
}

// Status reports current shim installation, PATH reachability, and usage.
type Status struct {
	Dir       string
	Commands  []string
	Installed []string
	Missing   []string
	Skipped   []string
	Active    []string
	Uses      int
	LastUse   string
}

// Use is one scrubbed shim-capture log entry.
type Use struct {
	TS      string `json:"ts"`
	Shim    string `json:"shim"`
	Command string `json:"command"`
}

// InstallDefault installs the default shim set.
func InstallDefault(dir, ctxWire string) (InstallReport, error) {
	return Install(dir, ctxWire, DefaultCommands)
}

// UninstallDefault removes managed shims for the default command set.
func UninstallDefault(dir string) (UninstallReport, error) {
	// Include DeprecatedShims so a normal uninstall also clears commands ctx-wire
	// used to manage (a command that was ever shimmed should stay removable).
	cmds := append(append([]string{}, DefaultCommands...), DeprecatedShims...)
	return Uninstall(dir, cmds)
}

// Install writes shims into dir for commands that have a real executable
// outside the shim dir. Existing non-ctx-wire files are never overwritten; they
// are returned as Skipped. Missing real executables are returned as Missing, and
// any stale managed shim for that command is removed so ctx-wire never makes an
// absent command appear to exist.
func Install(dir, ctxWire string, commands []string) (InstallReport, error) {
	report := InstallReport{Dir: dir, Commands: normalized(commands)}
	if dir == "" {
		return report, errors.New("shim directory is empty")
	}
	if ctxWire == "" {
		return report, errors.New("ctx-wire path is empty")
	}
	absCtxWire, err := filepath.Abs(ctxWire)
	if err != nil {
		return report, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return report, err
	}
	for _, cmd := range report.Commands {
		path := filepath.Join(dir, shimFileName(cmd))
		real, realOK := lookPathSkippingDir(cmd, dir)
		if !realOK || cleanPath(real) == cleanPath(path) {
			if data, err := os.ReadFile(path); err == nil && isManaged(data) {
				if rmErr := os.Remove(path); rmErr != nil {
					return report, rmErr
				}
			}
			report.Missing = append(report.Missing, cmd)
			continue
		}
		content := shimScript(cmd, absCtxWire)
		if data, err := os.ReadFile(path); err == nil {
			if string(data) == content {
				report.Unchanged = append(report.Unchanged, cmd)
				continue
			}
			if !isManaged(data) {
				report.Skipped = append(report.Skipped, cmd)
				continue
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return report, err
		}
		if err := writeExecutable(path, []byte(content)); err != nil {
			return report, err
		}
		report.Changed = append(report.Changed, cmd)
	}
	return report, nil
}

// Uninstall removes only ctx-wire-managed shims. Existing non-ctx-wire files are
// never removed; they are returned as Skipped.
func Uninstall(dir string, commands []string) (UninstallReport, error) {
	report := UninstallReport{Dir: dir}
	if dir == "" {
		return report, errors.New("shim directory is empty")
	}
	for _, cmd := range normalized(commands) {
		path := filepath.Join(dir, shimFileName(cmd))
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return report, err
		case !isManaged(data):
			report.Skipped = append(report.Skipped, cmd)
			continue
		default:
			if err := os.Remove(path); err != nil {
				return report, err
			}
			report.Removed = append(report.Removed, cmd)
		}
	}
	return report, nil
}

// Inspect reports installation and PATH reachability without writing anything.
func Inspect(dir string, commands []string) Status {
	st := Status{Dir: dir, Commands: normalized(commands)}
	for _, cmd := range st.Commands {
		path := filepath.Join(dir, shimFileName(cmd))
		data, err := os.ReadFile(path)
		switch {
		case err == nil && isManaged(data):
			st.Installed = append(st.Installed, cmd)
			if p, perr := lookPath(cmd); perr == nil && samePath(p, path) {
				st.Active = append(st.Active, cmd)
			}
		case err == nil:
			st.Skipped = append(st.Skipped, cmd)
		default:
			st.Missing = append(st.Missing, cmd)
		}
	}
	if uses, last, err := UsageSummary(); err == nil {
		st.Uses = uses
		st.LastUse = last
	}
	return st
}

// ResolveReal returns the real executable for name when name points at a
// ctx-wire-managed shim. It prevents nested wrapping when an agent hook already
// rewrote `git status` into `ctx-wire run git status` while shims are first on
// PATH. The returned bool is true only when name was rewritten.
func ResolveReal(name string) (string, bool) {
	if name == "" {
		return name, false
	}
	if strings.ContainsRune(name, filepath.Separator) {
		data, err := os.ReadFile(name)
		if err != nil || !isManaged(data) {
			return name, false
		}
		dir := filepath.Dir(name)
		if real, ok := lookPathSkippingDir(filepath.Base(name), dir); ok {
			return real, true
		}
		return name, false
	}
	path, err := lookPath(name)
	if err != nil {
		return name, false
	}
	data, err := os.ReadFile(path)
	if err != nil || !isManaged(data) {
		return name, false
	}
	if real, ok := lookPathSkippingDir(name, filepath.Dir(path)); ok {
		return real, true
	}
	return name, false
}

// ResolveRealStrict resolves name to the executable to run, but unlike
// ResolveReal it returns an error when name resolves ONLY to a ctx-wire-managed
// shim with no real binary behind it. ResolveReal hands the original name back in
// that case; the runner would then exec it and re-enter the shim (a confusing
// "no real X" failure from the shell, or a bounce between duplicate shim dirs).
// The runner uses this to fail cleanly with exit 127 instead. A path or
// hook-rewritten name that points at a real binary passes through unchanged.
func ResolveRealStrict(name string) (string, error) {
	real, _ := ResolveReal(name)
	if resolvesOnlyToShim(real) {
		return "", fmt.Errorf("no real %q found on PATH (only ctx-wire shims); PATH may be missing system dirs such as /usr/bin:/bin, or a duplicate ctx-wire install left stale shims", filepath.Base(real))
	}
	return real, nil
}

// resolvesOnlyToShim reports whether name (a bare command or a path) currently
// resolves to a ctx-wire-managed shim. ResolveReal has already tried and failed
// to find the real binary before this is consulted, so a managed-shim result
// means executing name would re-enter the shim.
func resolvesOnlyToShim(name string) bool {
	if name == "" {
		return false
	}
	path := name
	if !strings.ContainsRune(name, filepath.Separator) {
		p, err := lookPath(name)
		if err != nil {
			return false // not on PATH at all; let exec surface a normal error
		}
		path = p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return isManaged(data)
}

// ResolveRealExe returns the first real (non-shim) executable for a bare command
// name on PATH, skipping every ctx-wire-managed shim. Unlike ResolveReal (which
// unwraps a single hook-rewritten name and falls back to the name itself), this
// is for `run --shim`: it must never hand back a shim, because the shim would
// then re-exec itself. When only ctx-wire shims (or nothing) are found it
// returns an error, which the caller surfaces as exit 127 rather than looping.
func ResolveRealExe(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		for _, ext := range executableExts() {
			candidate := filepath.Join(dir, name+ext)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() || !isExecutable(info) {
				continue
			}
			if isManagedShimFile(candidate) {
				continue // a ctx-wire shim; keep scanning for the real binary
			}
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no real %q on PATH (only ctx-wire shims); check for duplicate installs", name)
}

// RecordUse appends a scrubbed shim-capture entry. It is best-effort by caller
// convention: errors should never break command execution.
func RecordUse(shimName, scrubbedCommand string) error {
	shimName = filepath.Base(strings.TrimSpace(shimName))
	if shimName == "" || scrubbedCommand == "" {
		return nil
	}
	path, err := logPath()
	if err != nil {
		return err
	}
	// 0700: the data dir holds usage logs; keep it private and consistent with
	// the gain log dir (whichever creates it first sets the mode).
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	entry := Use{
		TS:      time.Now().UTC().Format(time.RFC3339Nano),
		Shim:    shimName,
		Command: scrubbedCommand,
	}
	return json.NewEncoder(f).Encode(entry)
}

// UsageSummary returns count and last timestamp for shim captures.
func UsageSummary() (count int, last string, err error) {
	path, err := logPath()
	if err != nil {
		return 0, "", err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var u Use
		if json.Unmarshal(sc.Bytes(), &u) == nil {
			count++
			if u.TS != "" {
				last = u.TS
			}
		}
	}
	return count, last, sc.Err()
}

func shimFileName(cmd string) string {
	if runtime.GOOS == "windows" {
		return cmd + ".cmd"
	}
	return cmd
}

func shimScript(cmd, ctxWire string) string {
	if runtime.GOOS == "windows" {
		// The binary owns gating (detection, opt-outs, recursion backstop) and
		// attribution via `run --shim`, so the .cmd stays a thin launcher. The
		// marker on line 2 keeps install/uninstall scanning working; exit /b
		// propagates the binary's exit code.
		return fmt.Sprintf("@echo off\r\nrem %s\r\n\"%s\" run --shim %s %%*\r\nexit /b %%errorlevel%%\r\n", marker, ctxWire, cmd)
	}
	qCtx := shellQuote(ctxWire)
	qCmd := shellQuote(cmd)
	return fmt.Sprintf(`#!/bin/sh
%s
cmd=%s
ctx_wire=%s
# Compute the shim's own directory using only shell builtins (parameter
# expansion plus the cd/pwd builtins), which need no PATH. Spawning an external
# helper here would abort the shim on a hostile or stripped PATH (one missing
# /usr/bin), the exact failure being fixed: every command then dies with 127.
case $0 in
  */*) shim_dir=${0%%/*} ;;
  *) shim_dir=. ;;
esac
if abs_dir=$(CDPATH= cd -- "$shim_dir" 2>/dev/null && pwd); then
  shim_dir=$abs_dir
fi

# is_ctx_wire_shim reports whether $1 is a ctx-wire-managed shim. It reads only
# the first two lines so it never scans a real (binary) executable: the marker
# sits near the top of every shim and never inside a real tool.
is_ctx_wire_shim() {
  _l1=; _l2=
  { IFS= read -r _l1; IFS= read -r _l2; } < "$1" 2>/dev/null || return 1
  case $_l1$_l2 in
    *'ctx-wire shim'*) return 0 ;;
  esac
  return 1
}

new_path=
old_ifs=$IFS
IFS=:
for part in ${PATH-}; do
  [ -n "$part" ] || part=.
  case "$part" in
    /*) abs=$part ;;
    *) abs=$(CDPATH= cd -- "$part" 2>/dev/null && pwd) || abs=$part ;;
  esac
  if [ "$abs" = "$shim_dir" ]; then
    continue
  fi
  if [ -z "$new_path" ]; then
    new_path=$part
  else
    new_path=$new_path:$part
  fi
done
IFS=$old_ifs
PATH=$new_path
export PATH
real=$(command -v "$cmd" 2>/dev/null) || {
  echo "ctx-wire shim: $cmd not found after removing $shim_dir from PATH" >&2
  exit 127
}
# If command -v resolved to ANOTHER ctx-wire shim (e.g. a second install left on
# PATH by an upgrade), scan PATH for the first non-shim executable instead.
# Without this, two shim dirs can bounce a command between shims without end.
if is_ctx_wire_shim "$real"; then
  real=
  old_ifs=$IFS
  IFS=:
  for part in ${PATH-}; do
    [ -n "$part" ] || part=.
    cand=$part/$cmd
    [ -f "$cand" ] && [ -x "$cand" ] || continue
    if is_ctx_wire_shim "$cand"; then
      continue
    fi
    real=$cand
    break
  done
  IFS=$old_ifs
  if [ -z "$real" ]; then
    echo "ctx-wire shim: no real '$cmd' on PATH (only ctx-wire shims); check for duplicate installs" >&2
    exit 127
  fi
fi
should_wire=0
case "${CTX_WIRE_DISABLE_SHIMS:-}" in
  1|true|TRUE|yes|YES|on|ON)
    exec "$real" "$@"
    ;;
esac
case "${CTX_WIRE_SHIMS:-}" in
  0|false|FALSE|no|NO|off|OFF)
    exec "$real" "$@"
    ;;
esac
case "${CTX_WIRE_AGENT_SHIMS:-}" in
  1|true|TRUE|yes|YES|on|ON) should_wire=1 ;;
esac
case "${CTX_WIRE_SHIMS:-}" in
  1|true|TRUE|yes|YES|on|ON) should_wire=1 ;;
esac
if [ "$should_wire" != 1 ]; then
  # A hook/plugin-capable agent already rewrites model-visible commands, and its
  # subprocesses inherit CTX_WIRE_AGENT. The shim must NOT also wire under it:
  # that double-covers shell plumbing and corrupts command substitutions like
  # result=$(cat file). Pass through. (Keep this set in sync with agent.HookCapable.)
  case "${CTX_WIRE_AGENT:-}" in
    claude|codex|cursor|gemini|copilot|opencode|pi|hermes)
      exec "$real" "$@" ;;
  esac
  ppid=${PPID:-}
  depth=0
  while [ -n "$ppid" ] && [ "$ppid" -gt 1 ] 2>/dev/null && [ "$depth" -lt 12 ]; do
    # comm+args in ONE ps call, matched directly as a string (no parsing), then the
    # parent ppid in a second call. Two ps calls per ancestor instead of the old
    # three. Never word-split or use "set --" here: it would clobber the original
    # "$@" this shim must still exec, and would glob a star in a process's args.
    info=$(ps -o comm= -o args= -p "$ppid" 2>/dev/null || true)
    case "$info" in
      # Hook/plugin-capable ancestor: covered by its own rewrite, so pass through
      # rather than double-wrap (keep in sync with agent.HookCapable).
      *claude*|*codex*|*cursor*|*gemini*|*copilot*|*opencode*|*pi-coding-agent*|*"pi coding agent"*|*/.pi/agent*|*hermes*)
        exec "$real" "$@" ;;
      # Steering-only / opt-in MCP agents have no auto-rewrite: the shim is their
      # only coverage, so wire under them.
      *windsurf*) should_wire=1; detected_agent=windsurf; break ;;
      *cline*) should_wire=1; detected_agent=cline; break ;;
      *kilocode*) should_wire=1; detected_agent=kilocode; break ;;
      *antigravity*) should_wire=1; detected_agent=antigravity; break ;;
      *vscode*|*"Visual Studio Code"*|*"visual studio code"*) should_wire=1; detected_agent=vscode; break ;;
      *visualstudio*|*"Visual Studio"*|*"visual studio"*) should_wire=1; detected_agent=visualstudio; break ;;
      *agent-browser*) should_wire=1; break ;;
    esac
    ppid=$(ps -o ppid= -p "$ppid" 2>/dev/null | tr -d ' ')
    depth=$((depth + 1))
  done
fi
if [ "$should_wire" != 1 ]; then
  exec "$real" "$@"
fi
# Recursion backstop: cap how deep ctx-wire may wrap itself (the counter rides
# in the env across exec). A misconfigured PATH then degrades to running the
# real command directly instead of forking without bound.
wrap_depth=${CTX_WIRE_SHIM_DEPTH:-0}
case $wrap_depth in
  ''|*[!0-9]*) wrap_depth=0 ;;
esac
if [ "$wrap_depth" -ge 10 ]; then
  echo "ctx-wire shim: wrap depth $wrap_depth exceeded for '$cmd'; running it directly (check for duplicate ctx-wire installs on PATH)" >&2
  exec "$real" "$@"
fi
CTX_WIRE_SHIM_DEPTH=$((wrap_depth + 1))
export CTX_WIRE_SHIM_DEPTH
CTX_WIRE_SHIM=$cmd
export CTX_WIRE_SHIM
# Attribute the command to the detected agent, unless an outer hook already set
# CTX_WIRE_AGENT (keep its more specific value rather than overwriting it).
if [ -z "${CTX_WIRE_AGENT:-}" ] && [ -n "${detected_agent:-}" ]; then
  CTX_WIRE_AGENT=$detected_agent
  export CTX_WIRE_AGENT
fi
exec "$ctx_wire" run "$real" "$@"
`, marker, qCmd, qCtx)
}

func writeExecutable(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ctx-wire-shim-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func isManaged(data []byte) bool {
	return strings.Contains(string(data), marker)
}

func normalized(commands []string) []string {
	seen := make(map[string]bool, len(commands))
	var out []string
	for _, cmd := range commands {
		cmd = filepath.Base(strings.TrimSpace(cmd))
		if cmd == "" || cmd == "." || cmd == "ctx-wire" || seen[cmd] {
			continue
		}
		seen[cmd] = true
		out = append(out, cmd)
	}
	sort.Strings(out)
	return out
}

// lookPath finds the managed shim file for cmd on PATH. The shim is named by
// shimFileName (cmd on Unix, cmd.cmd on Windows), so this looks for that exact
// name. Executability is OS-specific (see isExecutable): a Windows .cmd shim has
// no execute mode bit, only its extension.
func lookPath(cmd string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, shimFileName(cmd))
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && isExecutable(info) {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

// lookPathSkippingDir finds the REAL executable for cmd, ignoring the shim's own
// directory so ResolveReal never resolves back to the shim. Unlike lookPath this
// must try every runnable extension (executableExts): the shim is "git.cmd" but
// the real binary is "git.exe", and on Unix the single empty extension keeps the
// behavior identical to a plain name lookup.
func lookPathSkippingDir(cmd, skipDir string) (string, bool) {
	skipDir = cleanPath(skipDir)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		if cleanPath(dir) == skipDir {
			continue
		}
		for _, ext := range executableExts() {
			candidate := filepath.Join(dir, cmd+ext)
			info, err := os.Stat(candidate)
			if err != nil || info.IsDir() || !isExecutable(info) {
				continue
			}
			if isManagedShimFile(candidate) {
				continue // another ctx-wire shim; keep scanning for the real binary
			}
			return candidate, true
		}
	}
	return "", false
}

// isManagedShimFile reports whether the file at path is a ctx-wire-managed shim,
// reading only a small prefix so it never scans a real (possibly large, binary)
// executable. The marker sits in the first line of every shim.
func isManagedShimFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var buf [256]byte
	n, _ := f.Read(buf[:])
	return strings.Contains(string(buf[:n]), marker)
}

// CtxWireBinariesOnPATH returns the distinct ctx-wire executables found on PATH,
// in PATH order. More than one means a stale or duplicate install that can
// shadow the intended binary and contribute to shim recursion; callers surface
// this as a warning.
func CtxWireBinariesOnPATH() []string {
	var bins []string
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, shimFileName("ctx-wire"))
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || !isExecutable(info) {
			continue
		}
		clean := cleanPath(candidate)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		bins = append(bins, candidate)
	}
	return bins
}

// ManagedShimDirsOnPATH returns the distinct directories on PATH that hold at
// least one ctx-wire-managed shim. More than one indicates a stale install
// (commonly left by an upgrade) that can make command resolution bounce between
// shim sets; callers surface this as a warning.
func ManagedShimDirsOnPATH() []string {
	var dirs []string
	seen := map[string]bool{}
	// A dir counts as managed if it holds a managed shim for any command ctx-wire
	// installs now (DefaultCommands) OR used to (DeprecatedShims). Including the
	// deprecated set is what lets the self-heal still visit, and prune from, a dir
	// that holds only a stale deprecated shim such as cat.
	known := append(append([]string{}, DefaultCommands...), DeprecatedShims...)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		clean := cleanPath(dir)
		if seen[clean] {
			continue
		}
		for _, cmd := range known {
			p := filepath.Join(dir, shimFileName(cmd))
			if info, err := os.Stat(p); err == nil && !info.IsDir() && isManagedShimFile(p) {
				seen[clean] = true
				dirs = append(dirs, dir)
				break
			}
		}
	}
	return dirs
}

// ManagedDirsWith returns every directory on PATH that holds ctx-wire-managed
// shims, plus installDir (deduplicated) when non-empty. Callers that report or
// remove shims should operate across ALL of these, not just the install dir, so a
// stale earlier shim dir that is first on PATH (the real source of prompt latency)
// is not missed.
func ManagedDirsWith(installDir string) []string {
	dirs := ManagedShimDirsOnPATH()
	if installDir == "" {
		return dirs
	}
	want := cleanPath(installDir)
	for _, d := range dirs {
		if cleanPath(d) == want {
			return dirs
		}
	}
	return append(dirs, installDir)
}

// AggregateStatus merges Inspect across dirs: a command counts as installed/active
// if it is installed/active in ANY dir, so a shim shadowed in one dir but first on
// PATH from another is reported as active. Uses is the global capture count (the
// same for every dir). total is the number of default commands.
func AggregateStatus(dirs []string) (installed, active, uses, total int) {
	inst := map[string]bool{}
	act := map[string]bool{}
	for _, d := range dirs {
		st := Inspect(d, DefaultCommands)
		for _, c := range st.Installed {
			inst[c] = true
		}
		for _, c := range st.Active {
			act[c] = true
		}
		if st.Uses > uses {
			uses = st.Uses
		}
	}
	return len(inst), len(act), uses, len(DefaultCommands)
}

// keepMarkerName marks a shim dir whose shims were installed DELIBERATELY (a
// steering agent's init or an explicit `shims install`). Advisory code consults it
// so it never nags the user to remove shims they want, and the auto-migration
// never touches an explicitly-requested set. A plain filename, not a shim.
const keepMarkerName = ".ctx-wire-shims-keep"

// MarkKeep records explicit intent to keep shims in dir. Best-effort.
func MarkKeep(dir string) {
	_ = os.WriteFile(filepath.Join(dir, keepMarkerName),
		[]byte("shims installed explicitly; ctx-wire will not advise removing them\n"), 0o644)
}

// ClearKeep drops the keep-marker (on uninstall). Best-effort.
func ClearKeep(dir string) { _ = os.Remove(filepath.Join(dir, keepMarkerName)) }

// WantsKeep reports whether any dir carries the keep-marker.
func WantsKeep(dirs []string) bool {
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, keepMarkerName)); err == nil {
			return true
		}
	}
	return false
}

// RefreshManaged regenerates every ctx-wire-managed shim in the managed shim
// dirs on PATH so they match the CURRENT template. A binary upgrade (manual
// update, auto-update, or curl install) replaces the binary but never the PATH
// shims, so a user who upgraded from a pre-guard release is left with stale
// shims that lack the recursion backstops and can fork-bomb. Healing them here,
// from a human-facing command, closes that gap without a manual re-init.
//
// ctxWire is the absolute path the shims should invoke (the running binary). It
// rewrites ALL managed shim dirs on PATH (so a duplicate install is healed too),
// touches only managed shims whose content differs, and is best-effort: a
// failure on one shim never stops the rest. It returns the count rewritten.
func RefreshManaged(ctxWire string) int {
	absCtxWire, err := filepath.Abs(ctxWire)
	if err != nil {
		return 0
	}
	deprecated := make(map[string]bool, len(DeprecatedShims))
	for _, c := range DeprecatedShims {
		deprecated[c] = true
	}
	refreshed := 0
	for _, dir := range ManagedShimDirsOnPATH() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if !isManagedShimFile(path) {
				continue
			}
			cmd := shimCommandName(e.Name())
			if cmd == "" {
				continue
			}
			// Prune a shim for a command ctx-wire no longer manages instead of
			// refreshing it, so an upgrade actually removes de-listed shims (e.g.
			// cat) from every managed dir on PATH, not just new installs.
			if deprecated[cmd] {
				_ = os.Remove(path)
				continue
			}
			want := shimScript(cmd, absCtxWire)
			if cur, err := os.ReadFile(path); err == nil && string(cur) == want {
				continue // already current
			}
			if writeExecutable(path, []byte(want)) == nil {
				refreshed++
			}
		}
	}
	return refreshed
}

// shimCommandName derives the command name from a managed shim file name,
// stripping the Windows .cmd suffix.
func shimCommandName(fileName string) string {
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(fileName, ".cmd") {
			return ""
		}
		return strings.TrimSuffix(fileName, ".cmd")
	}
	return fileName
}

func samePath(a, b string) bool {
	return cleanPath(a) == cleanPath(b)
}

func cleanPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func logPath() (string, error) {
	if p := os.Getenv(EnvLog); p != "" {
		return p, nil
	}
	base, err := paths.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ctx-wire", "shims.jsonl"), nil
}
