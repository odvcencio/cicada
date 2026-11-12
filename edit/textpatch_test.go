package edit

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"
)

// numbered returns n lines "line 1" to "line n", each ending in newline.
func numbered(n int, newline string) []byte {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d%s", i, newline)
	}
	return []byte(b.String())
}

// replaceLine returns text with line n (from 1) replaced by with.
func replaceLine(text []byte, n int, with string) []byte {
	lines := bytes.SplitAfter(text, []byte("\n"))
	lines[n-1] = []byte(with)
	return bytes.Join(lines, nil)
}

// TestDiffThenPatchGivesTheTarget checks apply(from, diff(from, to)) = to,
// for line endings LF and CRLF, texts with and without a final newline, empty
// texts, a text too large for the line table, and random edits.
func TestDiffThenPatchGivesTheTarget(t *testing.T) {
	type pair struct{ name, from, to string }
	pairs := []pair{
		{"same", "a\nb\n", "a\nb\n"},
		{"from empty", "", "a\nb\n"},
		{"to empty", "a\nb\n", ""},
		{"one line", "a\n", "b\n"},
		{"no final newline", "a\nb", "a\nc"},
		{"adds final newline", "a\nb", "a\nb\n"},
		{"drops final newline", "a\nb\n", "a\nb"},
		{"append", "a\nb\n", "a\nb\nc\n"},
		{"prepend", "a\nb\n", "z\na\nb\n"},
		{"crlf", "a\r\nb\r\nc\r\n", "a\r\nB\r\nc\r\n"},
		{"crlf to lf", "a\r\nb\r\n", "a\nb\n"},
		{"two hunks", string(numbered(40, "\n")), string(replaceLine(replaceLine(numbered(40, "\n"), 3, "three\n"), 35, "thirty-five\n"))},
	}
	big := numbered(1500, "\n")
	pairs = append(pairs, pair{"over the line table", string(big), string(replaceLine(replaceLine(big, 10, "ten\n"), 1400, "x\ny\n"))})
	random := rand.New(rand.NewSource(7))
	words := []string{"a\n", "b\n", "c\n", "a\r\n", "\n", "d", "tempo 120\n"}
	for i := 0; i < 300; i++ {
		lines := make([]string, random.Intn(30))
		for j := range lines {
			lines[j] = words[random.Intn(len(words)-1)]
		}
		from := strings.Join(lines, "")
		edited := append([]string(nil), lines...)
		for k := random.Intn(5); k >= 0; k-- {
			at := random.Intn(len(edited) + 1)
			switch random.Intn(3) {
			case 0:
				edited = append(edited[:at], append([]string{words[random.Intn(len(words))]}, edited[at:]...)...)
			case 1:
				if at < len(edited) {
					edited = append(edited[:at], edited[at+1:]...)
				}
			default:
				if at < len(edited) {
					edited[at] = words[random.Intn(len(words))]
				}
			}
		}
		pairs = append(pairs, pair{fmt.Sprintf("random %d", i), from, strings.Join(edited, "")})
	}
	moves := 0
	for _, p := range pairs {
		hunks := DiffText([]byte(p.from), []byte(p.to))
		got, err := ApplyHunks([]byte(p.from), hunks)
		if err != nil || string(got) != p.to {
			t.Fatalf("%s: apply(from, diff) = %q, %v; want %q (hunks %+v)", p.name, got, err, p.to, hunks)
		}
		// Another writer added a line on top. The change follows its lines,
		// or is refused when its place is not certain; it never lands
		// elsewhere.
		moved := "// another writer\n" + p.from
		if rebased, err := RebaseText([]byte(moved), []byte(p.from), []byte(p.to)); err == nil {
			moves++
			if string(rebased) != "// another writer\n"+p.to {
				t.Fatalf("%s: rebase on moved text = %q, want %q", p.name, rebased, "// another writer\n"+p.to)
			}
		}
		if p.from == p.to && len(hunks) != 0 {
			t.Fatalf("%s: equal texts give hunks %+v", p.name, hunks)
		}
	}
	t.Logf("%d of %d pairs rebased onto moved text; the others were refused", moves, len(pairs))
	if moves < len(pairs)/2 {
		t.Fatalf("only %d of %d pairs rebased onto moved text", moves, len(pairs))
	}
}

// A patch made on one text applies to a text that another writer changed in
// other lines, and is refused, naming its line, when the same lines changed.
func TestPatchFollowsMovedTextAndRefusesTheSameLines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		from := numbered(30, newline)
		to := replaceLine(from, 12, "twelve"+newline)
		hunks := DiffText(from, to)
		if len(hunks) != 1 || hunks[0].Line != 9 {
			t.Fatalf("hunks %+v", hunks)
		}
		// Another writer inserted two lines at the top and changed line 25.
		moved := append([]byte("new 1"+newline+"new 2"+newline), replaceLine(from, 25, "changed"+newline)...)
		got, err := ApplyHunks(moved, hunks)
		want := append([]byte("new 1"+newline+"new 2"+newline), replaceLine(replaceLine(from, 25, "changed"+newline), 12, "twelve"+newline)...)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("moved text %q: %q, %v", newline, got, err)
		}
		// An unchanged line around the change changed, at the hunk's own
		// line: the hunk still applies there by its changed line and its
		// nearer neighbours.
		context := replaceLine(from, 9, "nine"+newline)
		got, err = ApplyHunks(context, hunks)
		if err != nil || !bytes.Equal(got, replaceLine(context, 12, "twelve"+newline)) {
			t.Fatalf("changed context %q: %q, %v", newline, got, err)
		}
		// The changed line's own neighbour changed: no window keeps an
		// unchanged line on that side, so the hunk is refused.
		if got, err = ApplyHunks(replaceLine(from, 11, "eleven"+newline), hunks); err == nil {
			t.Fatalf("changed neighbour %q was fitted: %q", newline, got)
		}
		// The same, after the text moved: a smaller window is tried only at
		// the hunk's own line, so the hunk is refused.
		movedContext := append([]byte("new"+newline), context...)
		if got, err = ApplyHunks(movedContext, hunks); err == nil {
			t.Fatalf("changed context on moved text %q was fitted: %q", newline, got)
		}
		// The same line changed: refused, with the hunk's line.
		_, err = ApplyHunks(replaceLine(from, 12, "mine"+newline), hunks)
		var mismatch *HunkError
		if !errors.As(err, &mismatch) || mismatch.Hunk != 0 || mismatch.Line != 9 || !strings.Contains(err.Error(), "line 9") {
			t.Fatalf("same line %q: %v", newline, err)
		}
	}
}

func TestPatchRefusesAmbiguousOverlappingAndContextlessHunks(t *testing.T) {
	text := []byte("a\nb\na\nb\nc\n")
	var refusal *HunkError
	// Not at its line, and at lines 1 and 3 alike: refused, naming both.
	if _, err := ApplyHunks(text, []Hunk{{Line: 2, Old: "a\nb\n", New: "x\n"}}); !errors.As(err, &refusal) || refusal.Reason != "hunk_ambiguous" || refusal.Places != 2 || fmt.Sprint(refusal.Matches) != "[1 3]" {
		t.Fatalf("ambiguous: %+v", err)
	}
	// At its line: the line pins it, even with a copy elsewhere.
	if got, err := ApplyHunks(text, []Hunk{{Line: 3, Old: "a\nb\n", New: "x\n"}}); err != nil || string(got) != "a\nb\nx\nc\n" {
		t.Fatalf("at its line: %q, %v", got, err)
	}
	if _, err := ApplyHunks(text, []Hunk{{Line: 1, Old: "a\nb\na\n", New: "x\n"}, {Line: 3, Old: "a\nb\n", New: "y\n"}}); !errors.As(err, &refusal) || refusal.Reason != "hunk_overlap" || refusal.Hunk != 1 || refusal.Other != 0 || !strings.Contains(err.Error(), "hunk 1 also changes") {
		t.Fatalf("overlap: %v", err)
	}
	// An insertion needs an unchanged line beside it, so it cannot land blind.
	for _, line := range []int{1, 6, 7} {
		if _, err := ApplyHunks(text, []Hunk{{Line: line, Old: "", New: "z\n"}}); !errors.As(err, &refusal) || refusal.Reason != "hunk_needs_context" {
			t.Fatalf("insertion at line %d without context: %v", line, err)
		}
	}
	if got, err := ApplyHunks(nil, []Hunk{{Line: 1, Old: "", New: "z\n"}}); err != nil || string(got) != "z\n" {
		t.Fatalf("an empty text takes an insertion: %q, %v", got, err)
	}
	if got, err := ApplyHunks(text, []Hunk{{Line: 1, Old: "a\n", New: "A\n"}, {Line: 2, Old: "b\n", New: "B\n"}}); err != nil || string(got) != "A\nB\na\nb\nc\n" {
		t.Fatalf("adjacent hunks: %q, %v", got, err)
	}
	if _, err := ApplyHunks(text, []Hunk{{Line: 0, Old: "a\n", New: "b\n"}}); !errors.As(err, &refusal) || refusal.Reason != "hunk_mismatch" {
		t.Fatalf("line 0: %v", err)
	}
}

// The two traces of the review of #351: on moved text the old search put the
// change in the wrong pattern, and put an insertion into unrelated text.
func TestPatchRefusesTheReviewTraces(t *testing.T) {
	from := []byte("pattern one {\n  x\n}\npattern two {\n  x\n}\n")
	to := []byte("pattern one {\n  x\n}\npattern two {\n  y\n}\n")
	moved := []byte("// a\n// b\n// c\npattern one {\n  x\n}\npattern drums {\n  x\n}\n")
	var refusal *HunkError
	if got, err := ApplyHunks(moved, DiffText(from, to)); !errors.As(err, &refusal) {
		t.Fatalf("renamed pattern: %q, %v", got, err)
	}
	// Without the end mark the hunk is still refused: its whole old lines
	// stand nowhere, and the smaller window that once landed in pattern one
	// (three lines dropped, three lines added above) is not tried.
	unmarked := DiffText(from, to)
	unmarked[0].End = false
	if got, err := ApplyHunks(moved, unmarked); !errors.As(err, &refusal) || refusal.Reason != "hunk_mismatch" {
		t.Fatalf("renamed pattern without the end mark: %q, %v", got, err)
	}
	if got, err := RebaseText(moved, from, to); err == nil {
		t.Fatalf("RebaseText placed it: %q", got)
	}
	insertion := DiffText([]byte("a\nb\nc\nd\ne\nf\n"), []byte("a\nb\nc\nX\nd\ne\nf\n"))
	if got, err := ApplyHunks([]byte("p\nq\nr\ns\nt\n"), insertion); !errors.As(err, &refusal) || refusal.Reason != "hunk_mismatch" {
		t.Fatalf("insertion into unrelated text: %q, %v", got, err)
	}
	// The same change still follows a writer who only added lines above.
	if got, err := ApplyHunks(append([]byte("// new\n"), from...), DiffText(from, to)); err != nil || string(got) != "// new\n"+string(to) {
		t.Fatalf("moved by one line: %q, %v", got, err)
	}
}

// Duplicate blocks that moved: the hunk applies only where its lines are
// unique, and the same lines twice are refused unless the hunk sits at its line.
func TestPatchDuplicatesThatMoved(t *testing.T) {
	block := "  1 . 5 .\n  5 . 1 .\n  1 . . .\n"
	from := []byte("pattern a {\n" + block + "}\npattern b {\n" + block + "}\n")
	to := bytes.Replace(from, []byte("pattern b {\n  1 . 5 ."), []byte("pattern b {\n  1 . 7 ."), 1)
	hunks := DiffText(from, to)
	// The context holds "pattern b {", so after a move the hunk is unique.
	moved := append([]byte("// moved\n// twice\n"), from...)
	if got, err := ApplyHunks(moved, hunks); err != nil || string(got) != "// moved\n// twice\n"+string(to) {
		t.Fatalf("unique after a move: %q, %v", got, err)
	}
	// Without the header in the hunk, the two blocks are alike: refused.
	bare := []Hunk{{Line: 7, Old: block, New: strings.Replace(block, "1 . 5 .", "1 . 7 .", 1)}}
	var refusal *HunkError
	if _, err := ApplyHunks(moved, bare); !errors.As(err, &refusal) || refusal.Reason != "hunk_ambiguous" || refusal.Places != 2 {
		t.Fatalf("alike blocks after a move: %v", err)
	}
	if got, err := ApplyHunks(from, bare); err != nil || !bytes.Equal(got, to) {
		t.Fatalf("alike blocks at the hunk's line: %q, %v", got, err)
	}
}

// New lines without a final newline must end the text; elsewhere they would
// join two lines. Both ends of the text.
func TestPatchKeepsLinesApart(t *testing.T) {
	text := []byte("// header\ntempo 120\nx\n")
	var refusal *HunkError
	if _, err := ApplyHunks(text, []Hunk{{Line: 1, Old: "// header\n", New: "// header2"}}); !errors.As(err, &refusal) || refusal.Reason != "hunk_newline" {
		t.Fatalf("first line: %v", err)
	}
	if got, err := ApplyHunks(text, []Hunk{{Line: 3, Old: "x\n", New: "y"}}); err != nil || string(got) != "// header\ntempo 120\ny" {
		t.Fatalf("last line: %q, %v", got, err)
	}
	if got, err := ApplyHunks([]byte("a\nb"), []Hunk{{Line: 2, Old: "b", New: "c\nd"}}); err != nil || string(got) != "a\nc\nd" {
		t.Fatalf("last line without a newline: %q, %v", got, err)
	}
	// Old without its newline is only the last line of a text without one.
	if _, err := ApplyHunks(text, []Hunk{{Line: 1, Old: "// header", New: "// h\n"}}); !errors.As(err, &refusal) || refusal.Reason != "hunk_mismatch" {
		t.Fatalf("old without a newline at line 1: %v", err)
	}
}

// A hunk whose old lines stand at very many places costs at most eleven
// confirmations: the search stops counting there.
func TestPatchAmbiguityStopsCounting(t *testing.T) {
	text := []byte(strings.Repeat("a\n", 50000))
	var refusal *HunkError
	start := time.Now()
	_, err := ApplyHunks(append([]byte("moved\n"), text...), []Hunk{{Line: 1, Old: strings.Repeat("a\n", 1000), New: "b\n"}})
	took := time.Since(start)
	if !errors.As(err, &refusal) || refusal.Reason != "hunk_ambiguous" || refusal.Places != 11 || len(refusal.Matches) != 10 || !strings.Contains(err.Error(), "more than 10 places") {
		t.Fatalf("many places: %v", err)
	}
	t.Logf("a 1,000-line hunk on 50,001 repeated lines: %s", took)
	if took > 5*time.Second {
		t.Fatalf("ambiguity took %s", took)
	}
}

func TestPatchLimits(t *testing.T) {
	text := []byte("a\n")
	many := make([]Hunk, MaxHunks+1)
	for i := range many {
		many[i] = Hunk{Line: 1, Old: "a\n", New: "a\n"}
	}
	var refusal *HunkError
	if _, err := ApplyHunks(text, many); !errors.As(err, &refusal) || refusal.Reason != "hunk_limit" || refusal.Limit != MaxHunks || refusal.Value != MaxHunks+1 {
		t.Fatalf("hunk count: %v", err)
	}
	big := []Hunk{{Line: 1, Old: "a\n", New: strings.Repeat("b", MaxPatchBytes) + "\n"}}
	if _, err := ApplyHunks(text, big); !errors.As(err, &refusal) || refusal.Reason != "hunk_limit" || refusal.Limit != MaxPatchBytes {
		t.Fatalf("hunk bytes: %v", err)
	}
}

// The most a patch under the limits can cost: as many hunks as allowed, each
// with a full window, on a 20,000-line text that moved, so every hunk is found
// by a pass over the whole text.
func TestPatchWorstCaseUnderTheLimits(t *testing.T) {
	var text strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&text, "line %d\n", i)
	}
	from := []byte(text.String())
	hunks := make([]Hunk, 0, MaxHunks)
	for i := 0; i < MaxHunks; i++ {
		at := 10 + i*19
		old := fmt.Sprintf("line %d\nline %d\nline %d\nline %d\nline %d\nline %d\nline %d\n", at-3, at-2, at-1, at, at+1, at+2, at+3)
		hunks = append(hunks, Hunk{Line: at - 2, Old: old, New: strings.Replace(old, fmt.Sprintf("line %d\n", at), "changed\n", 1)})
	}
	moved := append([]byte("// moved\n"), from...)
	start := time.Now()
	got, err := ApplyHunks(moved, hunks)
	took := time.Since(start)
	if err != nil || bytes.Count(got, []byte("changed\n")) != MaxHunks {
		t.Fatalf("worst case: %v", err)
	}
	t.Logf("%d hunks on a moved %d-line text: %s", MaxHunks, 20001, took)
	if took > 10*time.Second {
		t.Fatalf("worst case under the limits took %s", took)
	}
}

// A patch made on one text, applied to the text after another writer's
// random edits: either the change lands where its author meant, or the hunk is
// refused. Lines repeat, so "meant" is judged as text allows: there must be a
// largest alignment of the base onto the other writer's text (the longest
// common subsequence, any of them) that holds the author's replaced lines
// together at the place the change landed, or for an insertion one of its two
// neighbouring lines right beside it. The only other outcome allowed is the
// one the rules allow by design: each hunk's whole old lines stand at the
// line it names, or at the only place they stand in the other writer's text,
// or end it for a hunk marked end. In a run of repeated lines the other
// writer's edits can make such a place that the longest alignment does not
// choose; the test counts those and fails on anything else.
func TestPatchPlacesWhereMeantOrRefuses(t *testing.T) {
	const trials = 20000
	random := rand.New(rand.NewSource(12))
	words := []string{"a\n", "b\n", "}\n", "pattern p {\n", "  x . x .\n", "\n", "c\n"}
	word := func() string { return words[random.Intn(len(words))] }
	var meant, byRule, refused int
	for trial := 0; trial < trials; trial++ {
		base := make([]string, 3+random.Intn(25))
		for i := range base {
			base[i] = word()
		}
		i := random.Intn(len(base) + 1)
		j := i + random.Intn(min(3, len(base)-i)+1)
		added := make([]string, random.Intn(3))
		for k := range added {
			added[k] = word()
		}
		mine := slices.Concat(base[:i], added, base[j:])
		if slices.Equal(mine, base) {
			continue
		}
		// The same change in its smallest form: lines the region keeps at
		// either end are not part of it.
		for i < j && len(added) > 0 && base[i] == added[0] {
			i, added = i+1, added[1:]
		}
		for i < j && len(added) > 0 && base[j-1] == added[len(added)-1] {
			j, added = j-1, added[:len(added)-1]
		}
		theirs := slices.Clone(base)
		for edits := random.Intn(5); edits >= 0; edits-- {
			at := random.Intn(len(theirs) + 1)
			switch random.Intn(3) {
			case 0:
				theirs = slices.Insert(theirs, at, word())
			case 1:
				if at < len(theirs) && len(theirs) > 1 {
					theirs = slices.Delete(theirs, at, at+1)
				}
			default:
				if at < len(theirs) {
					theirs[at] = word()
				}
			}
		}
		hunks := DiffText([]byte(strings.Join(base, "")), []byte(strings.Join(mine, "")))
		got, err := ApplyHunks([]byte(strings.Join(theirs, "")), hunks)
		if err != nil {
			refused++
			continue
		}
		// prefix[x][y] and suffix[x][y]: the longest common subsequence of
		// base[:x] and theirs[:y], and of base[x:] and theirs[y:].
		n, m := len(base), len(theirs)
		prefix, suffix := make([][]int, n+1), make([][]int, n+2)
		for x := range prefix {
			prefix[x] = make([]int, m+1)
		}
		for x := range suffix {
			suffix[x] = make([]int, m+2)
		}
		for x := 1; x <= n; x++ {
			for y := 1; y <= m; y++ {
				if base[x-1] == theirs[y-1] {
					prefix[x][y] = prefix[x-1][y-1] + 1
				} else {
					prefix[x][y] = max(prefix[x-1][y], prefix[x][y-1])
				}
			}
		}
		for x := n - 1; x >= 0; x-- {
			for y := m - 1; y >= 0; y-- {
				if base[x] == theirs[y] {
					suffix[x][y] = suffix[x+1][y+1] + 1
				} else {
					suffix[x][y] = max(suffix[x+1][y], suffix[x][y+1])
				}
			}
		}
		total := prefix[n][m]
		var allowed []string
		if j > i {
			for at := 0; at+j-i <= m; at++ {
				if slices.Equal(base[i:j], theirs[at:at+j-i]) && prefix[i][at]+j-i+suffix[j][at+j-i] == total {
					allowed = append(allowed, strings.Join(slices.Concat(theirs[:at], added, theirs[at+j-i:]), ""))
				}
			}
		} else {
			for at := 0; at <= m; at++ {
				left := i > 0 && at > 0 && base[i-1] == theirs[at-1] && prefix[i-1][at-1]+1+suffix[i][at] == total
				right := i < n && at < m && base[i] == theirs[at] && prefix[i][at]+1+suffix[i+1][at+1] == total
				if left || right {
					allowed = append(allowed, strings.Join(slices.Concat(theirs[:at], added, theirs[at:]), ""))
				}
			}
		}
		if slices.Contains(allowed, string(got)) {
			meant++
			continue
		}
		text := strings.Join(theirs, "")
		lines := strings.SplitAfter(text, "\n")
		standsAt := func(at int, old string) bool {
			return at >= 0 && at <= len(lines) && strings.HasPrefix(strings.Join(lines[at:], ""), old)
		}
		allowedByRule := true
		for _, h := range hunks {
			places := 0
			for at := range lines {
				if h.Old != "" && standsAt(at, h.Old) {
					places++
				}
			}
			old, neu := strings.SplitAfter(h.Old, "\n"), strings.SplitAfter(h.New, "\n")
			old, neu = old[:len(old)-1], neu[:len(neu)-1] // the empty piece after the last newline
			prefix, suffix := 0, 0
			for prefix < len(old) && prefix < len(neu) && old[prefix] == neu[prefix] {
				prefix++
			}
			for suffix < len(old)-prefix && suffix < len(neu)-prefix && old[len(old)-1-suffix] == neu[len(neu)-1-suffix] {
				suffix++
			}
			// Rule 3: a window without some unchanged lines, at the hunk's
			// own line (for an end hunk, ending the text).
			trimmedHere := false
			for left := 0; left <= min(prefix, 3); left++ {
				for right := 0; right <= min(suffix, 3); right++ {
					window := strings.Join(old[left:len(old)-right], "")
					if window == "" || left == prefix && prefix > 0 || right == suffix && suffix > 0 {
						continue
					}
					if h.End && right == 0 && strings.HasSuffix(text, window) || !h.End && standsAt(h.Line-1+left, window) {
						trimmedHere = true
					}
				}
			}
			ending := strings.HasSuffix(text, h.Old)
			allowedByRule = allowedByRule && (h.End && (ending || trimmedHere) || !h.End && (standsAt(h.Line-1, h.Old) || places == 1 || places == 0 && trimmedHere))
		}
		if !allowedByRule {
			t.Fatalf("trial %d placed a change where it was not meant\nbase    %q\nmine    %q\ntheirs  %q\ngot     %q\nallowed %q\nhunks %+v", trial, strings.Join(base, ""), strings.Join(mine, ""), text, got, allowed, hunks)
		}
		byRule++
	}
	t.Logf("%d trials: %d where meant, %d refused, %d placed by the rules where the longest alignment does not put them", meant+refused+byRule, meant, refused, byRule)
	if meant == 0 || refused == 0 {
		t.Fatal("the trials did not exercise both outcomes")
	}
}

// The patch never changes the supplied buffers: the text is copied before it
// is patched.
func TestPatchLeavesItsInputs(t *testing.T) {
	from := []byte("a\nb\nc\n")
	hunks := DiffText(from, []byte("a\nB\nc\n"))
	before := bytes.Clone(from)
	if _, err := ApplyHunks(from, hunks); err != nil || !bytes.Equal(from, before) {
		t.Fatalf("input changed: %q, %v", from, err)
	}
}
