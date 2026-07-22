package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"ctx-wire/internal/rewrite"
)

func captureRewrite(t *testing.T, args []string) (string, int) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := cmdRewrite(args)
	_ = w.Close()
	os.Stdout = orig
	defer r.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return out.String(), code
}

func TestCmdRewriteJSON(t *testing.T) {
	out, code := captureRewrite(t, []string{"--json", "--agent", "pi", "git status; FOO=1 git log"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got rewrite.LineExplain
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if !got.Changed || got.Result != "ctx-wire run --agent pi git status; FOO=1 ctx-wire run --agent pi git log" {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(got.Segments) != 2 || got.Segments[0].Program != "git" || got.Segments[1].Program != "git" {
		t.Fatalf("unexpected segments: %+v", got.Segments)
	}
}

func TestCmdRewriteJSONRequiresCommand(t *testing.T) {
	if _, code := captureRewrite(t, []string{"--json", "--agent", "pi"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}
