package commandpolicy

import "testing"

func TestExcludedCommands(t *testing.T) {
	t.Cleanup(func() { SetExcludedCommands(nil) })
	SetExcludedCommands([]string{"curl", " playwright ", ""})

	if !IsExcluded("curl") {
		t.Error("curl should be excluded")
	}
	if !IsExcluded("/usr/bin/curl") {
		t.Error("exclusion must match by basename")
	}
	if !IsExcluded("playwright") {
		t.Error("blanks should be trimmed; playwright excluded")
	}
	if IsExcluded("git") {
		t.Error("git was not excluded")
	}

	// Excluded commands bypass capture (run raw) in the runner path.
	if bypass, reason := ClassifyBypass("curl", []string{"https://x"}); !bypass || reason != "excluded by config" {
		t.Errorf("ClassifyBypass(curl) = (%v, %q), want (true, excluded by config)", bypass, reason)
	}
	if bypass, _ := ClassifyBypass("git", []string{"status"}); bypass {
		t.Error("git must not bypass")
	}

	SetExcludedCommands(nil)
	if IsExcluded("curl") {
		t.Error("SetExcludedCommands(nil) should clear the list")
	}
}
