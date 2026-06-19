package telemetry

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ctx-wire/internal/gain"
)

func TestSafeProgramName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cargo", "cargo"},                   // public, ships a filter
		{"git", "git"},                       // public
		{"ls", "ls"},                         // public passthrough tool
		{"nl", "nl"},                         // file-read steer in AGENTS.md must stay visible
		{"gh", "gh"},                         // GitHub CLI, ubiquitous in agent workflows
		{"just", "just"},                     // task runner (this repo uses it)
		{"powershell", "powershell"},         // Windows shell, public by definition
		{"agent-browser", otherBucket},       // user script -> (other): the privacy boundary
		{"project-zeus-deploy", otherBucket}, // private codename -> (other)
		{"my_secret_tool", otherBucket},
		{"", ""}, // not a token: stays empty (callers skip it)
	}
	for _, c := range cases {
		if got := safeProgramName(c.in); got != c.want {
			t.Errorf("safeProgramName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSafeAgentName(t *testing.T) {
	if got := safeAgentName("claude"); got != "claude" {
		t.Errorf("known agent: got %q, want claude", got)
	}
	if got := safeAgentName("project-zeus"); got != otherBucket {
		t.Errorf("unknown well-formed agent: got %q, want %s", got, otherBucket)
	}
	if got := safeAgentName(""); got != "" {
		t.Errorf("empty agent: got %q, want empty", got)
	}
}

// A path-invoked private binary: gain.ProgramName strips the path to the base
// name, then the allowlist buckets it. Proves /opt/secret/zeus-deploy never
// leaves the machine, not even as a basename.
func TestPathInvokedPrivateProgramBucketed(t *testing.T) {
	tot := totalsFromCommand("/opt/secret/zeus-deploy build --prod", "claude", 1000, 100)
	if _, leaked := tot.Programs["zeus-deploy"]; leaked {
		t.Fatal("private binary basename leaked into telemetry programs")
	}
	if _, ok := tot.Programs[otherBucket]; !ok {
		t.Fatalf("expected (other) bucket, got %v", tot.Programs)
	}
}

func captureSender(t *testing.T) *[]map[string]any {
	t.Helper()
	var payloads []map[string]any
	restoreSender(t, func(v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var p map[string]any
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		payloads = append(payloads, p)
		return nil
	})
	return &payloads
}

func telemetryTempEnv(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(envEnabled, "1")
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
}

// The impact payload must carry the build version (for per-version graphs) and
// must never carry a private program name.
func TestImpactPayloadCarriesVersionAndBucketsPrivatePrograms(t *testing.T) {
	telemetryTempEnv(t)
	// Steady state: install already reported, so the impact flush doesn't backfill.
	if err := writeConfig(Config{InstallReported: true}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	SetBuildInfo("0.1.17-test")
	t.Cleanup(func() { SetBuildInfo("") })
	payloads := captureSender(t)

	s := summary(5, 1000, 200, 800, []gain.CommandStat{
		{Program: "cargo", Count: 3, RawBytes: 600, EmittedBytes: 100, SavedBytes: 500},
		{Program: "project-zeus-deploy", Count: 2, RawBytes: 400, EmittedBytes: 100, SavedBytes: 300},
	})
	if _, err := ReportImpact(s); err != nil {
		t.Fatalf("ReportImpact: %v", err)
	}
	if len(*payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(*payloads))
	}
	if got := (*payloads)[0]["version"]; got != "0.1.17-test" {
		t.Errorf("version = %v, want 0.1.17-test", got)
	}
	programs := (*payloads)[0]["programs"].(map[string]any)
	if _, leaked := programs["project-zeus-deploy"]; leaked {
		t.Error("private program leaked into the impact payload")
	}
	if _, ok := programs[otherBucket]; !ok {
		t.Errorf("expected (other) bucket, got %v", programs)
	}
	if _, ok := programs["cargo"]; !ok {
		t.Error("public program cargo should be preserved")
	}
}

// A private agent label must bucket to (other), not leave the machine.
func TestPrivateAgentBucketed(t *testing.T) {
	telemetryTempEnv(t)
	payloads := captureSender(t)
	// rawBytes over the 10 MB flush threshold forces an immediate send.
	if _, err := RecordCommand("cargo build", "project-zeus", 11<<20, 100); err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	if len(*payloads) != 1 {
		t.Fatalf("expected one flush, got %d", len(*payloads))
	}
	agents, ok := (*payloads)[0]["agents"].(map[string]any)
	if !ok {
		t.Fatalf("no agents in payload: %v", (*payloads)[0])
	}
	if _, leaked := agents["project-zeus"]; leaked {
		t.Error("private agent label leaked")
	}
	if _, ok := agents[otherBucket]; !ok {
		t.Errorf("expected (other) agent bucket, got %v", agents)
	}
}

// A pre-upgrade telemetry-state.json may already hold a private program key in
// Pending. It must be folded to (other) on read, so it cannot leak on the next
// flush (reviewer 1's at-rest case).
func TestOldStatePrivateKeysSanitizedOnFlush(t *testing.T) {
	telemetryTempEnv(t)
	big := int64(11 << 20)
	if err := writeState(stateFile{Pending: Totals{
		Commands:   1,
		RawBytes:   big,
		BytesSaved: big,
		Programs:   map[string]ProgramTotals{"project-zeus": {Count: 1, RawBytes: big, BytesSaved: big}},
	}}); err != nil {
		t.Fatalf("seed state: %v", err)
	}
	payloads := captureSender(t)
	// Any small public command tips the already-large pending over the threshold.
	if _, err := RecordCommand("ls -la", "", 100, 10); err != nil {
		t.Fatalf("RecordCommand: %v", err)
	}
	if len(*payloads) != 1 {
		t.Fatalf("expected a flush, got %d payloads", len(*payloads))
	}
	programs := (*payloads)[0]["programs"].(map[string]any)
	if _, leaked := programs["project-zeus"]; leaked {
		t.Fatal("pre-upgrade private key in state leaked on flush")
	}
	if _, ok := programs[otherBucket]; !ok {
		t.Errorf("expected the old private key folded to (other), got %v", programs)
	}
}
