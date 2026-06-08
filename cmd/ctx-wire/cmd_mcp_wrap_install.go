package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
func mcpWrapInstall(configPath, name string) int {
	return mcpWrapEdit(configPath, name, true)
}

func mcpWrapUninstall(configPath, name string) int {
	return mcpWrapEdit(configPath, name, false)
}

func mcpWrapEdit(configPath, name string, install bool) int {
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

	changed := 0
	visit := func(servers map[string]any) {
		sc, ok := servers[name].(map[string]any)
		if !ok {
			return
		}
		if install {
			if wrapServerEntry(sc, exe) {
				changed++
			}
		} else if unwrapServerEntry(sc, exe) {
			changed++
		}
	}
	if servers, ok := cfg["mcpServers"].(map[string]any); ok {
		visit(servers)
	}
	if projects, ok := cfg["projects"].(map[string]any); ok {
		for _, pc := range projects {
			if pm, ok := pc.(map[string]any); ok {
				if servers, ok := pm["mcpServers"].(map[string]any); ok {
					visit(servers)
				}
			}
		}
	}

	theme := themeForStdout()
	if changed == 0 {
		verb := "wrap"
		if !install {
			verb = "unwrap"
		}
		fmt.Printf("%s no %q mcpServers entry to %s in %s (already in the target state, or not found)\n",
			theme.Dim.Render("mcp-wrap:"), name, verb, configPath)
		return 0
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %v\n", err)
		return 1
	}
	// Sanity: the re-encoded config must still parse before we replace anything.
	if json.Unmarshal(out, new(map[string]any)) != nil {
		fmt.Fprintln(os.Stderr, "ctx-wire mcp-wrap: refusing to write malformed config")
		return 1
	}
	bak := configPath + ".ctxw-bak"
	if err := os.WriteFile(bak, raw, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: cannot write backup %s: %v\n", bak, err)
		return 1
	}
	if err := atomicWrite(configPath, string(out)+"\n"); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire mcp-wrap: %v\n", err)
		return 1
	}

	action := "wrapped"
	if !install {
		action = "unwrapped"
	}
	fmt.Printf("%s %s %d %q entr%s in %s (backup: %s)\n",
		theme.Success(), action, changed, name, plural(changed), theme.Path.Render(configPath), bak)
	fmt.Printf("%s restart the agent so it picks up the change\n", theme.Dim.Render("then:"))
	return 0
}

// wrapServerEntry rewrites a stdio server entry to launch through ctx-wire
// mcp-wrap. It returns false when there is nothing to do (already wrapped, or no
// command). The original command and args are recoverable from the wrapped form,
// so unwrap needs no separate record.
func wrapServerEntry(sc map[string]any, exe string) bool {
	cmd, ok := sc["command"].(string)
	if !ok || cmd == "" {
		return false
	}
	if isWrapped(sc) {
		return false
	}
	origArgs := toStringList(sc["args"])
	newArgs := []any{"mcp-wrap", "--", cmd}
	for _, a := range origArgs {
		newArgs = append(newArgs, a)
	}
	sc["command"] = exe
	sc["args"] = newArgs
	return true
}

func unwrapServerEntry(sc map[string]any, exe string) bool {
	if !isWrapped(sc) {
		return false
	}
	args := toStringList(sc["args"]) // ["mcp-wrap","--",origCmd, origArgs...]
	sc["command"] = args[2]
	rest := []any{}
	for _, a := range args[3:] {
		rest = append(rest, a)
	}
	sc["args"] = rest
	return true
}

func isWrapped(sc map[string]any) bool {
	args := toStringList(sc["args"])
	return len(args) >= 3 && args[0] == "mcp-wrap" && args[1] == "--"
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
