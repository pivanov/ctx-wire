package mcpcompress

import (
	"compress/gzip"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

func loadPwFixture(t *testing.T) string {
	t.Helper()
	f, err := os.Open("testdata/playwright_github_snapshot.txt.gz")
	if err != nil {
		t.Skipf("fixture not present: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var pwRefRe = regexp.MustCompile(`\[ref=e\d+\]`)

// TestPlaywrightNeverRenumbers is the centerpiece, mirroring the chrome-devtools
// guard: every output line is byte-identical to an input line (purely subtractive)
// and every [ref=eN] in the output is an unchanged input ref. A renumbered ref
// would hand the agent a valid-looking reference to the WRONG element.
func TestPlaywrightNeverRenumbers(t *testing.T) {
	in := loadPwFixture(t)
	out, dropped := ReducePlaywrightSnapshot(in)
	if dropped == 0 || len(out) >= len(in) {
		t.Fatalf("expected real reduction; dropped=%d in=%d out=%d", dropped, len(in), len(out))
	}
	inLines := map[string]int{}
	for _, l := range strings.Split(in, "\n") {
		inLines[l]++
	}
	for _, l := range strings.Split(out, "\n") {
		if inLines[l] == 0 {
			t.Fatalf("reducer emitted a line not present byte-identical in the input:\n%q", l)
		}
	}
	inRefs := map[string]bool{}
	for _, r := range pwRefRe.FindAllString(in, -1) {
		inRefs[r] = true
	}
	for _, r := range pwRefRe.FindAllString(out, -1) {
		if !inRefs[r] {
			t.Fatalf("reducer produced a ref not in the input (renumbered/regenerated): %s", r)
		}
	}
}

func TestPlaywrightReducesMeaningfully(t *testing.T) {
	in := loadPwFixture(t)
	out, dropped := ReducePlaywrightSnapshot(in)
	pct := 100 * float64(len(in)-len(out)) / float64(len(in))
	t.Logf("playwright reduced %d -> %d chars (%.1f%% smaller), %d lines dropped", len(in), len(out), pct, dropped)
	if len(out) >= len(in) {
		t.Fatal("expected the reducer to shrink a real snapshot")
	}
	if pct < 8 {
		t.Errorf("reduction %.1f%% is suspiciously low; reducer may have regressed", pct)
	}
}

func TestPlaywrightSafeNoOpOnNonSnapshot(t *testing.T) {
	for _, s := range []string{"", "hello world", `{"some":"json"}`, "no refs here\njust text"} {
		out, dropped := ReducePlaywrightSnapshot(s)
		if out != s || dropped != 0 {
			t.Errorf("non-snapshot input must pass through unchanged: in=%q out=%q dropped=%d", s, out, dropped)
		}
	}
}

func TestPlaywrightIdempotent(t *testing.T) {
	in := loadPwFixture(t)
	once, _ := ReducePlaywrightSnapshot(in)
	twice, d2 := ReducePlaywrightSnapshot(once)
	if twice != once {
		t.Errorf("reduce must be idempotent; a second pass changed the output (dropped=%d)", d2)
	}
}

// TestPlaywrightAdversarialRefsSurvive is the parser-safety bar Review 2 required:
// refs mid-line, /url children, nested indentation, named vs nameless generics,
// and a malformed line. Every kept [ref=eN]/role/name must survive byte-identical,
// dropped subtrees must be fully gone, and the reducer must not crash.
func TestPlaywrightAdversarialRefsSurvive(t *testing.T) {
	in := strings.Join([]string{
		`- generic [ref=e1]:`,                          // nameless WRAPPER (has children) -> kept
		`  - banner [ref=e2]:`,                         // chrome -> whole subtree dropped
		`    - link "Logo" [ref=e3]:`,                  // inside banner -> dropped
		`      - /url: /`,                              // inside banner -> dropped
		`  - main [ref=e4]:`,                           // kept
		`    - link "Home" [ref=e5] [cursor=pointer]:`, // ref mid-line + trailing attr -> kept byte-identical
		`      - /url: "/home"`,                        // property of the kept link -> kept
		`      - img [ref=e6]`,                         // leaf -> kept
		`    - heading "Title" [level=1] [ref=e7]`,     // ref after an attr -> kept
		`      - text: Title`,                          // redundant text (== parent name) -> dropped
		`    - generic [ref=e8]`,                       // nameless LEAF (empty container) -> dropped
		`    - generic "named" [ref=e9]`,               // NAMED leaf -> kept (not an empty container)
		`    - malformed line with [ref=e10] no dash`,  // a "node" with role=malformed -> kept verbatim
	}, "\n")
	out, _ := ReducePlaywrightSnapshot(in)

	for _, want := range []string{"[ref=e1]", "[ref=e4]", "[ref=e5]", "[ref=e6]", "[ref=e7]", "[ref=e9]", "[ref=e10]"} {
		if !strings.Contains(out, want) {
			t.Errorf("kept ref %s missing from output:\n%s", want, out)
		}
	}
	for _, gone := range []string{"[ref=e2]", "[ref=e3]", "[ref=e8]"} {
		if strings.Contains(out, gone) {
			t.Errorf("dropped ref %s still present:\n%s", gone, out)
		}
	}
	// The kept link line (ref mid-line + trailing attr) must be byte-identical.
	if !strings.Contains(out, `    - link "Home" [ref=e5] [cursor=pointer]:`) {
		t.Errorf("kept link line was not preserved byte-identical:\n%s", out)
	}
	// The kept link's /url property must survive with it.
	if !strings.Contains(out, `      - /url: "/home"`) {
		t.Errorf("kept link's /url property was dropped:\n%s", out)
	}
}

// TestReduceSnapshotDispatch checks the dialect router: a Playwright [ref=eN] tree
// goes to the playwright reducer, a chrome-devtools uid= tree to the uid reducer.
func TestReduceSnapshotDispatch(t *testing.T) {
	pw := "- banner [ref=e1]:\n  - link \"x\" [ref=e2]:\n- main [ref=e3]:"
	if out, d := ReduceSnapshot(pw); d == 0 || strings.Contains(out, "[ref=e1]") {
		t.Errorf("playwright dialect must route to the playwright reducer and drop the banner: %q (d=%d)", out, d)
	}
	cd := `uid=1_1 main "x"`
	if out, _ := ReduceSnapshot(cd); out != cd {
		t.Errorf("chrome-devtools dialect must route to the uid reducer (no-op here): %q", out)
	}
}
