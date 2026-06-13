package filter

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkApplyTruncate measures per-line []rune allocations in the
// truncate_lines_at path. Run with -benchmem to see allocs/op.
func BenchmarkApplyTruncate(b *testing.B) {
	at := 120
	def := tomlFilter{
		TruncateLinesAt: &at,
	}
	f, err := compile("bench-truncate", def)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}

	// Build ~5 000 lines of mixed length: some short, some over the limit.
	var sb strings.Builder
	for i := range 5000 {
		if i%3 == 0 {
			// Long line (>120 runes) with some multibyte runes to stress rune counting.
			fmt.Fprintf(&sb, "LONG line %04d: %s\n", i, strings.Repeat("x", 150))
		} else {
			fmt.Fprintf(&sb, "short line %04d\n", i)
		}
	}
	input := sb.String()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		Apply(f, input)
	}
}
