package commandpolicy

import (
	"path/filepath"
	"strings"
)

// dedupReadCommands are read-only commands whose repeated identical output is
// safe to dedup: re-running them does not change the world, so an unchanged
// result is genuinely unchanged. Deliberately conservative; anything that can
// have side effects is excluded.
//
// `env` is intentionally absent: `env NAME=v cmd` executes an arbitrary command
// and is not read-only, and detecting the safe (print-only) form reliably is not
// worth the risk, so env output is simply never deduped.
var dedupReadCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true,
	"grep": true, "rg": true, "ag": true, "find": true, "fd": true,
	"ps": true, "df": true, "du": true, "stat": true, "file": true,
	"pwd": true, "printenv": true, "tree": true, "which": true,
	"nl": true, "od": true, "cut": true, "sort": true, "uniq": true,
}

// writeFlagsByCommand lists, per command, the flags that turn an otherwise
// read-only command into one with side effects (deleting, executing, or writing
// a file). A command carrying any of these is NOT dedup-eligible, because the
// repeated run is no longer guaranteed to leave the world unchanged.
var writeFlagsByCommand = map[string][]string{
	// find can delete, exec, or write listings to a file.
	"find": {"-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls"},
	// fd (the find alternative) execs via -x/-X.
	"fd": {"-x", "--exec", "-X", "--exec-batch"},
}

// hasWriteFlag reports whether args contain a side-effecting flag for base. It
// also handles `sort -o FILE` / `--output=FILE`, which writes its output to a
// file rather than stdout.
func hasWriteFlag(base string, args []string) bool {
	if base == "sort" {
		for _, a := range args {
			if a == "-o" || a == "--output" || strings.HasPrefix(a, "-o") || strings.HasPrefix(a, "--output=") {
				return true
			}
		}
		return false
	}
	flags := writeFlagsByCommand[base]
	if flags == nil {
		return false
	}
	for _, a := range args {
		for _, w := range flags {
			if a == w {
				return true
			}
		}
	}
	return false
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
		// A normally read-only command becomes side-effecting with certain flags
		// (find -delete, sort -o FILE, ...); those are not safe to dedup.
		return !hasWriteFlag(base, args)
	}
	// git is read-only only for specific subcommands. A flag-prefixed form like
	// `git -C dir status` puts a flag in args[0], so it is conservatively treated
	// as ineligible (not deduped) rather than misclassified.
	if base == "git" && len(args) > 0 && gitReadSubcommands[args[0]] {
		return true
	}
	return false
}
