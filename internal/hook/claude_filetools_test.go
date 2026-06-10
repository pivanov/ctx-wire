package hook

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeInfo implements just enough of os.FileInfo for the Read mapper.
type fakeInfo struct {
	size int64
	mode fs.FileMode
}

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

func regularFile(size int64) func(string) (os.FileInfo, error) {
	return func(string) (os.FileInfo, error) { return fakeInfo{size: size}, nil }
}

func TestMapReadSuggestion(t *testing.T) {
	big := regularFile(64 * 1024)
	cases := []struct {
		name  string
		input string
		lstat func(string) (os.FileInfo, error)
		want  string
		ok    bool
	}{
		{"big unranged text file", `{"file_path":"/work/big.go"}`, big, "nl -ba '/work/big.go'", true},
		{"path with spaces and quotes", `{"file_path":"/work/it's a dir/big file.md"}`, big, `nl -ba '/work/it'\''s a dir/big file.md'`, true},
		{"ranged read allows", `{"file_path":"/work/big.go","offset":10,"limit":50}`, big, "", false},
		{"limit-only allows", `{"file_path":"/work/big.go","limit":100}`, big, "", false},
		{"small file allows", `{"file_path":"/work/small.go"}`, regularFile(512), "", false},
		{"image suffix allows", `{"file_path":"/work/big.png"}`, big, "", false},
		{"notebook allows", `{"file_path":"/work/nb.ipynb"}`, big, "", false},
		{"extensionless allows", `{"file_path":"/work/Makefile"}`, big, "", false},
		{"relative path allows", `{"file_path":"work/big.go"}`, big, "", false},
		{"dash-leading relative allows", `{"file_path":"-rf.go"}`, big, "", false},
		{"stat error allows", `{"file_path":"/gone/big.go"}`, func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }, "", false},
		{"directory allows", `{"file_path":"/work/dir.go"}`, func(string) (os.FileInfo, error) { return fakeInfo{size: 1 << 20, mode: fs.ModeDir}, nil }, "", false},
		{"symlink allows", `{"file_path":"/work/link.go"}`, func(string) (os.FileInfo, error) { return fakeInfo{size: 1 << 20, mode: fs.ModeSymlink}, nil }, "", false},
		{"device allows", `{"file_path":"/dev/disk0.log"}`, func(string) (os.FileInfo, error) { return fakeInfo{size: 1 << 20, mode: fs.ModeDevice}, nil }, "", false},
		{"malformed shape allows", `{"file_path":42}`, big, "", false},
		{"empty path allows", `{}`, big, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := mapReadSuggestion(json.RawMessage(c.input), c.lstat)
			if got != c.want || ok != c.ok {
				t.Errorf("mapReadSuggestion(%s) = %q, %v; want %q, %v", c.input, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestMapGrepSuggestion(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"default mode is files_with_matches", `{"pattern":"TODO"}`, "rg -l -- 'TODO'", true},
		{"content mode", `{"pattern":"TODO","path":"/src","output_mode":"content"}`, "rg -n -- 'TODO' '/src'", true},
		{"count mode", `{"pattern":"TODO","output_mode":"count"}`, "rg -c -- 'TODO'", true},
		{"case insensitive", `{"pattern":"todo","output_mode":"content","-i":true}`, "rg -n -i -- 'todo'", true},
		{"context flags in content mode", `{"pattern":"x","output_mode":"content","-C":2}`, "rg -n -C 2 -- 'x'", true},
		{"before and after", `{"pattern":"x","output_mode":"content","-B":1,"-A":3}`, "rg -n -B 1 -A 3 -- 'x'", true},
		{"type filter", `{"pattern":"x","output_mode":"content","type":"go"}`, "rg -n -t go -- 'x'", true},
		{"glob filter", `{"pattern":"x","glob":"*.tsx"}`, "rg -l -g '*.tsx' -- 'x'", true},
		{"dash-leading pattern is safe after --", `{"pattern":"-rf","output_mode":"content"}`, "rg -n -- '-rf'", true},
		{"single quotes in pattern", `{"pattern":"it's"}`, `rg -l -- 'it'\''s'`, true},
		{"command substitution stays inert", `{"pattern":"$(rm -rf /)"}`, `rg -l -- '$(rm -rf /)'`, true},
		{"backticks stay inert", "{\"pattern\":\"`id`\"}", "rg -l -- '`id`'", true},
		{"newline in pattern stays quoted", `{"pattern":"a\nb"}`, "rg -l -- 'a\nb'", true},
		{"head_limit allows", `{"pattern":"x","head_limit":5}`, "", false},
		{"multiline allows", `{"pattern":"x","multiline":true}`, "", false},
		{"unknown mode allows", `{"pattern":"x","output_mode":"json"}`, "", false},
		{"context outside content allows", `{"pattern":"x","-C":2}`, "", false},
		{"negative context allows", `{"pattern":"x","output_mode":"content","-A":-1}`, "", false},
		{"weird type allows", `{"pattern":"x","output_mode":"content","type":"go; rm -rf /"}`, "", false},
		{"empty pattern allows", `{"path":"/src"}`, "", false},
		{"malformed shape allows", `{"pattern":["a"]}`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := mapGrepSuggestion(json.RawMessage(c.input))
			if got != c.want || ok != c.ok {
				t.Errorf("mapGrepSuggestion(%s) = %q, %v; want %q, %v", c.input, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestClaudeFileToolEndToEnd drives the full hook with a Grep payload: flag off
// emits nothing; flag on denies with the suggestion in the reason; an
// immediate retry of the same request is allowed through (loop-breaker).
func TestClaudeFileToolEndToEnd(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // isolate deny state
	payload := `{"session_id":"s1","tool_name":"Grep","tool_input":{"pattern":"TODO","output_mode":"content"}}`

	SetCaptureFileTools(false)
	t.Cleanup(func() { SetCaptureFileTools(false) })
	var out bytes.Buffer
	if err := Claude(strings.NewReader(payload), &out); err != nil || out.Len() != 0 {
		t.Fatalf("flag off must emit nothing, got %q (err %v)", out.String(), err)
	}

	SetCaptureFileTools(true)
	out.Reset()
	if err := Claude(strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	var resp claudeOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("deny output is not valid JSON: %v\n%s", err, out.String())
	}
	h := resp.HookSpecificOutput
	if h.PermissionDecision != "deny" || h.UpdatedInput != nil {
		t.Errorf("want pure deny, got %+v", h)
	}
	if !strings.Contains(h.PermissionDecisionReason, "rg -n -- 'TODO'") {
		t.Errorf("reason must carry the exact suggestion, got %q", h.PermissionDecisionReason)
	}

	// Immediate retry: loop-breaker lets it through.
	out.Reset()
	if err := Claude(strings.NewReader(payload), &out); err != nil || out.Len() != 0 {
		t.Fatalf("retry within TTL must be allowed (no output), got %q", out.String())
	}
}

// TestClaudeBashPathUnchanged pins that the Bash rewrite path still works after
// the envelope restructure (allow + updatedInput).
func TestClaudeBashPathUnchanged(t *testing.T) {
	payload := `{"tool_name":"Bash","tool_input":{"command":"git status"}}`
	var out bytes.Buffer
	if err := Claude(strings.NewReader(payload), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Skip("git not rewritable in this environment")
	}
	var resp claudeOutput
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	h := resp.HookSpecificOutput
	if h.PermissionDecision != "allow" || h.UpdatedInput == nil || !strings.Contains(h.UpdatedInput.Command, "ctx-wire run") {
		t.Errorf("Bash path broken: %+v", h)
	}
	if h.PermissionDecisionReason != "" {
		t.Errorf("allow must not carry a deny reason: %q", h.PermissionDecisionReason)
	}
}

func TestRecordDenyOnce(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	in := []byte(`{"pattern":"x"}`)
	if !recordDenyOnce("s", "Grep", in) {
		t.Fatal("first deny must be recordable")
	}
	if recordDenyOnce("s", "Grep", in) {
		t.Fatal("repeat within TTL must NOT deny again")
	}
	if !recordDenyOnce("s", "Grep", []byte(`{"pattern":"y"}`)) {
		t.Fatal("different input is a different request")
	}
	if !recordDenyOnce("s2", "Grep", in) {
		t.Fatal("different session is a different request")
	}
}

func TestRecordDenyOnceUnwritableStateAllows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	// Make the ctx-wire data dir an unwritable file so MkdirAll/WriteFile fail.
	if err := os.WriteFile(dir+"/ctx-wire", []byte("not a dir"), 0o400); err != nil {
		t.Fatal(err)
	}
	if recordDenyOnce("s", "Read", []byte(`{}`)) {
		t.Fatal("unrecordable state must never deny")
	}
}

// TestFileToolUnknownFieldsFailOpen pins the strict-decode rule the review
// demanded: a payload carrying any field these mappers do not model means
// Claude is expressing semantics the translation cannot honor (e.g. a future
// Grep "literal" mode), so the mapping must fail OPEN and let the built-in
// tool run. Plain json.Unmarshal would silently drop the field and deny with
// a non-equivalent suggestion.
func TestFileToolUnknownFieldsFailOpen(t *testing.T) {
	bigText := regularFile(10 << 20)

	cases := []struct {
		name  string
		tool  string
		input string
	}{
		{"grep with future literal flag", "Grep", `{"pattern":"TODO","output_mode":"content","literal":true}`},
		{"grep with unknown option", "Grep", `{"pattern":"TODO","frobnicate":1}`},
		{"read with unknown option", "Read", `{"file_path":"/abs/big.go","follow_symlinks":true}`},
		{"read with trailing garbage", "Read", `{"file_path":"/abs/big.go"} {"x":1}`},
	}
	for _, tc := range cases {
		if got, ok := mapFileToolSuggestion(tc.tool, []byte(tc.input), bigText); ok {
			t.Errorf("%s: must fail open, got suggestion %q", tc.name, got)
		}
	}

	// Sanity: the same payloads WITHOUT the unknown field still map, so the
	// fail-open cases above are rejected for the right reason.
	if _, ok := mapFileToolSuggestion("Grep", []byte(`{"pattern":"TODO","output_mode":"content"}`), bigText); !ok {
		t.Error("known-field grep payload must still map")
	}
	if _, ok := mapFileToolSuggestion("Read", []byte(`{"file_path":"/abs/big.go"}`), bigText); !ok {
		t.Error("known-field read payload must still map")
	}
}
