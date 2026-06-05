package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPi(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pi", "agent", "extensions", "ctx-wire.ts")
	changed, err := InstallPi(path)
	if err != nil || !changed {
		t.Fatalf("InstallPi = (%v, %v), want (true, nil)", changed, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pi extension not written: %v", err)
	}
	if !strings.Contains(string(data), "ctx-wire") || !strings.Contains(string(data), "tool_call") {
		t.Errorf("pi extension content unexpected:\n%s", data)
	}
	if changed, err := InstallPi(path); err != nil || changed {
		t.Errorf("second InstallPi: changed=%v err=%v, want (false, nil)", changed, err)
	}
}

func TestInstallHermes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".hermes", "plugins", "ctx-wire-rewrite")
	changed, err := InstallHermes(dir)
	if err != nil || !changed {
		t.Fatalf("InstallHermes = (%v, %v), want (true, nil)", changed, err)
	}
	initPy, err := os.ReadFile(filepath.Join(dir, "__init__.py"))
	if err != nil {
		t.Fatalf("__init__.py not written: %v", err)
	}
	if !strings.Contains(string(initPy), "pre_tool_call") || !strings.Contains(string(initPy), "ctx-wire") {
		t.Errorf("hermes __init__.py unexpected:\n%s", initPy)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "plugin.yaml"))
	if err != nil {
		t.Fatalf("plugin.yaml not written: %v", err)
	}
	if !strings.Contains(string(manifest), "ctx-wire-rewrite") {
		t.Errorf("hermes manifest unexpected:\n%s", manifest)
	}
	if changed, err := InstallHermes(dir); err != nil || changed {
		t.Errorf("second InstallHermes: changed=%v err=%v, want (false, nil)", changed, err)
	}
}
