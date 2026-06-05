package main

import "testing"

func TestParseTokenCount(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"200000", 200000, false},
		{"200k", 200000, false},
		{"200K", 200000, false},
		{"1.5m", 1500000, false},
		{"2M", 2000000, false},
		{"2_000_000", 2000000, false},
		{"1,000,000", 1000000, false},
		{"0", 0, false},
		{"", 0, true},
		{"abc", 0, true},
		{"-5", 0, true},
		{"10x", 0, true},
	}
	for _, c := range cases {
		got, err := parseTokenCount(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseTokenCount(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("parseTokenCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseGainOptionsQuota(t *testing.T) {
	// --budget implies the quota view and parses the token count.
	view, _, _, q, err := parseGainOptions([]string{"--budget", "500k"})
	if err != nil {
		t.Fatalf("parseGainOptions: %v", err)
	}
	if view != "quota" {
		t.Errorf("view = %q, want quota", view)
	}
	if q.budget != 500000 {
		t.Errorf("budget = %d, want 500000", q.budget)
	}

	// Bare --quota leaves budget/window unset (negative sentinels) for config.
	_, _, _, q2, err := parseGainOptions([]string{"--quota"})
	if err != nil {
		t.Fatalf("parseGainOptions --quota: %v", err)
	}
	if q2.budget != -1 || q2.window != -1 {
		t.Errorf("unset overrides = %+v, want both -1", q2)
	}

	// --quota conflicts with another view.
	if _, _, _, _, err := parseGainOptions([]string{"--quota", "--graph"}); err == nil {
		t.Error("expected conflict error for --quota --graph")
	}

	// --window=0 is rejected (must be positive).
	if _, _, _, _, err := parseGainOptions([]string{"--window", "0"}); err == nil {
		t.Error("expected error for --window 0")
	}
}

func TestParseGainOptionsTopRequiresHistory(t *testing.T) {
	// --top with --history is accepted and sets the limit.
	view, _, limit, _, err := parseGainOptions([]string{"--history", "--top", "5"})
	if err != nil || view != "history" || limit != 5 {
		t.Fatalf("--history --top 5 => view=%q limit=%d err=%v", view, limit, err)
	}
	// --top alone (default summary view) is rejected rather than silently ignored.
	if _, _, _, _, err := parseGainOptions([]string{"--top", "1"}); err == nil {
		t.Error("expected error for --top without --history")
	}
	// --top with a non-history view is rejected too.
	if _, _, _, _, err := parseGainOptions([]string{"--daily", "--top", "1"}); err == nil {
		t.Error("expected error for --daily --top 1")
	}
}
