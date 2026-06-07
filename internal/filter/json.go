package filter

import (
	"encoding/json"
	"strings"
)

// IsCompleteJSON reports whether s is a single complete JSON object or array. It
// is the cheap first-byte gate ('{' or '[') followed by json.Valid, shared by
// the runtime jsonGuard (whole-JSON passthrough) and `tune draft` (so the
// drafter never proposes a line-truncating transform on structured data, which
// would reintroduce mid-JSON corruption). NDJSON, being line-independent, is not
// a single document and stays truncatable.
func IsCompleteJSON(s string) bool {
	t := strings.TrimLeft(s, " \t\r\n")
	if t == "" || (t[0] != '{' && t[0] != '[') {
		return false
	}
	return json.Valid([]byte(s))
}
