package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallMCPGoldenFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	changed, err := InstallMCP(path)
	if err != nil {
		t.Fatalf("InstallMCP: %v", err)
	}
	if !changed {
		t.Error("expected changed=true for fresh install")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/mcp_servers.golden")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch\n got:\n%s\n want:\n%s", got, want)
	}
}

func TestInstallMCPIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if _, err := InstallMCP(path); err != nil {
		t.Fatalf("first install: %v", err)
	}
	changed, err := InstallMCP(path)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if changed {
		t.Error("expected changed=false on second install")
	}
}

func TestInstallMCPPreservesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	existing := `{
      "inputs": [ { "id": "token", "type": "promptString" } ],
      "servers": { "github": { "url": "https://api.githubcopilot.com/mcp/" } }
    }`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallMCP(path); err != nil {
		t.Fatalf("InstallMCP: %v", err)
	}
	root := readSettings(t, path)
	if _, ok := root["inputs"]; !ok {
		t.Error("existing top-level 'inputs' lost")
	}
	servers := root["servers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Error("existing 'github' server lost")
	}
	if _, ok := servers["ctx-wire"]; !ok {
		t.Error("ctx-wire server not added")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Error("expected a .bak backup")
	}
}

func TestInstallMCPRejectsWrongServersShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(`{"servers":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallMCP(path); err == nil {
		t.Fatal("expected schema error for non-object servers")
	}
}

func TestMCPPaths(t *testing.T) {
	if got := VSCodeMCPPath("/work"); got != filepath.Join("/work", ".vscode", "mcp.json") {
		t.Errorf("VSCodeMCPPath = %q", got)
	}
	vs, err := VisualStudioMCPPath()
	if err != nil {
		t.Fatalf("VisualStudioMCPPath: %v", err)
	}
	if filepath.Base(vs) != ".mcp.json" {
		t.Errorf("VisualStudioMCPPath base = %q, want .mcp.json", filepath.Base(vs))
	}
}
