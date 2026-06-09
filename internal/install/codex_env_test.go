package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func codexEnvPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.toml")
}

func writeCodexConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func decodeCodexSet(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		t.Fatalf("config no longer parses: %v\n%s", err, data)
	}
	policy, _ := raw["shell_environment_policy"].(map[string]any)
	set, _ := policy["set"].(map[string]any)
	return set
}

func mustBytes(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCodexAgentEnvCreatesMissingFile(t *testing.T) {
	path := codexEnvPath(t)
	res, err := InstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	if got := decodeCodexSet(t, path)[CodexAgentEnvKey]; got != CodexAgentEnvValue {
		t.Fatalf("CTX_WIRE_AGENT = %v, want %q", got, CodexAgentEnvValue)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("no .bak expected when creating a fresh file")
	}
}

func TestCodexAgentEnvMergesInlineTable(t *testing.T) {
	path := codexEnvPath(t)
	orig := "# my codex config\nmodel = \"o4\"\n\n[shell_environment_policy]\ninherit = \"all\"  # keep env\nset = { FOO = \"bar\" }\n\n[features]\nhooks = true\n"
	writeCodexConfig(t, path, orig)

	res, err := InstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	set := decodeCodexSet(t, path)
	if set["FOO"] != "bar" || set[CodexAgentEnvKey] != CodexAgentEnvValue {
		t.Fatalf("set = %v; want FOO and CTX_WIRE_AGENT merged", set)
	}
	out := mustBytes(t, path)
	for _, keep := range []string{"# my codex config", "inherit = \"all\"  # keep env", "hooks = true"} {
		if !strings.Contains(out, keep) {
			t.Errorf("surgical edit lost %q:\n%s", keep, out)
		}
	}
	if bak := mustBytes(t, path+".bak"); bak != orig {
		t.Error(".bak does not hold the original contents")
	}
}

func TestCodexAgentEnvMergesSectionForm(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[shell_environment_policy.set]\nFOO = \"bar\"\n")

	res, err := InstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	set := decodeCodexSet(t, path)
	if set["FOO"] != "bar" || set[CodexAgentEnvKey] != CodexAgentEnvValue {
		t.Fatalf("set = %v; want FOO and CTX_WIRE_AGENT merged", set)
	}
}

func TestCodexAgentEnvInsertsSetIntoExistingPolicy(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[shell_environment_policy]\ninherit = \"core\"\n")

	res, err := InstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	set := decodeCodexSet(t, path)
	if set[CodexAgentEnvKey] != CodexAgentEnvValue {
		t.Fatalf("set = %v; want CTX_WIRE_AGENT added", set)
	}
	if !strings.Contains(mustBytes(t, path), "inherit = \"core\"") {
		t.Error("inherit key lost")
	}
}

func TestCodexAgentEnvAppendsWhenNoPolicy(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "model = \"o4\"\n")

	res, err := InstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v; want Updated", res, err)
	}
	if got := decodeCodexSet(t, path)[CodexAgentEnvKey]; got != CodexAgentEnvValue {
		t.Fatalf("CTX_WIRE_AGENT = %v, want %q", got, CodexAgentEnvValue)
	}
}

func TestCodexAgentEnvIdempotent(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[shell_environment_policy]\nset = { FOO = \"bar\" }\n")
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("first install = %v, %v", res, err)
	}
	after := mustBytes(t, path)
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvNoChange {
		t.Fatalf("second install = %v, %v; want NoChange", res, err)
	}
	if mustBytes(t, path) != after {
		t.Error("second install changed the file")
	}
}

func TestCodexAgentEnvInstallPreservesForeignValue(t *testing.T) {
	path := codexEnvPath(t)
	orig := "[shell_environment_policy]\nset = { CTX_WIRE_AGENT = \"custom\" }\n"
	writeCodexConfig(t, path, orig)
	res, err := InstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUserManaged {
		t.Fatalf("install = %v, %v; want UserManaged", res, err)
	}
	if mustBytes(t, path) != orig {
		t.Error("install must not touch a user-modified value")
	}
}

func TestCodexAgentEnvFailsOpenOnAmbiguousShapes(t *testing.T) {
	cases := map[string]string{
		"brace in value":    "[shell_environment_policy]\nset = { FOO = \"a{b\" }\n",
		"dotted, no header": "shell_environment_policy.inherit = \"all\"\n",
		"malformed":         "[shell_environment_policy\nset = oops\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := codexEnvPath(t)
			writeCodexConfig(t, path, content)
			res, err := InstallCodexAgentEnv(path)
			if err != nil || res != CodexEnvManual {
				t.Fatalf("install = %v, %v; want Manual (fail open)", res, err)
			}
			if mustBytes(t, path) != content {
				t.Error("fail-open must leave the file byte-identical")
			}
		})
	}
}

func TestCodexAgentEnvUninstallInline(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[shell_environment_policy]\nset = { FOO = \"bar\" }\n")
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v", res, err)
	}
	res, err := UninstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", res, err)
	}
	set := decodeCodexSet(t, path)
	if _, ours := set[CodexAgentEnvKey]; ours {
		t.Error("CTX_WIRE_AGENT still present after uninstall")
	}
	if set["FOO"] != "bar" {
		t.Errorf("foreign key lost on uninstall: set = %v", set)
	}
}

func TestCodexAgentEnvUninstallSoleKeyDropsSetLine(t *testing.T) {
	path := codexEnvPath(t)
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v", res, err)
	}
	res, err := UninstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", res, err)
	}
	out := mustBytes(t, path)
	if strings.Contains(out, "set =") || strings.Contains(out, CodexAgentEnvKey) {
		t.Errorf("sole-key set line should be gone:\n%s", out)
	}
	if strings.Contains(out, "[shell_environment_policy]") {
		t.Errorf("empty policy header is residue; should be cleaned up:\n%s", out)
	}
	decodeCodexSet(t, path) // file must still parse
}

func TestCodexAgentEnvRoundTripLeavesNoResidue(t *testing.T) {
	path := codexEnvPath(t)
	orig := "model = \"o4\"\n\n[mcp_servers.node_repl]\ncommand = \"node\"\n"
	writeCodexConfig(t, path, orig)
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v", res, err)
	}
	if res, err := UninstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("uninstall = %v, %v", res, err)
	}
	if got := mustBytes(t, path); got != orig {
		t.Errorf("install+uninstall round trip is not byte-identical:\norig:\n%s\ngot:\n%s", orig, got)
	}
}

func TestCodexAgentEnvUninstallSectionForm(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "[shell_environment_policy.set]\nFOO = \"bar\"\n")
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v", res, err)
	}
	res, err := UninstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUpdated {
		t.Fatalf("uninstall = %v, %v; want Updated", res, err)
	}
	set := decodeCodexSet(t, path)
	if _, ours := set[CodexAgentEnvKey]; ours {
		t.Error("CTX_WIRE_AGENT still present after uninstall")
	}
	if set["FOO"] != "bar" {
		t.Errorf("foreign key lost on uninstall: set = %v", set)
	}
}

func TestCodexAgentEnvUninstallSkipsUserModified(t *testing.T) {
	path := codexEnvPath(t)
	orig := "[shell_environment_policy]\nset = { CTX_WIRE_AGENT = \"custom\" }\n"
	writeCodexConfig(t, path, orig)
	res, err := UninstallCodexAgentEnv(path)
	if err != nil || res != CodexEnvUserManaged {
		t.Fatalf("uninstall = %v, %v; want UserManaged", res, err)
	}
	if mustBytes(t, path) != orig {
		t.Error("uninstall must not touch a user-modified value")
	}
}

func TestCodexAgentEnvUninstallNoChangeWhenAbsent(t *testing.T) {
	path := codexEnvPath(t)
	writeCodexConfig(t, path, "model = \"o4\"\n")
	if res, err := UninstallCodexAgentEnv(path); err != nil || res != CodexEnvNoChange {
		t.Fatalf("uninstall = %v, %v; want NoChange", res, err)
	}
	if res, err := UninstallCodexAgentEnv(filepath.Join(t.TempDir(), "missing.toml")); err != nil || res != CodexEnvNoChange {
		t.Fatalf("uninstall missing file = %v, %v; want NoChange", res, err)
	}
}

func TestCodexAgentEnvWriteGuardRefusesWrongState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	// wantOurs=true with lines that do not carry our key: must refuse.
	res, err := codexWriteLines(path, []string{"model = \"o4\"", ""}, nil, true)
	if res != CodexEnvManual || err == nil {
		t.Fatalf("guard did not refuse missing key: res=%v err=%v", res, err)
	}
	// wantOurs=false with our key present: must refuse.
	res, err = codexWriteLines(path, []string{"[shell_environment_policy]", "set = { CTX_WIRE_AGENT = \"codex\" }", ""}, nil, false)
	if res != CodexEnvManual || err == nil {
		t.Fatalf("guard did not refuse lingering key: res=%v err=%v", res, err)
	}
	if _, serr := os.Stat(path); serr == nil {
		t.Error("refused writes must not create the file")
	}
}

func TestCodexAgentEnvConfiguredReadsState(t *testing.T) {
	path := codexEnvPath(t)
	if CodexAgentEnvConfigured(path) {
		t.Error("missing file should not report configured")
	}
	if res, err := InstallCodexAgentEnv(path); err != nil || res != CodexEnvUpdated {
		t.Fatalf("install = %v, %v", res, err)
	}
	if !CodexAgentEnvConfigured(path) {
		t.Error("installed config should report configured")
	}
}
