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

	// Does not fire unless the filter actually altered a valid JSON document.
	// Truncation is only one way to alter it; match_output replacement is another.
	if _, _, ok := jsonGuard(doc, false, false, false); ok {
		t.Error("guard fired when the filter left the document untouched")
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

// configMapJSON is a realistic `kubectl get configmap -o json` response. The
// word "created" appears only inside an ordinary English annotation, which is
// all it takes for kubectl.toml's unanchored match_output alternation to match
// the whole blob.
const configMapJSON = `{
  "apiVersion": "v1",
  "kind": "ConfigMap",
  "metadata": {
    "name": "app-config",
    "namespace": "prod",
    "annotations": {
      "description": "database settings created by the platform team"
    }
  },
  "data": {
    "DB_HOST": "db.prod.internal",
    "DB_PORT": "5432",
    "CACHE_TTL": "300"
  }
}`

// TestMatchOutputCannotDestroyCompleteJSON is the 3a regression. A SUCCESSFUL
// kubectl command returning a complete JSON document had its entire payload
// replaced by the synthetic message "kubectl: ok". match_output returns with
// Truncated=false, so jsonGuard never ran, and because nothing looked truncated
// the spool was discarded too: silent, unrecoverable loss of a successful
// command's output. Exit 0 means SuppressSyntheticSuccess does not apply.
func TestMatchOutputCannotDestroyCompleteJSON(t *testing.T) {
	t.Setenv("CTX_WIRE_TEE_DIR", t.TempDir())
	reg := mustRegistry(t)
	cmdline := "kubectl get configmap app-config -o json"

	matched := reg.Find(cmdline)
	if matched == nil {
		t.Fatal("expected the kubectl filter to match; otherwise this test does not exercise the bug")
	}
	// Non-vacuity guard. kubectl.toml's pattern is now anchored, so it alone would
	// stop collapsing this payload and the assertions below would pass even with
	// the runner-side guard reverted. Pin the ORIGINAL unanchored shape here so
	// this test keeps exercising the general fix rather than the filter tweak.
	compiled, err := filter.CompileTOMLForTest(`
schema_version = 1

[filters.kubectl-unanchored]
description = "the pre-fix unanchored shape, pinned so this test cannot go vacuous"
match_command = "^kubectl\\b"
match_output = [
  { pattern = "(?m)(configured|created|unchanged|deleted|rolled out)", message = "kubectl: ok" },
]
`)
	if err != nil || len(compiled) != 1 {
		t.Fatalf("CompileTOMLForTest: %v (%d filters)", err, len(compiled))
	}
	unanchored := compiled[0]
	if applied := applySafe(unanchored, configMapJSON, filter.ApplyOptions{}); !applied.Synthetic {
		t.Fatal("the reference filter no longer collapses this payload; the test would be vacuous")
	}

	out, _, _, code, err := runBuffered(context.Background(), reg, unanchored, "/bin/sh",
		[]string{"-c", "cat <<'JSON'\n" + configMapJSON + "\nJSON"},
		cmdline, cmdline, tee.NewSpool("kubectl"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if strings.Contains(out, "kubectl: ok") {
		t.Fatalf("a successful command's complete JSON was replaced by a synthetic message: %q", out)
	}
	if !json.Valid([]byte(strings.TrimSpace(out))) {
		t.Fatalf("output is not valid JSON: %q", out)
	}
	// The payload the agent actually asked for must survive.
	for _, want := range []string{"DB_HOST", "db.prod.internal", "CACHE_TTL"} {
		if !strings.Contains(out, want) {
			t.Errorf("payload %q was lost from the response: %q", want, out)
		}
	}
}

// TestSyntheticCollapseStaysRecoverable covers the general layer. The JSON guard
// and the anchored kubectl pattern both fix the case we can prove, but roughly
// 39 filters using match_output still carry at least one unanchored pattern, and
// their output is not always JSON. Whenever a synthetic message replaces real
// output the spool must survive, even though Truncated is false and the command
// succeeded. Note the distinction: retention alone is forensic (no hash is
// surfaced, so `ctx-wire fetch` cannot reach it); only a collapse above
// syntheticRecoveryHintBytes is agent-recoverable, which is what this test
// asserts via the fetch pointer.
func TestSyntheticCollapseStaysRecoverable(t *testing.T) {
	teeDir := t.TempDir()
	t.Setenv("CTX_WIRE_TEE_DIR", teeDir)
	reg := mustRegistry(t)

	// brew-install.toml carries a fully unanchored `already installed` pattern with
	// no `unless`, so any output mentioning that phrase collapses wholesale. This
	// is plain text, so the JSON guard cannot help, and it is the shape the other
	// unaudited filters share.
	payload := "IMPORTANT DATA LINE\n" + strings.Repeat("filler line of brew output\n", 300) +
		"Warning: openssl 3.2.1 is already installed\n"
	cmdline := "brew install openssl"
	matched := reg.Find(cmdline)
	if matched == nil {
		t.Fatal("expected the brew-install filter to match")
	}

	out, _, hint, code, err := runBuffered(context.Background(), reg, matched, "/bin/sh",
		[]string{"-c", "cat <<'TXT'\n" + payload + "TXT"},
		cmdline, cmdline, tee.NewSpool("brew-install"))
	if err != nil {
		t.Fatalf("runBuffered: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	// Guard against a vacuous test: this input MUST actually collapse, or the
	// assertions below prove nothing.
	if !strings.Contains(out, "ok (already installed)") {
		t.Fatalf("input did not collapse, so this test would not exercise the recovery path: %q", out)
	}
	if strings.Contains(out, "IMPORTANT DATA LINE") {
		t.Fatalf("expected the payload to be replaced by the synthetic message, got %q", out)
	}

	var spooled bool
	if walkErr := filepath.WalkDir(teeDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, readErr := os.ReadFile(p); readErr == nil && strings.Contains(string(b), "IMPORTANT DATA LINE") {
			spooled = true
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("walk tee dir: %v", walkErr)
	}
	if !spooled {
		t.Error("output was replaced by a synthetic message AND the spool was discarded: the data is unrecoverable")
	}
	if !strings.Contains(hint, "ctx-wire fetch") {
		t.Errorf("a large synthetic collapse must point at the spool, got hint %q", hint)
	}
}
