package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// maxHookInput bounds how much of a hook payload we read from stdin. A real
// pre-tool payload is far smaller; the cap is defensive. An over-cap payload is
// rejected (not truncated), so a host can never make a malformed payload parse
// as a valid truncated prefix; every adapter treats the error as a fail-open
// passthrough, so the cap can never wrongly deny a command.
const maxHookInput = 1 << 20

// utf8BOM is the UTF-8 byte order mark some hosts (e.g. Cursor on Windows)
// prepend to the JSON. encoding/json rejects a leading BOM, so without stripping
// it the hook would silently parse nothing and quietly stop rewriting.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// readHookInput reads a hook payload from r, bounded by maxHookInput and with a
// leading UTF-8 BOM stripped. It reads one byte past the cap and rejects an
// oversize payload, so the full payload's validity is never changed by silent
// truncation.
func readHookInput(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxHookInput+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxHookInput {
		return nil, errors.New("hook payload exceeds cap")
	}
	return bytes.TrimPrefix(data, utf8BOM), nil
}

// toolInputWithCommand returns toolInput with only its "command" key replaced by
// command. Every other key is carried over byte-for-byte (booleans stay booleans,
// numbers stay numbers, unknown future keys survive), because hosts like Claude
// Code REPLACE the tool input with the hook's updatedInput rather than merging
// it: emitting only {"command": ...} would silently drop fields such as
// run_in_background, timeout, description, or dangerouslyDisableSandbox.
// It returns nil when toolInput is not a JSON object; callers fail open.
func toolInputWithCommand(toolInput json.RawMessage, command string) json.RawMessage {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(toolInput, &obj); err != nil || obj == nil {
		return nil
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return nil
	}
	obj["command"] = raw
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}
