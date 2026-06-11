package main

import (
	"os"
	"testing"
)

func TestCanonicalUninstallAgentMatchesInitAliases(t *testing.T) {
	cases := map[string]string{
		"vs":             "visualstudio",
		"visualstudio":   "visualstudio",
		"github-copilot": "copilot",
		"copilot":        "copilot",
		"Claude":         "claude",
		"claud":          "claud",
	}
	for in, want := range cases {
		if got := canonicalUninstallAgent(in); got != want {
			t.Errorf("canonicalUninstallAgent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCmdUninstallRejectsBadArityAndUnknownAgent(t *testing.T) {
	if code := cmdUninstall([]string{"claude", "extra"}); code != 2 {
		t.Fatalf("cmdUninstall extra args exit = %d, want 2", code)
	}
	if code := cmdUninstall([]string{"claud"}); code != 2 {
		t.Fatalf("cmdUninstall unknown agent exit = %d, want 2", code)
	}
}

func TestCmdUninstallAgentAcceptsInitAliases(t *testing.T) {
	home := t.TempDir()
	workdir := t.TempDir()
	t.Setenv("HOME", home)
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
	})

	if code := cmdUninstallAgent("vs"); code != 0 {
		t.Fatalf("cmdUninstallAgent(vs) exit = %d, want 0", code)
	}
	if code := cmdUninstallAgent("github-copilot"); code != 0 {
		t.Fatalf("cmdUninstallAgent(github-copilot) exit = %d, want 0", code)
	}
}
