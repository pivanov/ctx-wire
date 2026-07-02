package telemetry

import (
	"encoding/json"

	"ctx-wire/internal/gain"
)

// PreviewPayload renders the sanitized impact summary telemetry would share for
// this gain summary, with the user's REAL numbers, for the explicit
// `ctx-wire telemetry preview` command. It is the exact wire shape: programs and
// agents allowlisted to public names ("other" for the rest), version stamped,
// nothing else. Pretty-printed; returns "" only on an impossible marshal error.
func PreviewPayload(summary *gain.Summary) string {
	t := totalsFromSummary(summary)
	sanitizeTotals(&t)     // defensive; totalsFromSummary already gates at construction
	cfg, _ := readConfig() // preview must match the wire, including the sub-toggle
	p := buildImpactPayload(t, cfg)
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// MockPayload is a short, illustrative example of the anonymous summary
// telemetry sends, shown in the one-time consent invite. It is a FIXED sample,
// not the user's real ledger (which can be dozens of programs and is the wall of
// text a consent prompt must never be); the real payload is available on demand
// via `ctx-wire telemetry preview`.
func MockPayload() string {
	example := impactPayload{
		Schema:       1,
		Event:        "impact",
		Version:      buildVersion,
		Commands:     128,
		RawBytes:     9_500_000,
		EmittedBytes: 1_300_000,
		BytesSaved:   8_200_000,
		TokensSaved:  2_050_000,
		// One example program is enough for a consent prompt; the real, full
		// breakdown is on demand via `ctx-wire telemetry preview`.
		Programs: map[string]ProgramTotals{
			"git": {Count: 22, RawBytes: 180_000, EmittedBytes: 100_000, BytesSaved: 80_000, TokensSaved: 20_000},
		},
		Agents: map[string]ProgramTotals{
			"claude": {Count: 128, RawBytes: 9_500_000, EmittedBytes: 1_300_000, BytesSaved: 8_200_000, TokensSaved: 2_050_000},
		},
	}
	b, err := json.MarshalIndent(example, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

// ShouldPreviewConsent reports whether the one-time telemetry notice should be
// shown: the user has not already seen it. The notice explains that aggregate
// telemetry is on and asks only about the per-command breakdown. Read errors read
// as "no" so it never blocks or repeats on a broken config.
func ShouldPreviewConsent() bool {
	cfg, err := readConfig()
	if err != nil {
		return false
	}
	return !cfg.PreviewShown
}

// MigrationNoticeIfPending returns the one-time non-interactive telemetry notice (and
// latches its own marker) for a previously-undecided user, or "" if it should not
// show. It is the non-interactive counterpart to the interactive notice: agents
// run `ctx-wire gain` without a terminal, so this discloses at the point of
// collection on that path. Its marker is SEPARATE from the interactive one, so a
// swallowed line here never suppresses the notice a human would see in a terminal.
func MigrationNoticeIfPending() string {
	cfg, err := readConfig()
	if err != nil {
		return ""
	}
	if cfg.MigrationNoticeShown {
		return "" // already shown
	}
	cfg.MigrationNoticeShown = true
	_ = writeConfig(cfg)
	return "anonymous aggregate telemetry is on; disable the per-command breakdown with `ctx-wire telemetry disable`"
}

// MarkPreviewShown latches the one-time consent invite so it is shown once.
// Best-effort: a write error simply means it may show again, never an error to
// the user.
func MarkPreviewShown() {
	cfg, err := readConfig()
	if err != nil {
		return
	}
	cfg.PreviewShown = true
	_ = writeConfig(cfg)
}
