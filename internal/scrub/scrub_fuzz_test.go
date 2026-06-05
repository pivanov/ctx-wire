package scrub

import "testing"

func FuzzScrub(f *testing.F) {
	for _, seed := range []string{
		"PASSWORD=hunter2",
		"TOKEN=\"a b c\"",
		"Authorization: Bearer sk-test-token",
		"-----BEGIN RSA PRIVATE KEY-----\nSECRET\n-----END RSA PRIVATE KEY-----",
		"https://user:pass@example.com/path",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Scrub(s)
		if _, ok := ScrubFailClosed(out); !ok {
			t.Fatalf("ScrubFailClosed failed on scrubbed output")
		}
	})
}
