package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"ctx-wire/internal/tee"
)

const minFetchPrefix = 8 // minimum hex prefix length accepted by fetch

// cmdFetch recovers the full scrubbed output that ctx-wire spooled for a
// truncated or failed command, addressed by the hash shown in the hint footer.
func cmdFetch(args []string) int {
	if isHelpArg(args) || len(args) == 0 {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire fetch <hash>"},
			summary: "Recover the full scrubbed output ctx-wire spooled for a truncated/failed command.",
			examples: []string{
				"ctx-wire fetch a1b2c3d4e5f6",
			},
			notes: []string{
				"The output is already secret-scrubbed.",
				"Handles are evicted once 20 newer commands spool; if a hash is gone, re-run the command.",
			},
		})
		return 0
	}
	hash := strings.ToLower(strings.TrimSpace(args[0]))
	if !isHexPrefix(hash) || len(hash) < minFetchPrefix {
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: %q is not a valid output handle\n", args[0])
		return 2
	}
	path, ok := tee.Resolve(hash)
	if !ok {
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: no spooled output for %s (it may have been evicted; re-run the command)\n", hash)
		return 1
	}
	f, err := os.Open(path)
	if err != nil {
		// TOCTOU: a concurrent spool's cleanup() can evict this file between
		// Resolve and Open. Report that as an eviction (same message as a miss),
		// not a raw I/O error, so the agent gets one consistent recovery instruction.
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "ctx-wire fetch: no spooled output for %s (it may have been evicted; re-run the command)\n", hash)
			return 1
		}
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: %v\n", err)
		return 1
	}
	defer f.Close()
	if _, err := io.Copy(os.Stdout, f); err != nil { // file is already scrubbed
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: %v\n", err)
		return 1
	}
	return 0
}

// isHexPrefix reports whether s consists entirely of lowercase hex characters
// (0-9, a-f). An empty string returns false.
func isHexPrefix(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
