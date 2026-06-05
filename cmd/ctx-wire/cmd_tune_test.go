package main

import (
	"testing"
	"time"
)

func TestParseTuneOptionsTop(t *testing.T) {
	for _, args := range [][]string{{"--top", "5"}, {"--top=5"}} {
		_, topts, err := parseTuneOptions(args)
		if err != nil {
			t.Fatalf("parseTuneOptions(%v): %v", args, err)
		}
		if topts.TopN != 5 {
			t.Fatalf("parseTuneOptions(%v) TopN = %d, want 5", args, topts.TopN)
		}
	}
}

func TestParseTuneOptionsSince(t *testing.T) {
	for _, args := range [][]string{{"--since", "24h"}, {"--since=24h"}} {
		gopts, _, err := parseTuneOptions(args)
		if err != nil {
			t.Fatalf("parseTuneOptions(%v): %v", args, err)
		}
		if gopts.Since.IsZero() {
			t.Fatalf("parseTuneOptions(%v) should set Since", args)
		}
		// 24h ago must be in the past and roughly a day back.
		ago := time.Since(gopts.Since)
		if ago < 23*time.Hour || ago > 25*time.Hour {
			t.Fatalf("parseTuneOptions(%v) Since is %v ago, want ~24h", args, ago)
		}
	}
}

func TestParseTuneOptionsCombined(t *testing.T) {
	gopts, topts, err := parseTuneOptions([]string{"--since", "1h", "--top", "3"})
	if err != nil {
		t.Fatalf("parseTuneOptions combined: %v", err)
	}
	if gopts.Since.IsZero() || topts.TopN != 3 {
		t.Fatalf("combined parse wrong: since=%v top=%d", gopts.Since, topts.TopN)
	}
}

func TestParseBundleArgsOut(t *testing.T) {
	for _, args := range [][]string{{"--out", "x.tar.gz"}, {"--out=x.tar.gz"}} {
		_, _, out, err := parseBundleArgs(args)
		if err != nil {
			t.Fatalf("parseBundleArgs(%v): %v", args, err)
		}
		if out != "x.tar.gz" {
			t.Fatalf("parseBundleArgs(%v) out = %q, want x.tar.gz", args, out)
		}
	}
}

func TestParseBundleArgsCombined(t *testing.T) {
	gopts, topts, out, err := parseBundleArgs([]string{"--out", "b.tgz", "--since", "1h", "--top", "3"})
	if err != nil {
		t.Fatalf("parseBundleArgs combined: %v", err)
	}
	if out != "b.tgz" || gopts.Since.IsZero() || topts.TopN != 3 {
		t.Fatalf("combined bundle parse wrong: out=%q since=%v top=%d", out, gopts.Since, topts.TopN)
	}
}

func TestParseBundleArgsErrors(t *testing.T) {
	cases := [][]string{
		{"--out"},        // missing value
		{"--out="},       // empty value
		{"--top", "abc"}, // delegated flag error still surfaces
		{"--bogus"},      // unknown flag
	}
	for _, args := range cases {
		if _, _, _, err := parseBundleArgs(args); err == nil {
			t.Fatalf("parseBundleArgs(%v) expected an error", args)
		}
	}
}

func TestParseIssueArgs(t *testing.T) {
	gopts, topts, iopts, err := parseIssueArgs([]string{"--open", "--repo", "owner/repo", "--bundle", "b.tgz", "--since", "1h", "--top", "2"})
	if err != nil {
		t.Fatalf("parseIssueArgs: %v", err)
	}
	if !iopts.open || iopts.repo != "owner/repo" || iopts.bundle != "b.tgz" {
		t.Fatalf("issue opts wrong: %+v", iopts)
	}
	if gopts.Since.IsZero() || topts.TopN != 2 {
		t.Fatalf("delegated opts wrong: since=%v top=%d", gopts.Since, topts.TopN)
	}
}

func TestParseIssueArgsEqualsForms(t *testing.T) {
	_, _, iopts, err := parseIssueArgs([]string{"--repo=o/r", "--bundle=x.tgz"})
	if err != nil {
		t.Fatalf("parseIssueArgs equals: %v", err)
	}
	if iopts.repo != "o/r" || iopts.bundle != "x.tgz" || iopts.open {
		t.Fatalf("equals-form opts wrong: %+v", iopts)
	}
}

func TestParseIssueArgsErrors(t *testing.T) {
	cases := [][]string{
		{"--repo"},     // missing value
		{"--repo="},    // empty value
		{"--bundle"},   // missing value
		{"--bundle="},  // empty value
		{"--top", "x"}, // delegated error surfaces
		{"--bogus"},    // unknown flag
	}
	for _, args := range cases {
		if _, _, _, err := parseIssueArgs(args); err == nil {
			t.Fatalf("parseIssueArgs(%v) expected an error", args)
		}
	}
}

func TestParseTuneOptionsErrors(t *testing.T) {
	cases := [][]string{
		{"--top", "abc"},   // not an integer
		{"--top", "-3"},    // negative
		{"--top"},          // missing value
		{"--since"},        // missing value
		{"--since", "zzz"}, // invalid duration/timestamp
		{"--bogus"},        // unknown flag
	}
	for _, args := range cases {
		if _, _, err := parseTuneOptions(args); err == nil {
			t.Fatalf("parseTuneOptions(%v) expected an error", args)
		}
	}
}
