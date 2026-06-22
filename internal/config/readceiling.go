package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

var (
	readCeilingKeyRe = regexp.MustCompile(`^\s*read_ceiling\s*=`)
	hooksHeaderRe    = regexp.MustCompile(`^\[hooks\]\s*(#.*)?$`)
	anySectionRe     = regexp.MustCompile(`^\s*\[`)
	hooksKeyIndent   = ""
)

// upsertHooksKey replaces an existing key line inside the [hooks] section with
// line, inserts it right after the [hooks] header if the key is absent, or
// appends a fresh [hooks] section if none exists. It is surgical: only the
// key inside [hooks] is touched; an identically-named key in another table is
// left alone.
func upsertHooksKey(content, line string, keyRe *regexp.Regexp) string {
	lines := strings.Split(content, "\n")
	// Replace an existing key line, but ONLY inside the [hooks] section: the
	// same key name under another table belongs to someone else, and rewriting
	// it would corrupt that section while silently leaving [hooks] unset.
	inHooks := false
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		switch {
		case hooksHeaderRe.MatchString(trimmed):
			inHooks = true
		case anySectionRe.MatchString(l):
			inHooks = false
		case inHooks && keyRe.MatchString(l):
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

// SetReadCeiling persists [hooks] read_ceiling in ctx-wire's own config.toml and
// returns the path written. mode is "off", "measure", or "on". The edit is
// surgical (preserves user comments): replace the existing key inside [hooks],
// else insert after the header, else append a fresh section. Atomic via
// temp+rename.
func SetReadCeiling(mode string) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("read_ceiling = %q", mode)

	data, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return path, readErr
	}
	var out string
	switch {
	case errors.Is(readErr, fs.ErrNotExist) || strings.TrimSpace(string(data)) == "":
		out = "[hooks]\n" + line + "\n"
	default:
		out = upsertHooksKey(string(data), line, readCeilingKeyRe)
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
