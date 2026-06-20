package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"ctx-wire/internal/tee"
)

const minFetchPrefix = 8 // minimum hex prefix length accepted by fetch

// cmdFetch recovers the full scrubbed output that ctx-wire spooled for a
// truncated or failed command, addressed by the hash shown in the hint footer.
// The optional --lines A-B flag returns only lines A through B (1-based, inclusive,
// clamped to the actual line count).
func cmdFetch(args []string) int {
	if isHelpArg(args) || len(args) == 0 {
		printHelp(os.Stdout, helpDoc{
			usage:   []string{"ctx-wire fetch <hash> [--lines A-B]"},
			summary: "Recover the full scrubbed output ctx-wire spooled for a truncated/failed command.",
			flags: [][2]string{
				{"--lines A-B", "return only lines A through B (1-based, inclusive; clamped to file length)"},
			},
			examples: []string{
				"ctx-wire fetch a1b2c3d4e5f6",
				"ctx-wire fetch a1b2c3d4e5f6 --lines 100-200",
			},
			notes: []string{
				"The output is already secret-scrubbed.",
				"Handles are evicted once the spool exceeds ~200 files or ~50 MiB; if a hash is gone, re-run the command, or re-Read the source file.",
			},
		})
		return 0
	}

	// Parse args: first positional is the hash; --lines A-B is optional.
	var hashArg string
	var lineStart, lineEnd int // 0 means not set (full output)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--lines":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "ctx-wire fetch: --lines requires A-B value")
				return 2
			}
			i++
			a, b, err := parseLineRange(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "ctx-wire fetch: --lines %q: %v\n", args[i], err)
				return 2
			}
			lineStart, lineEnd = a, b
		case strings.HasPrefix(arg, "--lines="):
			val := strings.TrimPrefix(arg, "--lines=")
			a, b, err := parseLineRange(val)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ctx-wire fetch: --lines %q: %v\n", val, err)
				return 2
			}
			lineStart, lineEnd = a, b
		case strings.HasPrefix(arg, "-") && arg != "-":
			fmt.Fprintf(os.Stderr, "ctx-wire fetch: unknown flag %q\n", arg)
			return 2
		default:
			if hashArg != "" {
				fmt.Fprintln(os.Stderr, "ctx-wire fetch: too many arguments")
				return 2
			}
			hashArg = strings.ToLower(strings.TrimSpace(arg))
		}
	}

	if hashArg == "" {
		fmt.Fprintln(os.Stderr, "ctx-wire fetch: missing hash argument")
		return 2
	}
	if !isHexPrefix(hashArg) || len(hashArg) < minFetchPrefix {
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: %q is not a valid output handle\n", hashArg)
		return 2
	}

	path, ok := tee.Resolve(hashArg)
	if !ok {
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: no spooled output for %s (it may have been evicted; re-run the command, or re-Read the source file)\n", hashArg)
		tee.IncrFetchStats(tee.FetchStats{Miss: 1})
		return 1
	}
	f, err := os.Open(path)
	if err != nil {
		// TOCTOU: a concurrent spool's cleanup() can evict this file between
		// Resolve and Open. Report that as an eviction (same message as a miss),
		// not a raw I/O error, so the agent gets one consistent recovery instruction.
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "ctx-wire fetch: no spooled output for %s (it may have been evicted; re-run the command, or re-Read the source file)\n", hashArg)
			tee.IncrFetchStats(tee.FetchStats{Miss: 1})
			return 1
		}
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: %v\n", err)
		return 1
	}
	defer f.Close()

	if lineStart == 0 {
		// Full output.
		n, err := io.Copy(os.Stdout, f) // file is already scrubbed
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire fetch: %v\n", err)
			return 1
		}
		tee.IncrFetchStats(tee.FetchStats{Full: 1, BytesReturned: n})
		return 0
	}

	// Ranged output: read lines A..B (1-based inclusive).
	linesOut, bytesOut, rc := emitLines(f, lineStart, lineEnd)
	tee.IncrFetchStats(tee.FetchStats{Ranged: 1, BytesReturned: bytesOut, LinesReturned: linesOut})
	return rc
}

// emitLines reads lines [start, end] (1-based, inclusive) from r, writing them
// to stdout. end is clamped to the actual line count; if start > total, a note
// is printed and nothing is emitted. Returns (linesEmitted, bytesEmitted, exitCode).
func emitLines(r io.Reader, start, end int) (int64, int64, int) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB per line max
	lineNum := 0
	var bytesOut int64
	var linesOut int64
	for scanner.Scan() {
		lineNum++
		if lineNum < start {
			continue
		}
		if lineNum > end {
			break
		}
		line := scanner.Bytes()
		// Write line with newline terminator.
		n, err := os.Stdout.Write(append(line, '\n'))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ctx-wire fetch: %v\n", err)
			return linesOut, bytesOut, 1
		}
		bytesOut += int64(n)
		linesOut++
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: %v\n", err)
		return linesOut, bytesOut, 1
	}
	if start > lineNum {
		// start is beyond the end of the file.
		fmt.Fprintf(os.Stderr, "ctx-wire fetch: --lines %d-%d: file has only %d lines (no output)\n", start, end, lineNum)
	}
	return linesOut, bytesOut, 0
}

// parseLineRange parses "A-B" into (a, b) where a >= 1 and b >= a.
func parseLineRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want A-B (e.g. 10-20)")
	}
	a, err := strconv.Atoi(parts[0])
	if err != nil || a < 1 {
		return 0, 0, fmt.Errorf("start line must be a positive integer, got %q", parts[0])
	}
	b, err := strconv.Atoi(parts[1])
	if err != nil || b < 1 {
		return 0, 0, fmt.Errorf("end line must be a positive integer, got %q", parts[1])
	}
	if b < a {
		return 0, 0, fmt.Errorf("end line (%d) must be >= start line (%d)", b, a)
	}
	return a, b, nil
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
