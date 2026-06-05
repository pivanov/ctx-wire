package runner

import "testing"

func TestClassifyBypass(t *testing.T) {
	tests := []struct {
		name       string
		cmd        string
		args       []string
		wantBypass bool
		wantReason string // substring
	}{
		{"interactive editor", "vim", []string{"f"}, true, "interactive"},
		{"interactive full path", "/usr/bin/less", []string{"log"}, true, "interactive"},
		{"follow flag", "tail", []string{"-f", "app.log"}, true, "streaming"},
		{"watch flag", "kubectl", []string{"get", "pods", "-w"}, true, "streaming"},
		{"long follow", "docker", []string{"logs", "--follow", "c1"}, true, "streaming"},
		{"normal build", "go", []string{"build", "./..."}, false, ""},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			bypass, reason := ClassifyBypass(tt.cmd, tt.args)
			if bypass != tt.wantBypass {
				t.Fatalf("bypass = %v, want %v", bypass, tt.wantBypass)
			}
			if tt.wantBypass && !containsSub(reason, tt.wantReason) {
				t.Errorf("reason = %q, want substring %q", reason, tt.wantReason)
			}
			if !tt.wantBypass && reason != "" {
				t.Errorf("reason = %q, want empty", reason)
			}
		})
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return sub == ""
}
