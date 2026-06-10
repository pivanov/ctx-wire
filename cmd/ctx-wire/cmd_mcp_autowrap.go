package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Auto-wrap: `ctx-wire init claude` puts the mcp-wrap --compress relay in front
// of known snapshot-heavy stdio MCP servers, so browser-snapshot compression is
// zero-config. Scoping is deliberate and measure-first: only servers whose
// snapshot output the reducers are built and tested for are wrapped, never
// every stdio server. The change is printed (never silent), `--no-mcp` skips
// it, and `ctx-wire uninstall` reverts every wrap.

// snapshotHeavyMCPMarkers identify the servers whose tool output the
// mcpcompress reducers positively handle (chrome-devtools take_snapshot and
// Playwright browser_snapshot). A server matches when its command or any arg
// contains a marker.
var snapshotHeavyMCPMarkers = []string{"chrome-devtools-mcp", "@playwright/mcp"}

// isSnapshotHeavyStdioServer reports whether sc is a stdio server entry whose
// launch line names a known snapshot-heavy MCP server. Entries without a
// command (http/sse remotes) and entries with an explicit non-stdio type are
// never candidates, and an entry already launched through any ctx-wire
// mcp-wrap (even a stale binary path) is left to the upgrade path instead of
// being double-wrapped.
func isSnapshotHeavyStdioServer(sc map[string]any) bool {
	cmd, _ := sc["command"].(string)
	if cmd == "" {
		return false
	}
	if typ, _ := sc["type"].(string); typ != "" && typ != "stdio" {
		return false
	}
	if looksCtxWireWrapped(sc) {
		// Some ctx-wire (current or stale path) already relays this server.
		// wrapServerEntry upgrades the current-exe form; a stale-path form is
		// surfaced by doctor rather than stacked into a relay chain here.
		cur, err := os.Executable()
		if err == nil {
			if resolved, rerr := filepath.EvalSymlinks(cur); rerr == nil {
				cur = resolved
			}
		}
		if cmd != cur {
			return false
		}
	}
	probe := strings.ToLower(cmd + " " + strings.Join(toStringList(sc["args"]), " "))
	for _, marker := range snapshotHeavyMCPMarkers {
		if strings.Contains(probe, marker) {
			return true
		}
	}
	return false
}

// looksCtxWireWrapped is a broader test than isWrapped: it recognizes the
// mcp-wrap arg shape under ANY ctx-wire binary path, so auto-wrap never builds
// a relay chain on top of a wrap made by an older install.
func looksCtxWireWrapped(sc map[string]any) bool {
	cmd, _ := sc["command"].(string)
	args := toStringList(sc["args"])
	return strings.HasSuffix(filepath.Base(cmd), "ctx-wire") && len(args) > 0 && args[0] == "mcp-wrap"
}

// mcpWrapApply loads configPath, offers every mcpServers entry (top level and
// per project) to edit, and when edit reports a change splices ONLY those
// entries back into the original bytes (key order and unrelated data
// preserved), backing up the pristine config once and writing atomically.
// It returns the names of the entries that changed.
func mcpWrapApply(configPath string, edit func(name string, sc map[string]any) bool) ([]string, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", configPath, err)
	}

	type patch struct {
		path  []string
		value map[string]any
	}
	var patches []patch
	var changed []string
	visit := func(servers map[string]any, prefix []string) {
		// Deterministic order so output and tests are stable.
		names := make([]string, 0, len(servers))
		for n := range servers {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sc, ok := servers[n].(map[string]any)
			if !ok {
				continue // unrecognized shape: never touched
			}
			if edit(n, sc) {
				path := append(append([]string{}, prefix...), n)
				patches = append(patches, patch{path: path, value: sc})
				changed = append(changed, n)
			}
		}
	}
	if servers, ok := cfg["mcpServers"].(map[string]any); ok {
		visit(servers, []string{"mcpServers"})
	}
	if projects, ok := cfg["projects"].(map[string]any); ok {
		pks := make([]string, 0, len(projects))
		for pk := range projects {
			pks = append(pks, pk)
		}
		sort.Strings(pks)
		for _, pk := range pks {
			if pm, ok := projects[pk].(map[string]any); ok {
				if servers, ok := pm["mcpServers"].(map[string]any); ok {
					visit(servers, []string{"projects", pk, "mcpServers"})
				}
			}
		}
	}
	if len(patches) == 0 {
		return nil, nil
	}

	out := raw
	for _, p := range patches {
		next, perr := patchJSONValue(out, p.path, p.value)
		if perr != nil {
			return nil, fmt.Errorf("cannot rewrite %s: %w", strings.Join(p.path, "."), perr)
		}
		out = next
	}
	if json.Unmarshal(out, new(map[string]any)) != nil {
		return nil, fmt.Errorf("refusing to write malformed config")
	}
	// Back up the pristine config once; never clobber an earlier backup (it may
	// hold the user's true original from before the first wrap).
	bak := configPath + ".ctxw-bak"
	if _, statErr := os.Stat(bak); os.IsNotExist(statErr) {
		if err := os.WriteFile(bak, raw, 0o600); err != nil {
			return nil, fmt.Errorf("cannot write backup %s: %w", bak, err)
		}
	}
	if err := atomicWrite(configPath, string(out)); err != nil {
		return nil, err
	}
	return dedupeStrings(changed), nil
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// autoWrapSnapshotMCP wraps every known snapshot-heavy stdio server in the
// config with `mcp-wrap --compress` (upgrading measurement-only wraps in
// place). A missing config is not an error: there is simply nothing to wrap.
func autoWrapSnapshotMCP(configPath string) ([]string, error) {
	if configPath == "" {
		configPath = defaultMCPConfigPath()
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return mcpWrapApply(configPath, func(name string, sc map[string]any) bool {
		if !isSnapshotHeavyStdioServer(sc) {
			return false
		}
		return wrapServerEntry(sc, exe, true)
	})
}

// unwrapAllCtxWireMCP reverts every entry this ctx-wire wrapped (measurement or
// compression form), restoring the original command and args. Used by
// `ctx-wire uninstall` so removing ctx-wire never leaves an MCP config
// pointing at a binary that is about to disappear.
func unwrapAllCtxWireMCP(configPath string) ([]string, error) {
	if configPath == "" {
		configPath = defaultMCPConfigPath()
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	return mcpWrapApply(configPath, func(name string, sc map[string]any) bool {
		return unwrapServerEntry(sc, exe)
	})
}
