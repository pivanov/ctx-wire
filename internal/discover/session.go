package discover

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ctx-wire/internal/rewrite"
	"ctx-wire/internal/ui"
)

// SessionStat is ctx-wire adoption for one agent transcript: of the commands
// ctx-wire could route (Coverable), how many actually went through it (Covered).
type SessionStat struct {
	Agent     string
	File      string // transcript basename
	Coverable int
	Covered   int
	ModTime   time.Time
}

// AdoptionPct is the share of coverable commands that actually used ctx-wire.
func (s SessionStat) AdoptionPct() float64 {
	if s.Coverable == 0 {
		return 0
	}
	return float64(s.Covered) / float64(s.Coverable) * 100
}

// Sessions reports per-transcript ctx-wire adoption, newest first, capped at
// opts.TopN (0 = no cap). Read-only and local-only, like discover.
func Sessions(opts Options) ([]SessionStat, error) {
	var stats []SessionStat

	for _, base := range opts.ClaudeDirs {
		root := filepath.Join(base, "projects")
		if opts.Project != "" {
			root = filepath.Join(root, encodeClaudeProjectSlug(opts.Project))
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			if st, ok := sessionStat("claude", path, parseClaudeFile(path, opts.Since), d); ok {
				stats = append(stats, st)
			}
			return nil
		})
	}

	if opts.CodexDir != "" {
		sessions := filepath.Join(opts.CodexDir, "sessions")
		_ = filepath.WalkDir(sessions, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
				return nil
			}
			if st, ok := sessionStat("codex", path, parseCodexFile(path, opts.Project, opts.Since), d); ok {
				stats = append(stats, st)
			}
			return nil
		})
	}

	sort.Slice(stats, func(i, j int) bool { return stats[i].ModTime.After(stats[j].ModTime) })
	if opts.TopN > 0 && len(stats) > opts.TopN {
		stats = stats[:opts.TopN]
	}
	return stats, nil
}

func sessionStat(agent, path string, cmds []exec, d os.DirEntry) (SessionStat, bool) {
	covered, coverable := 0, 0
	for _, c := range cmds {
		cmd := strings.TrimSpace(c.command)
		if cmd == "" {
			continue
		}
		switch {
		case isCtxWireCommand(cmd):
			covered++
			coverable++
		case wrappable(cmd):
			coverable++
		}
	}
	if coverable == 0 {
		return SessionStat{}, false // no ctx-wire-relevant commands; skip the session
	}
	var mt time.Time
	if info, err := d.Info(); err == nil {
		mt = info.ModTime()
	}
	return SessionStat{Agent: agent, File: filepath.Base(path), Coverable: coverable, Covered: covered, ModTime: mt}, true
}

func isCtxWireCommand(cmd string) bool {
	return cmd == "ctx-wire" || strings.HasPrefix(cmd, "ctx-wire ")
}

// wrappable reports whether ctx-wire's hook would route some part of cmd. It
// uses the shape classifier (rewrite.Explain), which is host-independent, so a
// transcript from another machine classifies the same way.
func wrappable(cmd string) bool {
	for _, seg := range rewrite.Explain(cmd).Segments {
		if seg.Wrapped {
			return true
		}
	}
	return false
}

// FormatSessionsThemed renders the adoption table, newest first.
func FormatSessionsThemed(stats []SessionStat, theme ui.Theme) string {
	var b strings.Builder
	b.WriteString(theme.Heading("ctx-wire session: adoption") + "\n")
	if len(stats) == 0 {
		b.WriteString("no agent sessions with routable commands found\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%-7s %-30s %5s %5s  %s\n", "Agent", "Session", "Cmds", "Used", "Adoption")
	for _, s := range stats {
		file := s.File
		if len(file) > 30 {
			file = file[:29] + "…"
		}
		const width = 10
		filled := int(float64(width) * s.AdoptionPct() / 100)
		if filled < 0 {
			filled = 0
		}
		if filled > width {
			filled = width
		}
		bar := theme.OK.Render(strings.Repeat("█", filled)) + theme.Dim.Render(strings.Repeat("░", width-filled))
		// Aligned columns stay plain (ANSI codes would break %-Ns padding); color
		// only the trailing bar and percentage where alignment no longer matters.
		fmt.Fprintf(&b, "%-7s %-30s %5d %5d  %s %s\n",
			s.Agent, file, s.Coverable, s.Covered, bar, theme.Number.Render(fmt.Sprintf("%.1f%%", s.AdoptionPct())))
	}
	return b.String()
}
