package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/filter"
	"ctx-wire/internal/tee"
)

func TestIsCompleteJSON(t *testing.T) {
	cases := map[string]bool{
		`{"a":1}`:           true,
		`[1,2,3]`:           true,
		"  {\n\"a\":1\n}\n": true, // surrounding whitespace is fine
		`{}`:                true,
		`[INFO] not json`:   false, // bracketed log line, not JSON
		`{broken`:           false,
		`42`:                false, // a scalar is not a JSON payload
		`"a string"`:        false,
		``:                  false,
		`   `:               false,
		`hello world`:       false,
	}
	for in, want := range cases {
		if got := filter.IsCompleteJSON(in); got != want {
			t.Errorf("IsCompleteJSON(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestJSONGuard(t *testing.T) {
	const doc = `{"workspace":{"dir":"/tmp/demo"},"pad":"xxxxxxxxxx"}`

	// Does not fire unless the filter actually truncated a valid JSON document.
	if _, _, ok := jsonGuard(doc, false, false, false); ok {
		t.Error("guard fired without truncation")
	}
	if _, _, ok := jsonGuard(doc, true, true, false); ok {
		t.Error("guard fired with filter_stderr (merged stream, not a pure JSON payload)")
	}
	if _, _, ok := jsonGuard(doc, true, false, true); ok {
		t.Error("guard fired for a reduce_json filter (jq)")
	}
	if _, _, ok := jsonGuard("[INFO] noisy log line", true, false, false); ok {
		t.Error("guard fired on non-JSON output")
	}

	// Fires for truncated valid JSON under the ceiling: emits the whole document.
	text, mode, ok := jsonGuard(doc, true, false, false)
	if !ok || mode != jsonModeWhole {
		t.Fatalf("guard whole: ok=%v mode=%q", ok, mode)
	}
	if !json.Valid([]byte(text)) {
		t.Errorf("emitted text is not valid JSON: %q", text)
	}

	// Over the ceiling: replaced with a notice, never a mid-structure cut.
	defer func(old int) { filter.MaxJSONPassthrough = old }(filter.MaxJSONPassthrough)
	filter.MaxJSONPassthrough = 16
	text, mode, ok = jsonGuard(doc, true, false, false)
	if !ok || mode != jsonModeCapped {
		t.Fatalf("guard capped: ok=%v mode=%q", ok, mode)
	}
	if strings.Contains(text, `"workspace"`) {
		t.Errorf("oversize JSON should be replaced, not emitted: %q", text)
	}
	if !strings.Contains(text, "JSON document omitted") {
		t.Errorf("expected an omission notice, got %q", text)
	}
}

func TestJSONGuardScrubsOnPassthrough(t *testing.T) {
	// A secret inside passed-through JSON must still be redacted.
	const secret = "AKIAIOSFODNN7EXAMPLE"
	doc := `{"aws_key":"` + secret + `","note":"keep"}`
	text, _, ok := jsonGuard(doc, true, false, false)
	if !ok {
		t.Fatal("guard did not fire")
	}
	if strings.Contains(text, secret) {
		t.Errorf("secret leaked through JSON passthrough: %q", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Errorf("expected redaction marker, got %q", text)
	}
}

// TestRunBufferedJSONPassthrough is the end-to-end reporter repro: a long
// single-line JSON document through a truncating filter (cat) must come out
// whole and parseable, not cut mid-string.
func TestRunBufferedJSONPassthrough(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)

	payload := `{"workspace":{"current_dir":"/tmp/demo"},"padding":"` +
		strings.Repeat("x", 2400) + `"}`
	file := filepath.Join(t.TempDir(), "statusline.json")
	if err := os.WriteFile(file, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, _, code, err := runBuffered(context.Background(), reg, reg.Find("cat "+file), "cat",
		[]string{file}, "cat "+file, "cat ...", tee.NewSpool("cat"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !json.Valid([]byte(strings.TrimSpace(out))) {
		t.Fatalf("JSON was corrupted by filtering: %q", out)
	}
}

// TestRunBufferedDisablesNestedShims proves a wrapped command's children see
// CTX_WIRE_DISABLE_SHIMS=1 so internal pipelines stay byte-exact.
func TestRunBufferedDisablesNestedShims(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	out, _, _, code, err := runBuffered(context.Background(), reg, reg.Find("sh -c ..."), "sh",
		[]string{"-c", `printf 'shims=%s' "$CTX_WIRE_DISABLE_SHIMS"`},
		"sh -c ...", "sh -c ...", tee.NewSpool("sh"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out, "shims=1") {
		t.Errorf("child did not see CTX_WIRE_DISABLE_SHIMS=1, got %q", out)
	}
}
