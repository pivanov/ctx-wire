package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// SetCaptureFileTools persists [hooks] capture_file_tools in ctx-wire's own
// config.toml and returns the path written. The edit is surgical (the file may
// carry user comments): replace the existing key line, else insert after the
// [hooks] header, else append a fresh section. Atomic via temp+rename.
func SetCaptureFileTools(enable bool) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("capture_file_tools = %t", enable)

	data, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return path, readErr
	}
	var out string
	switch {
	case errors.Is(readErr, fs.ErrNotExist) || strings.TrimSpace(string(data)) == "":
		out = "[hooks]\n" + line + "\n"
	default:
		out = upsertHooksKey(string(data), line)
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return path, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return path, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return path, err
	}
	return path, nil
}

var (
	captureKeyRe   = regexp.MustCompile(`^\s*capture_file_tools\s*=`)
	hooksHeaderRe  = regexp.MustCompile(`^\[hooks\]\s*(#.*)?$`)
	anySectionRe   = regexp.MustCompile(`^\s*\[`)
	hooksKeyIndent = ""
)

func upsertHooksKey(content, line string) string {
	lines := strings.Split(content, "\n")
	// Replace an existing key line wherever it is.
	for i, l := range lines {
		if captureKeyRe.MatchString(l) {
			lines[i] = hooksKeyIndent + line
			return strings.Join(lines, "\n")
		}
	}
	// Insert right after the [hooks] header.
	for i, l := range lines {
		if hooksHeaderRe.MatchString(strings.TrimSpace(l)) {
			out := append([]string{}, lines[:i+1]...)
			out = append(out, hooksKeyIndent+line)
			out = append(out, lines[i+1:]...)
			return strings.Join(out, "\n")
		}
	}
	// No [hooks] section: append one. Guard against a final partial line.
	_ = anySectionRe
	text := strings.TrimRight(content, "\n")
	if text != "" {
		text += "\n\n"
	}
	return text + "[hooks]\n" + line + "\n"
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	return "."
}
