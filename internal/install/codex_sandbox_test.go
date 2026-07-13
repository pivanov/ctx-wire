package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const testRoot = "/home/u/.local/share/ctx-wire"

// decodeWritableRoots returns sandbox_workspace_write.writable_roots and fails
// the test if the config no longer parses as TOML.
func decodeWritableRoots(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		t.Fatalf("config no longer parses: %v\n%s", err, data)
	}
	tbl, _ := raw["sandbox_workspace_write"].(map[string]any)
	arr, _ := tbl["writable_roots"].([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasRoot(roots []string, r string) bool {
	for _, x := range roots {
		if x == r {
			return true
		}
	}
	return false
}

func TestCodexWritableRootCreatesMissingFile(t *testing.T) {
	path := codexEnvPath(t)
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	if got := decodeWritableRoots(t, path); !hasRoot(got, testRoot) {
		t.Fatalf("writable_roots = %v, want to contain %q", got, testRoot)
	}
	if !CodexWritableRootConfigured(path, testRoot) {
		t.Fatal("CodexWritableRootConfigured = false after install")
	}
	if !strings.Contains(mustBytes(t, path), "ctx-wire (managed") {
		t.Fatal("created element is not marked; uninstall could not identify it")
	}
}

func TestCodexWritableRootMissingTableAppends(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "model = \"o3\"\n\n[shell_environment_policy]\nset = { CTX_WIRE_AGENT = \"codex\" }\n")
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	if got := decodeWritableRoots(t, path); !hasRoot(got, testRoot) {
		t.Fatalf("writable_roots = %v, want to contain %q", got, testRoot)
	}
	// Unrelated content survives.
	if b := mustBytes(t, path); !strings.Contains(b, "model = \"o3\"") || !strings.Contains(b, "CTX_WIRE_AGENT") {
		t.Fatalf("append clobbered unrelated content:\n%s", b)
	}
}

func TestCodexWritableRootExistingTableNoKey(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nnetwork_access = false\n")
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	if got := decodeWritableRoots(t, path); !hasRoot(got, testRoot) {
		t.Fatalf("writable_roots = %v, want to contain %q", got, testRoot)
	}
	if !strings.Contains(mustBytes(t, path), "network_access = false") {
		t.Fatal("existing table key was lost")
	}
}

func TestCodexWritableRootExistingMultilineArrayMerges(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nwritable_roots = [\n  \"/srv/data\",\n]\n")
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	got := decodeWritableRoots(t, path)
	if !hasRoot(got, testRoot) || !hasRoot(got, "/srv/data") {
		t.Fatalf("writable_roots = %v, want both /srv/data and %q", got, testRoot)
	}
}

func TestCodexWritableRootInlineArrayFailsOpen(t *testing.T) {
	path := codexEnvPath(t)
	orig := "[sandbox_workspace_write]\nwritable_roots = [\"/srv/data\"]\n"
	writeCodexConfig(t, path, orig)
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxManual {
		t.Fatalf("install = %v, %v; want Manual for an inline array", res, err)
	}
	if mustBytes(t, path) != orig {
		t.Fatal("inline-array fail-open must leave the file untouched")
	}
}

func TestCodexWritableRootRepeatedInitIdempotent(t *testing.T) {
	path := codexEnvPath(t)
	if _, err := InstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	after1 := mustBytes(t, path)
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxNoChange {
		t.Fatalf("second install = %v, %v; want NoChange", res, err)
	}
	if mustBytes(t, path) != after1 {
		t.Fatal("idempotent re-init must not rewrite the file")
	}
	if roots := decodeWritableRoots(t, path); len(roots) != 1 {
		t.Fatalf("writable_roots = %v, want exactly one entry", roots)
	}
}

func TestCodexWritableRootUserManagedPreexisting(t *testing.T) {
	path := codexEnvPath(t)
	// User added our exact path themselves, without the ctx-wire marker.
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nwritable_roots = [\n  \""+testRoot+"\",\n]\n")
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxNoChange {
		t.Fatalf("install = %v, %v; want NoChange (already present)", res, err)
	}
	// Uninstall must NOT remove a root the user manages.
	ures, err := UninstallCodexWritableRoot(path, testRoot)
	if err != nil || ures != CodexSandboxNoChange {
		t.Fatalf("uninstall = %v, %v; want NoChange (user-managed)", ures, err)
	}
	if got := decodeWritableRoots(t, path); !hasRoot(got, testRoot) {
		t.Fatalf("uninstall removed a user-managed root: %v", got)
	}
}

func TestCodexWritableRootNewPermissionsConflict(t *testing.T) {
	for _, key := range []string{"[permissions]\ndefault = \"allow\"\n", "default_permissions = \"read-only\"\n"} {
		path := codexEnvPath(t)
		writeCodexConfig(t, path, key)
		res, err := InstallCodexWritableRoot(path, testRoot)
		if err != nil || res != CodexSandboxConflict {
			t.Fatalf("install with %q = %v, %v; want Conflict", key, res, err)
		}
		if mustBytes(t, path) != key {
			t.Fatalf("conflict must leave the file untouched, got:\n%s", mustBytes(t, path))
		}
	}
}

func TestCodexWritableRootUninstallRemovesOnlyOurs(t *testing.T) {
	path := codexEnvPath(t)
	// User-created table with a user root; ctx-wire appends its own.
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nnetwork_access = false\nwritable_roots = [\n  \"/srv/data\",\n]\n")
	if _, err := InstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	ures, err := UninstallCodexWritableRoot(path, testRoot)
	if err != nil || ures != CodexSandboxUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", ures, err)
	}
	got := decodeWritableRoots(t, path)
	if hasRoot(got, testRoot) {
		t.Fatalf("uninstall left our root behind: %v", got)
	}
	if !hasRoot(got, "/srv/data") {
		t.Fatalf("uninstall removed the user's root: %v", got)
	}
	// The user's table and unrelated key survive.
	if b := mustBytes(t, path); !strings.Contains(b, "[sandbox_workspace_write]") || !strings.Contains(b, "network_access = false") {
		t.Fatalf("uninstall damaged the user's table:\n%s", b)
	}
}

func TestCodexWritableRootRoundTripFreshFileCollapses(t *testing.T) {
	path := codexEnvPath(t)
	if _, err := InstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	ures, err := UninstallCodexWritableRoot(path, testRoot)
	if err != nil || ures != CodexSandboxUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", ures, err)
	}
	// The table ctx-wire created (and its create-comment) is gone.
	if b := mustBytes(t, path); strings.Contains(b, "sandbox_workspace_write") || strings.Contains(b, "ctx-wire") {
		t.Fatalf("round-trip left residue:\n%q", b)
	}
	if CodexWritableRootConfigured(path, testRoot) {
		t.Fatal("root still configured after uninstall")
	}
}

func TestCodexWritableRootUninstallMissingFile(t *testing.T) {
	path := codexEnvPath(t)
	res, err := UninstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxNoChange {
		t.Fatalf("uninstall missing file = %v, %v; want NoChange", res, err)
	}
}

func TestCodexWritableRootWindowsPathQuoted(t *testing.T) {
	const winRoot = `C:\Users\u\AppData\Local\ctx-wire`
	path := codexEnvPath(t)
	res, err := InstallCodexWritableRoot(path, winRoot)
	if err != nil || res != CodexSandboxUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	// The raw path must be backslash-escaped in the file, but decode back to the
	// exact path (a naive interpolation would produce invalid TOML: \U...).
	if !strings.Contains(mustBytes(t, path), `\\`) {
		t.Fatalf("windows path was not TOML-escaped:\n%s", mustBytes(t, path))
	}
	if got := decodeWritableRoots(t, path); !hasRoot(got, winRoot) {
		t.Fatalf("writable_roots = %v, want to contain %q", got, winRoot)
	}
	// Round-trips cleanly with the escaped-path marker regex.
	if res, err := UninstallCodexWritableRoot(path, winRoot); err != nil || res != CodexSandboxUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", res, err)
	}
	if CodexWritableRootConfigured(path, winRoot) {
		t.Fatal("windows root still present after uninstall")
	}
}

func TestCodexWritableRootExactMarkerOnly(t *testing.T) {
	path := codexEnvPath(t)
	// User has our exact path with a comment that merely mentions ctx-wire but
	// is NOT the managed marker. Uninstall must not treat it as ours.
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nwritable_roots = [\n  \""+testRoot+"\", # ctx-wire is neat\n]\n")
	res, err := UninstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxNoChange {
		t.Fatalf("uninstall = %v, %v; want NoChange (comment is not the exact marker)", res, err)
	}
	if got := decodeWritableRoots(t, path); !hasRoot(got, testRoot) {
		t.Fatalf("uninstall removed a non-managed root: %v", got)
	}
}

func TestCodexWritableRootPreservesUserEmptyArray(t *testing.T) {
	path := codexEnvPath(t)
	// User owns an (empty) multiline array; ctx-wire appends into it.
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nwritable_roots = [\n]\n")
	if _, err := InstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	if res, err := UninstallCodexWritableRoot(path, testRoot); err != nil || res != CodexSandboxUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", res, err)
	}
	// The user's writable_roots key survives (ctx-wire did not own the array).
	b := mustBytes(t, path)
	if !strings.Contains(b, "writable_roots") {
		t.Fatalf("uninstall deleted the user's writable_roots key:\n%s", b)
	}
	if got := decodeWritableRoots(t, path); len(got) != 0 {
		t.Fatalf("writable_roots = %v, want empty", got)
	}
}

func TestCodexWritableRootUninstallScopedToSandbox(t *testing.T) {
	path := codexEnvPath(t)
	// An unrelated table also has an (empty) writable_roots; uninstall must not
	// scan into it.
	writeCodexConfig(t, path, "[other]\nwritable_roots = [\n]\n\n[sandbox_workspace_write]\nnetwork_access = false\n")
	if _, err := InstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	// The unrelated [other] table and its empty array are untouched.
	if b := mustBytes(t, path); !strings.Contains(b, "[other]") || !strings.Contains(b, "writable_roots") {
		t.Fatalf("uninstall damaged an unrelated table:\n%s", b)
	}
}

func TestCodexWritableRootAddArrayUninstallKeepsUserTable(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[sandbox_workspace_write]\nnetwork_access = false\n")
	if _, err := InstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCodexWritableRoot(path, testRoot); err != nil {
		t.Fatal(err)
	}
	b := mustBytes(t, path)
	if !strings.Contains(b, "[sandbox_workspace_write]") || !strings.Contains(b, "network_access = false") {
		t.Fatalf("uninstall removed the user's table/keys:\n%s", b)
	}
	if strings.Contains(b, "writable_roots") {
		t.Fatalf("uninstall left the array ctx-wire created:\n%s", b)
	}
}

func TestCodexWritableRootFreshBothWritersNoResidue(t *testing.T) {
	path := codexEnvPath(t)
	// Fresh install of BOTH ctx-wire writers, as `init codex` does.
	if _, err := InstallCodexAgentEnv(path); err != nil {
		t.Fatal(err)
	}
	root := testRoot
	if _, err := InstallCodexWritableRoot(path, root); err != nil {
		t.Fatal(err)
	}
	// Uninstall in the registry order: sandbox root first, then agent env.
	if _, err := UninstallCodexWritableRoot(path, root); err != nil {
		t.Fatal(err)
	}
	if _, err := UninstallCodexAgentEnv(path); err != nil {
		t.Fatal(err)
	}
	b := strings.TrimSpace(mustBytes(t, path))
	if strings.Contains(b, "ctx-wire") || strings.Contains(b, "sandbox_workspace_write") || strings.Contains(b, "shell_environment_policy") {
		t.Fatalf("fresh both-writers round trip left residue:\n%q", b)
	}
}

// writeCodexHookFile writes a structurally valid hooks.json carrying ctx-wire's
// Bash hook, so CodexHookInstalled recognizes it.
func writeCodexHookFile(t *testing.T, path string) {
	t.Helper()
	h := `{"hooks":{"preToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"` + codexHookCommand + `"}]}]}}`
	if err := os.WriteFile(path, []byte(h), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncCodexWritableRootOnUpdate(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	hooksPath := filepath.Join(codexHome, "hooks.json")
	cfgPath := filepath.Join(codexHome, "config.toml")
	root, _ := CodexWritableRoot()

	// No ctx-wire hook: settled (nothing to do), nothing written.
	if !SyncCodexWritableRootOnUpdate() {
		t.Fatal("no-hook case should be settled")
	}

	// Hook installed, but no config.toml yet: settled, and no config created.
	writeCodexHookFile(t, hooksPath)
	if !SyncCodexWritableRootOnUpdate() {
		t.Fatal("hook-but-no-config case should be settled")
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatal("sync silently created a config.toml on update")
	}

	// Config now exists: sync grants the root and is settled.
	if err := os.WriteFile(cfgPath, []byte("model = \"o3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !SyncCodexWritableRootOnUpdate() {
		t.Fatal("grant write should be settled")
	}
	if !CodexWritableRootConfigured(cfgPath, root) {
		t.Fatal("root not configured after sync")
	}
	// Idempotent + settled: no duplicate root.
	if !SyncCodexWritableRootOnUpdate() {
		t.Fatal("already-granted case should be settled")
	}
	if n := strings.Count(mustBytes(t, cfgPath), root); n != 1 {
		t.Fatalf("root appears %d times, want 1", n)
	}
}

func TestSyncCodexWritableRootOnUpdateSkipsPermissionsConfig(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writeCodexHookFile(t, filepath.Join(codexHome, "hooks.json"))
	cfgPath := filepath.Join(codexHome, "config.toml")
	orig := "[permissions]\ndefault = \"allow\"\n"
	if err := os.WriteFile(cfgPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	// Recognized codex, but a [permissions] config: skipped, and still settled
	// (retrying would not help a stable shape).
	if !SyncCodexWritableRootOnUpdate() {
		t.Fatal("[permissions] skip should be settled")
	}
	if mustBytes(t, cfgPath) != orig {
		t.Fatalf("sync must leave a [permissions] config untouched:\n%s", mustBytes(t, cfgPath))
	}
}

func TestSyncCodexWritableRootOnUpdateEnvOptOut(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("CTX_WIRE_NO_CODEX_SANDBOX_SYNC", "1")
	writeCodexHookFile(t, filepath.Join(codexHome, "hooks.json"))
	cfgPath := filepath.Join(codexHome, "config.toml")
	orig := "model = \"o3\"\n"
	if err := os.WriteFile(cfgPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	// Otherwise fully eligible (hook + config, no grant), but opted out: the
	// migration is a settled no-op and the config is untouched.
	if !SyncCodexWritableRootOnUpdate() {
		t.Fatal("opt-out should be settled (no retry)")
	}
	if mustBytes(t, cfgPath) != orig {
		t.Fatalf("opt-out must not edit config.toml:\n%s", mustBytes(t, cfgPath))
	}
	root, _ := CodexWritableRoot()
	if CodexWritableRootConfigured(cfgPath, root) {
		t.Fatal("opt-out granted the root anyway")
	}
}

func TestSyncCodexWritableRootOnUpdateUnsettledOnWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based write denial is a no-op for root")
	}
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	writeCodexHookFile(t, filepath.Join(codexHome, "hooks.json"))
	cfgPath := filepath.Join(codexHome, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("model = \"o3\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deny writes in the config dir so the atomic temp-create fails, as a codex
	// sandbox would. The grant must not land, and the outcome must be unsettled.
	if err := os.Chmod(codexHome, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(codexHome, 0o700) })
	if SyncCodexWritableRootOnUpdate() {
		t.Fatal("a denied write must be unsettled so the caller retries")
	}
	_ = os.Chmod(codexHome, 0o700)
	root, _ := CodexWritableRoot()
	if CodexWritableRootConfigured(cfgPath, root) {
		t.Fatal("grant landed despite the write being denied")
	}
}

func TestCodexWritableRootAmbiguousDoubleTableFailsOpen(t *testing.T) {
	path := codexEnvPath(t)
	orig := "[sandbox_workspace_write]\nnetwork_access = false\n\n[sandbox_workspace_write]\nnetwork_access = true\n"
	writeCodexConfig(t, path, orig)
	// A doubled header is invalid TOML, so decode fails first: Manual, untouched.
	res, err := InstallCodexWritableRoot(path, testRoot)
	if err != nil || res != CodexSandboxManual {
		t.Fatalf("install = %v, %v; want Manual for a doubled table", res, err)
	}
	if mustBytes(t, path) != orig {
		t.Fatal("ambiguous fail-open must leave the file untouched")
	}
}
