package scrub

import (
	"strings"
	"testing"
)

func benchInput() string {
	// 50KB of mixed-case prose with no secret marker -> anchors miss, ToLower runs.
	return strings.Repeat("The Quick Brown Fox Jumps Over The Lazy Dog 0123456789 ", 950)
}
func BenchmarkMightContainSecret(b *testing.B) {
	s := benchInput()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mightContainSecret(s)
	}
}
