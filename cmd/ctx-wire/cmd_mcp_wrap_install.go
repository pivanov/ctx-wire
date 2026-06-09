package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultMCPConfigPath is the Claude Code config that holds mcpServers entries.
func defaultMCPConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// mcpWrapInstall rewrites every mcpServers entry named `name` (top-level and per
// project) so the server is launched through `ctx-wire mcp-wrap --`, which
// measures its tools/call token cost. Idempotent (an already-wrapped entry is
// left alone) and reversible (mcpWrapUninstall restores it). The config is
// backed up and written atomically; all other data is preserved.
func mcpWrapInstall(configPath, name string, compress bool) int {
	return mcpWrapEdit(configPath, name, true, compress)
}

func mcpWrapUninstall(configPath, name string) int {
	return mcpWrapEdit(configPath, name, false, false)
}

func mcpWrapEdit(configPath, name string, install, compress bool) int {
	if configPath == "" {
		configPath = defaultMCPConfigPath()
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: cannot read %s: %v\n", configPath, err)
		return 1
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %s is not valid JSON: %v\n", configPath, err)
		return 1
	}

	// Collect the entries to change as (path, new value) patches rather than
	// mutating and re-serializing the whole config (which would reorder keys).
	type patch struct {
		path  []string
		value map[string]any
	}
	var patches []patch
	visit := func(servers map[string]any, prefix []string) {
		sc, ok := servers[name].(map[string]any)
		if !ok {
			return
		}
		var done bool
		if install {
			done = wrapServerEntry(sc, exe, compress)
		} else {
			done = unwrapServerEntry(sc, exe)
		}
		if done {
			path := append(append([]string{}, prefix...), name)
			patches = append(patches, patch{path: path, value: sc})
		}
	}
	if servers, ok := cfg["mcpServers"].(map[string]any); ok {
		visit(servers, []string{"mcpServers"})
	}
	if projects, ok := cfg["projects"].(map[string]any); ok {
		for pk, pc := range projects {
			if pm, ok := pc.(map[string]any); ok {
				if servers, ok := pm["mcpServers"].(map[string]any); ok {
					visit(servers, []string{"projects", pk, "mcpServers"})
				}
			}
		}
	}

	theme := themeForStdout()
	if len(patches) == 0 {
		verb := "wrap"
		if !install {
			verb = "unwrap"
		}
		fmt.Printf("%s no %q mcpServers entry to %s in %s (already in the target state, or not found)\n",
			theme.Dim.Render("mcp-wrap:"), name, verb, configPath)
		return 0
	}

	// Apply each change as a surgical splice into the original bytes so only the
	// touched entries change: the rest of the config (key order, formatting,
	// unrelated servers) is preserved instead of being re-serialized alphabetically.
	out := raw
	for _, p := range patches {
		next, err := patchJSONValue(out, p.path, p.value)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: cannot rewrite %s: %v\n", strings.Join(p.path, "."), err)
			return 1
		}
		out = next
	}
	// Sanity: the result must still parse before we replace anything.
	if json.Unmarshal(out, new(map[string]any)) != nil {
		fmt.Fprintln(os.Stderr, "ctx-wire mcp-wrap: refusing to write malformed config")
		return 1
	}
	// Back up the pristine config once. Never overwrite an existing backup: after
	// install then uninstall, a clobbered backup would hold the wrapped config
	// instead of the user's original.
	bak := configPath + ".ctxw-bak"
	if _, statErr := os.Stat(bak); os.IsNotExist(statErr) {
		if err := os.WriteFile(bak, raw, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: cannot write backup %s: %v\n", bak, err)
			return 1
		}
	}
	if err := atomicWrite(configPath, string(out)); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %v\n", err)
		return 1
	}

	action := "wrapped"
	if !install {
		action = "unwrapped"
	}
	fmt.Printf("%s %s %d %q entr%s in %s (backup: %s)\n",
		theme.Success(), action, len(patches), name, plural(len(patches)), theme.Path.Render(configPath), bak)
	fmt.Printf("%s restart the agent so it picks up the change\n", theme.Dim.Render("then:"))
	return 0
}

// wrapServerEntry rewrites a stdio server entry to launch through ctx-wire
// mcp-wrap. It returns false when there is nothing to do (already wrapped, or no
// command). The original command and args are recoverable from the wrapped form,
// so unwrap needs no separate record.
func wrapServerEntry(sc map[string]any, exe string, compress bool) bool {
	cmd, ok := sc["command"].(string)
	if !ok || cmd == "" {
		return false
	}
	if isWrapped(sc, exe) {
		return false
	}
	origArgs := toStringList(sc["args"])
	newArgs := []any{"mcp-wrap"}
	if compress {
		newArgs = append(newArgs, "--compress")
	}
	newArgs = append(newArgs, "--", cmd)
	for _, a := range origArgs {
		newArgs = append(newArgs, a)
	}
	sc["command"] = exe
	sc["args"] = newArgs
	return true
}

func unwrapServerEntry(sc map[string]any, exe string) bool {
	if !isWrapped(sc, exe) {
		return false
	}
	args := toStringList(sc["args"]) // ["mcp-wrap"(,"--compress"),"--",origCmd,origArgs...]
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep+1 >= len(args) {
		return false
	}
	sc["command"] = args[sep+1]
	rest := []any{}
	for _, a := range args[sep+2:] {
		rest = append(rest, a)
	}
	sc["args"] = rest
	return true
}

// isWrapped reports whether sc is an entry THIS tool wrapped: it launches the
// current ctx-wire executable AND carries the `mcp-wrap [--compress] --` arg
// shape. Requiring the command to match (not just the args) means uninstall never
// rewrites a user's own server that happens to pass `mcp-wrap --` to some other
// program. Both the measurement (`mcp-wrap --`) and compression
// (`mcp-wrap --compress --`) shapes are recognized, so an upgrade unwraps cleanly.
func isWrapped(sc map[string]any, exe string) bool {
	cmd, _ := sc["command"].(string)
	args := toStringList(sc["args"])
	if cmd != exe || len(args) < 3 || args[0] != "mcp-wrap" {
		return false
	}
	return args[1] == "--" || (args[1] == "--compress" && len(args) >= 4 && args[2] == "--")
}

func toStringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// patchJSONValue replaces the JSON value at the given object path inside raw with
// the encoding of value, splicing it into the original bytes so the rest of the
// document is byte-for-byte unchanged. The replacement is indented to match the
// line the value sits on, keeping the diff local to the touched entry.
func patchJSONValue(raw []byte, path []string, value any) ([]byte, error) {
	start, end, err := jsonValueSpan(raw, path)
	if err != nil {
		return nil, err
	}
	lineStart := bytes.LastIndexByte(raw[:start], '\n') + 1 // 0 when on the first line
	indent := leadingWhitespace(raw[lineStart:start])
	enc, err := json.MarshalIndent(value, indent, "  ")
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(raw)-(end-start)+len(enc))
	out = append(out, raw[:start]...)
	out = append(out, enc...)
	out = append(out, raw[end:]...)
	return out, nil
}

func leadingWhitespace(b []byte) string {
	n := 0
	for n < len(b) && (b[n] == ' ' || b[n] == '\t') {
		n++
	}
	return string(b[:n])
}

// jsonValueSpan returns the [start,end) byte range of the JSON value at the given
// object path within raw, located by streaming the document so nothing else is
// disturbed.
func jsonValueSpan(raw []byte, path []string) (int, int, error) {
	if len(path) == 0 {
		return 0, 0, fmt.Errorf("empty path")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	start, end, err := spanFromDecoder(dec, path)
	return int(start), int(end), err
}

func spanFromDecoder(dec *json.Decoder, path []string) (int64, int64, error) {
	t, err := dec.Token()
	if err != nil {
		return 0, 0, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return 0, 0, fmt.Errorf("expected an object while resolving %q", path[0])
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return 0, 0, err
		}
		key, _ := kt.(string)
		if key == path[0] {
			if len(path) == 1 {
				var rm json.RawMessage
				if err := dec.Decode(&rm); err != nil {
					return 0, 0, err
				}
				end := dec.InputOffset()
				return end - int64(len(rm)), end, nil
			}
			return spanFromDecoder(dec, path[1:])
		}
		// Skip this value (object, array, or scalar) and keep scanning.
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return 0, 0, err
		}
	}
	return 0, 0, fmt.Errorf("key %q not found", path[0])
}
