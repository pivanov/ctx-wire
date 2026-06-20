package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/tee"
)

// TestExplicitRangeHonored proves the carve-out end to end: a bounded sed slice
// under the ceiling reaches the agent whole, an over-ceiling or unbounded slice
// still caps, and long-line truncation still applies inside an honored range.
func TestExplicitRangeHonored(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)

	// 1300-line input, with one 600-char line inside the 988..1140 range.
	f := filepath.Join(t.TempDir(), "doc.md")
	var sb strings.Builder
	for i := 1; i <= 1300; i++ {
		if i == 1000 {
			sb.WriteString("LONG" + strings.Repeat("x", 600) + "\n")
		} else {
			fmt.Fprintf(&sb, "line %d\n", i)
		}
	}
	if err := os.WriteFile(f, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(script string) string {
		t.Helper()
		args := []string{"-n", script, f}
		cmdline := "sed -n " + script + " " + f
		matched := reg.Find(cmdline)
		if matched == nil {
			t.Fatalf("no filter matched %q", cmdline)
		}
		out, _, _, _, err := runBuffered(context.Background(), reg, matched, "sed", args, cmdline, "sed", tee.NewSpool("explicit-range"))
		if err != nil {
			t.Fatalf("runBuffered(%q): %v", script, err)
		}
		return out
	}

	// 153-line bounded range, under the 300 ceiling: honored, not count-capped.
	honored := run("988,1140p")
	if !strings.Contains(honored, "line 1140") {
		t.Errorf("explicit range must include its last line (1140); was it capped? lines=%d", strings.Count(honored, "\n"))
	}
	if strings.Contains(honored, "lines truncated") {
		t.Error("explicit range under the ceiling must not hit the line-count cap")
	}
	// The count cap is lifted, but per-line truncation (truncate_lines_at) still applies.
	for _, ln := range strings.Split(honored, "\n") {
		if strings.HasPrefix(ln, "LONG") && len(ln) >= 600 {
			t.Errorf("a long line inside an honored range must still be truncated (len=%d)", len(ln))
		}
	}

	// Over the ceiling: still capped.
	if got := run("1,99999p"); !strings.Contains(got, "lines truncated") {
		t.Errorf("sed -n 1,99999p (over the ceiling) must still cap; lines=%d", strings.Count(got, "\n"))
	}
	// Unbounded $ endpoint: still capped.
	if got := run("1,$p"); !strings.Contains(got, "lines truncated") {
		t.Error("sed -n 1,$p ($ endpoint) must still cap")
	}
}
