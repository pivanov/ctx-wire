package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallOpenCode(t *testing.T) {
	// Nested plugin dir does not exist yet.
	path := filepath.Join(t.TempDir(), "opencode", "plugins", "ctx-wire.ts")

	changed, err := InstallOpenCode(path)
	if err != nil {
		t.Fatalf("InstallOpenCode: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first install")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plugin not written: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "tool.execute.before") || !strings.Contains(got, "ctx-wire rewrite") {
		t.Errorf("plugin content unexpected:\n%s", got)
	}

	// Idempotent: an up-to-date plugin is left alone.
	if changed, err := InstallOpenCode(path); err != nil || changed {
		t.Errorf("second install: changed=%v err=%v, want (false, nil)", changed, err)
	}

	// A drifted plugin is rewritten.
	if err := os.WriteFile(path, []byte("// stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed, err := InstallOpenCode(path); err != nil || !changed {
		t.Errorf("drifted install: changed=%v err=%v, want (true, nil)", changed, err)
	}
}
