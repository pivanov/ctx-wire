package doctor

import (
	"strings"
	"testing"
)

// TestShimPathChecks pins the resolution-based advisory: the decision keys off the
// active count (commands that actually resolve to a shim, aggregated across dirs),
// not PATH directory order.
func TestShimPathChecks(t *testing.T) {
	const dir, total = "/home/u/.local/bin", 3

	cases := []struct {
		name        string
		installed   int
		active      int
		hookCovered bool
		wantName    string
		wantStatus  Status
	}{
		{"active + hook-covered -> startup-cost warning (the slow-terminal case)", 3, 2, true, "startup cost", Warn},
		{"active, steering-only -> shims first on PATH is fine", 3, 2, false, "PATH", OK},
		{"installed but shadowed + hook-covered -> optional, removable", 3, 0, true, "shims", Off},
		{"installed but shadowed, steering-only -> promote on PATH", 3, 0, false, "PATH", Warn},
	}
	for _, tc := range cases {
		got := shimPathChecks(dir, tc.installed, tc.active, total, tc.hookCovered)
		if len(got) != 1 {
			t.Fatalf("%s: got %d checks, want 1", tc.name, len(got))
		}
		if got[0].Name != tc.wantName || got[0].Status != tc.wantStatus {
			t.Errorf("%s: got {%q, %s}, want {%q, %s}", tc.name, got[0].Name, got[0].Status, tc.wantName, tc.wantStatus)
		}
	}

	// The load-bearing guarantee: the startup-cost warning fires ONLY when shims
	// are actually on the hot path AND a hook/plugin covers them. A hook-covered
	// user whose shims are shadowed must NOT be told they have a startup cost (that
	// false alarm is exactly the methodology error the reviews caught).
	if got := shimPathChecks(dir, 3, 0, total, true); got[0].Name == "startup cost" {
		t.Error("must not warn about startup cost when no managed command resolves to a shim")
	}
	// The warning names the cheap fix.
	if got := shimPathChecks(dir, 3, 2, total, true); !strings.Contains(got[0].Detail, "ctx-wire shims uninstall") {
		t.Errorf("startup-cost warning should point at `ctx-wire shims uninstall`, got %q", got[0].Detail)
	}
	// No shims installed at all -> no advisory line.
	if got := shimPathChecks(dir, 0, 0, total, true); got != nil {
		t.Errorf("no installed shims -> no advisory, got %+v", got)
	}
}
