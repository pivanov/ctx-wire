package telemetry

import "ctx-wire/internal/agent"

// buildVersion is the ctx-wire release version, stamped into every payload so the
// collector can chart per-version filter effectiveness (0.1.x vs 0.1.x+1). The
// telemetry package cannot see main.version, so main() calls SetBuildInfo early.
var buildVersion string

// SetBuildInfo records the running ctx-wire version for telemetry payloads. Call
// it once from main() before any telemetry send.
func SetBuildInfo(version string) { buildVersion = version }

// otherBucket is the catch-all label for any program or agent name not on the
// public allowlist. Telemetry NEVER sends a name it does not recognize: a
// private or codenamed binary (project-zeus-deploy) and any path-invoked
// program both collapse to this single label. The allowlist fails SAFE, an
// unknown PUBLIC tool is merely less granular (other), never an exposure, so
// drift costs a category label, not privacy.
//
// Keep this compatible with telemetry-worker's bucket key regex
// /^[a-z0-9._+-]{1,64}$/ so the deployed receiver stores it instead of dropping
// the catch-all bucket.
const otherBucket = "other"

// safePrograms is the curated allowlist of public program names telemetry may
// report by name: the union of programs ctx-wire ships a filter for (public by
// definition) and ubiquitous shell/coreutils/dev tools. It is hand-curated
// rather than parsed from each filter's match_command, because those regexes
// bury and alternate program names (e.g. "^(?:(npx|bunx)\s+|...)?biome\b",
// "^(grep|egrep|fgrep|git\s+grep)") and cannot be parsed reliably. Anything not
// listed maps to otherBucket. (A per-filter `program` field would be the clean
// way to auto-grow this later; it is a separate refinement.)
var safePrograms = newStringSet(
	// programs ctx-wire ships a filter for
	"cargo", "git", "go", "gofmt", "docker", "docker-compose", "kubectl",
	"terraform", "tofu", "mvn", "gradle", "gradlew", "java", "liquibase",
	"npm", "pnpm", "yarn", "npx", "bun", "bunx", "node", "deno", "nx",
	"tsc", "eslint", "biome", "prettier", "jest", "vitest", "playwright",
	"pip", "pip3", "python", "python3", "uv", "pytest", "mypy", "pylint",
	"pyright", "ruff", "flake8",
	"ruby", "bundle", "rspec", "rubocop", "rake", "rails",
	"rustc", "swift", "dotnet", "quarto",
	"brew", "apt", "apt-get", "sudo",
	"grep", "egrep", "fgrep", "rg", "ripgrep", "ag", "ack", "fd", "fdfind",
	"eza", "exa", "lsd", "http", "https", "httpie", "which", "whereis",
	// ubiquitous shell, coreutils, and common dev tools (public by definition)
	"ls", "cat", "find", "sed", "awk", "head", "tail", "sort", "uniq", "wc",
	"cut", "tr", "echo", "printf", "tee", "xargs", "diff", "patch", "file",
	"stat", "basename", "dirname", "realpath", "readlink", "seq", "date",
	"sleep", "watch", "env", "ps", "df", "du", "free", "uptime", "lsof",
	"kill", "killall", "make", "cmake", "gcc", "g++", "clang", "clang++",
	"ld", "ar", "nm", "objdump", "gdb", "lldb", "strace", "ltrace",
	"curl", "wget", "ssh", "scp", "rsync", "ping", "dig", "nslookup", "host",
	"tar", "gzip", "gunzip", "zip", "unzip", "jq", "yq", "base64",
	"sha256sum", "md5sum", "openssl", "systemctl", "journalctl",
	"mysql", "psql", "redis-cli", "sqlite3", "mongosh", "sqlcmd",
	"bash", "sh", "zsh", "fish", "pwsh", "powershell", "cmd", "ctx-wire",
	// Public tools surfaced from live telemetry's "other" bucket (and the
	// pre-allowlist history): ubiquitous coreutils + standard dev/infra CLIs,
	// public by definition. nl especially is the file-read replacement AGENTS.md
	// steers agents to, so it must stay visible instead of folding into (other).
	"nl", "gh", "just", "true", "time", "pwd", "uname", "mkdir", "rm", "cp",
	"mv", "touch", "chmod", "od", "cmp", "comm", "strings", "pgrep", "ss",
	"perl", "php", "helm", "gcloud", "glab", "ansible-playbook", "magick",
	"ffprobe", "pixi",
	// "read" is the program key the native-Read ceiling records its reclaim under
	// (internal/hook/claude_readceiling.go); allowlisted so that saving shows as a
	// "read" program in telemetry instead of folding into (other).
	"read",
)

// knownAgents allowlists agent labels. agent.Normalize only validates a slug's
// charset/length, so without this gate an arbitrary CTX_WIRE_AGENT=project-zeus
// would leave the machine. Unknown agents map to otherBucket, the same standard
// as programs.
var knownAgents = newStringSet(agent.Known...)

// safeProgramName validates and allowlists a raw program name: the lowercase
// name when public, otherBucket when valid-but-unknown (a private binary), or
// "" when not a well-formed token (callers skip "" as before).
func safeProgramName(raw string) string {
	if raw == otherBucket {
		return otherBucket // idempotent: re-sanitizing already-bucketed state must keep it
	}
	name := normalizeProgram(raw)
	if name == "" {
		return ""
	}
	if safePrograms[name] {
		return name
	}
	return otherBucket
}

// safeAgentName allowlists an agent label, otherBucket when valid-but-unknown.
func safeAgentName(raw string) string {
	if raw == otherBucket {
		return otherBucket // idempotent (see safeProgramName)
	}
	name := agent.Normalize(raw)
	if name == "" {
		return ""
	}
	if knownAgents[name] {
		return name
	}
	return otherBucket
}

func newStringSet(items ...string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, it := range items {
		s[it] = true
	}
	return s
}

// addBucket sums two per-program/per-agent counters (used when two unsafe keys
// collapse to the same safe label).
func addBucket(a, b ProgramTotals) ProgramTotals {
	return ProgramTotals{
		Count:        a.Count + b.Count,
		RawBytes:     a.RawBytes + b.RawBytes,
		EmittedBytes: a.EmittedBytes + b.EmittedBytes,
		BytesSaved:   a.BytesSaved + b.BytesSaved,
		TokensSaved:  a.TokensSaved + b.TokensSaved,
	}
}

// sanitizeBuckets rekeys a per-program/per-agent map through safe, merging keys
// that collapse to the same safe label. Returns nil for empty input.
func sanitizeBuckets(m map[string]ProgramTotals, safe func(string) string) map[string]ProgramTotals {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]ProgramTotals, len(m))
	for k, v := range m {
		sk := safe(k)
		if sk == "" {
			continue
		}
		out[sk] = addBucket(out[sk], v)
	}
	return out
}

// sanitizeTotals folds any unsafe program/agent keys in t onto the allowlist.
// Applied to state read from disk so a pre-upgrade telemetry-state.json holding
// private keys cannot leak them on the next send, and as a belt-and-suspenders
// guard at payload construction.
func sanitizeTotals(t *Totals) {
	t.Programs = sanitizeBuckets(t.Programs, safeProgramName)
	t.Agents = sanitizeBuckets(t.Agents, safeAgentName)
}
