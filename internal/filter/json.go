package filter

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaxJSONPassthrough bounds how much complete JSON is emitted verbatim under the
// "JSON payloads are not reduced" guarantee. A valid document up to this size is
// passed whole; a larger one is replaced (never cut mid-structure). Shared by
// the runner's jsonGuard and the MCP read_file path so both honor one limit. A
// var (not const) so tests can shrink it to exercise the oversize path.
var MaxJSONPassthrough = 1 << 20 // 1 MiB

// JSONPassthrough returns the text to emit for a body the caller has already
// confirmed is complete JSON (via IsCompleteJSON) and scrubbed. It honors the
// never-reduce rule: the whole document when it fits under MaxJSONPassthrough,
// or a size marker, never a line- or structure-level cut. whole is false when
// the marker was returned (document too large to pass inline). Recovery wording
// is the caller's job (the runner has a spool; read_file does not), so the
// marker here stays generic.
func JSONPassthrough(scrubbedJSON string) (text string, whole bool) {
	if len(scrubbedJSON) <= MaxJSONPassthrough {
		return scrubbedJSON, true
	}
	return fmt.Sprintf("[ctx-wire: %d-byte JSON document omitted (over the %d-byte JSON passthrough limit)]",
		len(scrubbedJSON), MaxJSONPassthrough), false
}

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
