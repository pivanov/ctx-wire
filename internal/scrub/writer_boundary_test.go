package scrub

import (
	"bytes"
	"strings"
	"testing"
)

// TestStreamingScrubTokenAtFlushBoundary pins that a secret straddling the
// no-newline forced-flush boundary (streamHoldMax) is redacted rather than
// emitted as two unmatched halves. Regression for the singleLineHold suffix:
// without it, the first flush cuts mid-token and neither half matches.
func TestStreamingScrubTokenAtFlushBoundary(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out)

	key := "AKIA" + strings.Repeat("Z", 16) // valid AWS access-key shape
	// First write ends exactly at the flush boundary, mid-key (right after AKIA);
	// the space before AKIA keeps the \bAKIA word boundary.
	prefix := strings.Repeat("x", streamHoldMax-len(" AKIA"))
	if _, err := w.Write([]byte(prefix + " AKIA")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("Z", 16) + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if strings.Contains(got, key) {
		t.Fatalf("secret leaked across the flush boundary: %q found unredacted", key)
	}
	if !strings.Contains(got, redacted) {
		t.Fatal("expected the boundary-straddling token to be redacted")
	}
}
