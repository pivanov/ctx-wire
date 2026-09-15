package hook

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// inLinkedWorktree reports whether dir lies inside a linked git worktree: some
// .git at or above dir is a FILE whose gitdir points into
// <common-dir>/worktrees/. A main worktree has a .git directory, and a submodule
// has a .git file pointing into .git/modules/, so neither counts by itself.
//
// The walk does NOT stop at the nearest .git. A submodule or nested repository
// inside a linked worktree has its own .git marker, and stopping there answered
// false for a session that is still isolated in the outer worktree, so the hook
// wrapped git again and the refusal came back. Every ancestor is checked.
//
// It is how the Claude hook recognizes a worktree-isolated session. Claude Code
// sends no field saying a session is isolated, but the payload's cwd follows the
// session into the worktree. The rule is deliberately broader than isolation
// alone: it also matches worktrees people create by hand, costing those sessions
// git output filtering. The narrower signal (cwd differing from
// CLAUDE_PROJECT_DIR) is not confirmed for sessions started with
// `claude --worktree`, and the two failure modes are not symmetric: a miss
// leaves every git command in the session refused, an over-match only leaves
// some git output unfiltered.
//
// It reads the filesystem only (one stat per ancestor, plus a small read for any
// .git file), never runs git, and treats a marker it cannot read as not a
// worktree, so the hook falls back to its normal behavior unless an ancestor
// says otherwise. It only runs for command lines that mention git.
func inLinkedWorktree(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	d := filepath.Clean(dir)
	for {
		gitPath := filepath.Join(d, ".git")
		if info, err := os.Stat(gitPath); err == nil && info.Mode().IsRegular() && gitFilePointsIntoWorktrees(gitPath) {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// gitFilePointsIntoWorktrees reports whether a .git file's gitdir line names a
// path under a worktrees/ directory, written with either separator.
func gitFilePointsIntoWorktrees(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// A .git file is a single short line; cap the read so a hostile file cannot
	// make the hook slurp megabytes.
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "gitdir:") {
			continue
		}
		gitdir := strings.ReplaceAll(strings.TrimSpace(strings.TrimPrefix(line, "gitdir:")), `\`, "/")
		return strings.Contains(gitdir, "/worktrees/")
	}
	return false
}
