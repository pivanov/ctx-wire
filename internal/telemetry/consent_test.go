package telemetry

import (
	"path/filepath"
	"strings"
	"testing"

	"ctx-wire/internal/gain"
)

func TestShouldPreviewConsentLatch(t *testing.T) {
	dir := t.TempDir()
	// An undecided user sees the invite once.
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	if !ShouldPreviewConsent() {
		t.Fatal("an undecided user should get the consent invite once")
	}
	MarkPreviewShown()
	if ShouldPreviewConsent() {
		t.Fatal("the invite must not show again after MarkPreviewShown")
	}
}

func TestShouldPreviewConsentNotAfterChoice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envConfig, filepath.Join(dir, "telemetry.json"))
	t.Setenv(envState, filepath.Join(dir, "state.json"))
	// A user who already saw the notice is never re-invited. Command-breakdown
	// choices do not affect aggregate telemetry.
	if err := SetShareImprovements(true); err != nil {
		t.Fatal(err)
	}
	MarkPreviewShown()
	if ShouldPreviewConsent() {
		t.Fatal("a user who saw the notice must not be re-invited")
	}
}

func TestPreviewPayloadShapeAndPrivacy(t *testing.T) {
	telemetryTempEnv(t)
	SetBuildInfo("0.1.17-test")
	t.Cleanup(func() { SetBuildInfo("") })
	s := summary(5, 1000, 200, 800, []gain.CommandStat{
		{Program: "cargo", Count: 3, RawBytes: 600, EmittedBytes: 100, SavedBytes: 500},
		{Program: "project-zeus", Count: 2, RawBytes: 400, EmittedBytes: 100, SavedBytes: 300},
	})
	out := PreviewPayload(s)
	if !strings.Contains(out, `"version": "0.1.17-test"`) {
		t.Errorf("preview missing version:\n%s", out)
	}
	if strings.Contains(out, "project-zeus") {
		t.Errorf("preview leaked a private program name:\n%s", out)
	}
	if !strings.Contains(out, otherBucket) {
		t.Errorf("preview should bucket the private program to %q:\n%s", otherBucket, out)
	}
	if !strings.Contains(out, "cargo") {
		t.Errorf("preview should keep the public program cargo:\n%s", out)
	}
}

func TestMockPayloadShape(t *testing.T) {
	SetBuildInfo("0.1.17-test")
	t.Cleanup(func() { SetBuildInfo("") })

	out := MockPayload()
	// A consent prompt shows ONE example program (git) plus one agent; the full
	// breakdown is on demand via `telemetry preview`. Keep this minimal.
	for _, want := range []string{
		`"version": "0.1.17-test"`,
		`"commands": 128`,
		`"git":`,
		`"claude":`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("mock payload missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"go":`) || strings.Contains(out, `"`+otherBucket+`":`) {
		t.Fatalf("mock payload should be a SINGLE example program (git), got:\n%s", out)
	}
	if strings.Contains(out, "project-") || strings.Contains(out, "/") {
		t.Fatalf("mock payload should be fixed and generic, got:\n%s", out)
	}
}
