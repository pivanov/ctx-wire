package filter

// FilterDecision reports how a command matches the registry, for diagnostics.
type FilterDecision struct {
	Matched        bool   // a filter matched
	Name           string // matched filter name (empty when no match)
	Normalized     bool   // matched only after path-program normalization
	NormalizedForm string // the normalized command (set when Normalized is true)
}

// Explain reports the matching decision for a command without applying it. It
// mirrors Find exactly (longest-match, then path-program normalization), but
// surfaces whether normalization was needed and which filter won, so callers
// can diagnose why a command is or is not filtered.
func (r *Registry) Explain(command string) FilterDecision {
	if best := r.find(command); best != nil {
		return FilterDecision{Matched: true, Name: best.Name}
	}
	if normalized := normalizeCommandProgram(command); normalized != command {
		if best := r.find(normalized); best != nil {
			return FilterDecision{Matched: true, Name: best.Name, Normalized: true, NormalizedForm: normalized}
		}
	}
	return FilterDecision{Matched: false}
}
