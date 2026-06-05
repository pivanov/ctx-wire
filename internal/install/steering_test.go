package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSteeringPresentInBlocks(t *testing.T) {
	for name, block := range map[string]string{
		"rules":   ctxWireRulesBlock,
		"copilot": copilotInstructionsBlock,
	} {
		if !strings.Contains(block, "Read tool") || !strings.Contains(block, "bypass it") {
			t.Errorf("%s instruction block is missing the Read/Grep/Glob steering", name)
		}
	}
}

func TestInstallClaudeMemoryAndCodexAgents(t *testing.T) {
	cases := []struct {
		name string
		do   func(string) (bool, error)
		file string
	}{
		{"claude", InstallClaudeMemory, "CLAUDE.md"},
		{"codex", InstallCodexAgents, "AGENTS.md"},
		{"gemini", InstallGeminiMemory, "GEMINI.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), c.file)
			if err := os.WriteFile(path, []byte("# My memory\n\nkeep me.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			changed, err := c.do(path)
			if err != nil || !changed {
				t.Fatalf("install = (%v, %v), want (true, nil)", changed, err)
			}
			s, _ := os.ReadFile(path)
			if !strings.Contains(string(s), "keep me.") {
				t.Error("existing memory content not preserved")
			}
			if !strings.Contains(string(s), "Read tool") || !strings.Contains(string(s), ctxWireBlockStart) {
				t.Errorf("steering block not inserted:\n%s", s)
			}
			if again, _ := c.do(path); again {
				t.Error("second install should be a no-op")
			}
		})
	}
}
