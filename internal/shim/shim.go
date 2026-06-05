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

	marker = "# ctx-wire shim v1"
)

// DefaultCommands are safe, high-value commands to shim. Deliberately exclude
// shells, language runtimes, and destructive/no-output utilities such as rm/cp.
var DefaultCommands = []string{
	"agent-browser",
	"awk",
	"base64",
	"biome",
	"bun",
	"bunx",
	"cargo",
	"cat",
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
	return Uninstall(dir, DefaultCommands)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
		return fmt.Sprintf("@echo off\r\nrem %s\r\nset CTX_WIRE_SHIM=%s\r\n\"%s\" run %s %%*\r\n", marker, cmd, ctxWire, cmd)
	}
	qCtx := shellQuote(ctxWire)
	qCmd := shellQuote(cmd)
	return fmt.Sprintf(`#!/bin/sh
%s
cmd=%s
ctx_wire=%s
shim_dir=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd)

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
  ppid=${PPID:-}
  depth=0
  while [ -n "$ppid" ] && [ "$ppid" -gt 1 ] 2>/dev/null && [ "$depth" -lt 12 ]; do
    comm=$(ps -o comm= -p "$ppid" 2>/dev/null || true)
    args=$(ps -o args= -p "$ppid" 2>/dev/null || true)
    case "$comm $args" in
      *claude*) should_wire=1; detected_agent=claude; break ;;
      *codex*) should_wire=1; detected_agent=codex; break ;;
      *cursor*) should_wire=1; detected_agent=cursor; break ;;
      *windsurf*) should_wire=1; detected_agent=windsurf; break ;;
      *cline*) should_wire=1; detected_agent=cline; break ;;
      *gemini*) should_wire=1; detected_agent=gemini; break ;;
      *copilot*) should_wire=1; detected_agent=copilot; break ;;
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
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		clean := cleanPath(dir)
		if seen[clean] {
			continue
		}
		for _, cmd := range DefaultCommands {
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
