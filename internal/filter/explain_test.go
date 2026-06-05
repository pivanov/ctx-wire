package filter

import "testing"

func TestRegistryExplain(t *testing.T) {
	reg, err := LoadBuiltin()
	if err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	tests := []struct {
		name           string
		command        string
		wantMatched    bool
		wantName       string
		wantNormalized bool
	}{
		{"matched filter", "git status", true, "git-status", false},
		{"no matched filter", "frobnicate --all", false, "", false},
		{"full-path normalization", "/usr/local/bin/git status", true, "git-status", true},
		{"full-path no match still none", "/opt/bin/frobnicate", false, "", false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			d := reg.Explain(tt.command)
			if d.Matched != tt.wantMatched {
				t.Fatalf("Matched = %v, want %v", d.Matched, tt.wantMatched)
			}
			if d.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", d.Name, tt.wantName)
			}
			if d.Normalized != tt.wantNormalized {
				t.Errorf("Normalized = %v, want %v", d.Normalized, tt.wantNormalized)
			}
			if tt.wantNormalized && d.NormalizedForm == "" {
				t.Error("expected NormalizedForm to be set")
			}
		})
	}
}
