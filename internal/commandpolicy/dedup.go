package commandpolicy

import "path/filepath"

// dedupReadCommands are read-only commands whose repeated identical output is
// safe to dedup: re-running them does not change the world, so an unchanged
// result is genuinely unchanged. Deliberately conservative; anything that can
// have side effects is excluded.
var dedupReadCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true,
	"grep": true, "rg": true, "ag": true, "find": true, "fd": true,
	"ps": true, "df": true, "du": true, "stat": true, "file": true,
	"pwd": true, "env": true, "printenv": true, "tree": true, "which": true,
	"nl": true, "od": true, "cut": true, "sort": true, "uniq": true,
}

// gitReadSubcommands are the read-only git subcommands safe to dedup.
var gitReadSubcommands = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true,
	"branch": true, "tag": true, "describe": true, "blame": true,
}

// IsDedupEligible reports whether a command's repeated output is safe to dedup.
// It is intentionally narrower than, and unrelated to, ClassifyBypass (which is
// about interactive/streaming/dev-server bypass, not read safety). When in doubt
// it returns false: not deduping is always safe, deduping a side-effectful
// command is not.
func IsDedupEligible(name string, args []string) bool {
	base := filepath.Base(name)
	if dedupReadCommands[base] {
		return true
	}
	// git is read-only only for specific subcommands. A flag-prefixed form like
	// `git -C dir status` puts a flag in args[0], so it is conservatively treated
	// as ineligible (not deduped) rather than misclassified.
	if base == "git" && len(args) > 0 && gitReadSubcommands[args[0]] {
		return true
	}
	return false
}
