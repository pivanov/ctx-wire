package filter

// DraftSpec is the subset of filter settings that `tune draft` infers. It exists
// so the drafter can compile and apply a candidate filter (to compute the
// expected output and the savings preview) without reaching into the unexported
// TOML types.
type DraftSpec struct {
	MatchCommand       string
	StripANSI          bool
	StripLinesMatching []string
	TruncateLinesAt    *int
	MaxLines           *int
	ReduceJSON         bool
}

// CompileDraft compiles a candidate draft filter so it can be applied to a
// sample. It mirrors the production compile path, so what the drafter previews
// and tests is exactly what the live filter would produce.
func CompileDraft(name string, spec DraftSpec) (*CompiledFilter, error) {
	return compile(name, tomlFilter{
		MatchCommand:       spec.MatchCommand,
		StripANSI:          spec.StripANSI,
		StripLinesMatching: spec.StripLinesMatching,
		TruncateLinesAt:    spec.TruncateLinesAt,
		MaxLines:           spec.MaxLines,
		ReduceJSON:         spec.ReduceJSON,
	})
}
