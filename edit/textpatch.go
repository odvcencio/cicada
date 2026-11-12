package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Line diffs and patches of source text. A line keeps its line ending, so a
// patch keeps LF, CRLF and a missing final newline exactly as they are.
//
// A hunk names the line where its old lines start. On the text it was made
// from, they are there, and the hunk applies there. On a text that another
// writer has changed since, a hunk applies only where its place is certain,
// and is refused otherwise; it is never placed by guess. In order:
//  1. all its old lines at the line it names;
//  2. all its old lines at the one place in the whole text where they stand;
//     two or more such places are refused (hunk_ambiguous);
//  3. its old lines without up to three of the unchanged lines at each end
//     (fewest first), still at the line it names, keeping at least one
//     unchanged line on each side that has one. The lines it changes are
//     never dropped, and a smaller window is tried nowhere else: there it
//     could land on a copy of its lines that means something else.
//
// A hunk marked end applies only where its old lines end the text, and may
// miss unchanged lines only above the change. An insertion needs an unchanged
// neighbouring line in both old and new (hunk_needs_context). Studio's Undo,
// Redo and Revert and the patchtext intent use it.

// Limits of one patch. Each hunk costs at most one pass over the text, so the
// limits bound the work a client can ask for.
const (
	MaxHunks          = 1024    // hunks in one patch
	MaxPatchBytes     = 8 << 20 // bytes of all hunks' old and new together
	maxDroppedContext = 3       // unchanged lines a hunk may miss at each end, at its own line
	maxReportedPlaces = 10      // places a hunk_ambiguous refusal lists
)

// Hunk is one change of a text patch: its old lines, which start at its line,
// become its new lines.
type Hunk struct {
	Line int    `json:"line" cicada:"required"` // the line, counted from 1, where old starts in the text the patch was made from
	Old  string `json:"old" cicada:"required"`  // the whole lines the hunk replaces, with their line endings, including unchanged lines around the change; "" only in an empty text
	New  string `json:"new" cicada:"required"`  // the whole lines that replace them, with their line endings, starting and ending with the same unchanged lines as old; "" to delete
	End  bool   `json:"end,omitempty"`          // old reaches the end of the text: the hunk applies only where its old lines end the text

	// missing names a field that the JSON left out or set to null. A hunk
	// must name all three, so that a forgotten old is not read as empty.
	missing string
}

// UnmarshalJSON decodes a hunk, refusing unknown fields and recording a
// missing or null one for the intent to refuse.
func (h *Hunk) UnmarshalJSON(data []byte) error {
	type plain Hunk
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*h = Hunk(decoded)
	h.missing = ""
	for _, name := range []string{"line", "old", "new"} {
		present := false
		for key, raw := range fields {
			present = present || strings.EqualFold(key, name) && string(bytes.TrimSpace(raw)) != "null"
		}
		if !present {
			h.missing = name
			break
		}
	}
	return nil
}

// HunkError says why a hunk does not apply, in fields an agent can act on.
type HunkError struct {
	// Reason is hunk_mismatch, hunk_ambiguous, hunk_overlap,
	// hunk_needs_context, hunk_newline or hunk_limit.
	Reason string
	Hunk   int // the hunk, from 0; -1 when the patch as a whole is over a limit
	Line   int // the line the hunk names
	// At is the line, from 1, where the hunk's old lines first differ from
	// the text at Line; Expected is that old line and Found the text's line
	// ("" past the end). Set for hunk_mismatch.
	At              int
	Expected, Found string
	// Matches are the lines, from 1, where a hunk_ambiguous hunk matches (at
	// most ten), and Places how many places there are.
	Matches []int
	Places  int
	Other   int // the hunk that a hunk_overlap hunk overlaps, from 0
	// Limit and Value are the limit a hunk_limit patch is over and its value.
	Limit, Value int
	Message      string
}

func (e *HunkError) Error() string { return e.Message }

// HunkRefusal names the hunk of a patchtext refusal, so an agent can act on
// the refusal without reading its message.
type HunkRefusal struct {
	Index    int    `json:"index"`              // the hunk, from 0
	Line     int    `json:"line"`               // the line the hunk names
	At       int    `json:"at,omitempty"`       // hunk_mismatch: the line where old first differs from the text
	Expected string `json:"expected,omitempty"` // hunk_mismatch: that line of old
	Found    string `json:"found,omitempty"`    // hunk_mismatch: the text's line there; absent past the end
	Matches  []int  `json:"matches,omitempty"`  // hunk_ambiguous: lines where the hunk matches, at most ten
	Places   int    `json:"places,omitempty"`   // hunk_ambiguous: how many places it matches
	Other    *int   `json:"other,omitempty"`    // hunk_overlap: the hunk it overlaps, from 0
}

// DiffText returns the hunks that turn from into to, each with up to three
// lines of context, in the order of their lines. Equal texts have none.
func DiffText(from, to []byte) []Hunk {
	lines := diffHunks(diffOps(from, to))
	hunks := make([]Hunk, 0, len(lines))
	ends := len(diffLines(from))
	for _, hunk := range lines {
		hunks = append(hunks, Hunk{Line: hunk.oldStart + 1, Old: string(bytes.Join(hunk.oldLines, nil)), New: string(bytes.Join(hunk.newLines, nil)), End: hunk.oldStart+len(hunk.oldLines) == ends})
	}
	return hunks
}

// ApplyHunks applies hunks to text, which may have changed since the hunks
// were made, by the rules at the top of this file. ApplyHunks(from,
// DiffText(from, to)) is to. A hunk that cannot be placed with certainty,
// overlaps another hunk's lines, or would join two lines is refused with a
// *HunkError, and nothing is applied. text is not changed.
func ApplyHunks(text []byte, hunks []Hunk) ([]byte, error) {
	if len(hunks) == 0 {
		return bytes.Clone(text), nil
	}
	if len(hunks) > MaxHunks {
		return nil, &HunkError{Reason: "hunk_limit", Hunk: -1, Limit: MaxHunks, Value: len(hunks), Message: fmt.Sprintf("the patch holds %d hunks; a patch holds at most %d", len(hunks), MaxHunks)}
	}
	size := 0
	for _, hunk := range hunks {
		size += len(hunk.Old) + len(hunk.New)
	}
	if size > MaxPatchBytes {
		return nil, &HunkError{Reason: "hunk_limit", Hunk: -1, Limit: MaxPatchBytes, Value: size, Message: fmt.Sprintf("the hunks' old and new hold %d bytes; a patch holds at most %d", size, MaxPatchBytes)}
	}
	t := newLineText(text)
	type placement struct {
		hunk, start int
		from, to    [][]byte
	}
	placements := make([]placement, 0, len(hunks))
	for i, hunk := range hunks {
		start, from, to, err := t.locate(hunk)
		if err != nil {
			err.Hunk, err.Line = i, hunk.Line
			err.Message = fmt.Sprintf("hunk %d (line %d) %s", i+1, hunk.Line, err.Message)
			return nil, err
		}
		placements = append(placements, placement{hunk: i, start: start, from: from, to: to})
	}
	sort.SliceStable(placements, func(i, j int) bool { return placements[i].start < placements[j].start })
	updated := make([][]byte, 0, len(t.lines))
	cursor, last := 0, -1
	for _, p := range placements {
		line := hunks[p.hunk].Line
		if p.start < cursor {
			return nil, &HunkError{Reason: "hunk_overlap", Hunk: p.hunk, Line: line, Other: last, Message: fmt.Sprintf("hunk %d (line %d) changes lines that hunk %d also changes", p.hunk+1, line, last+1)}
		}
		end := p.start + len(p.from)
		if n := len(p.to); n > 0 && !bytes.HasSuffix(p.to[n-1], []byte("\n")) && end < t.count {
			return nil, &HunkError{Reason: "hunk_newline", Hunk: p.hunk, Line: line, Message: fmt.Sprintf("hunk %d (line %d) ends its new lines without a newline before line %d, which would join two lines; end new with a newline, or let old reach the end of the text", p.hunk+1, line, end+1)}
		}
		updated = append(updated, t.lines[cursor:p.start]...)
		updated = append(updated, p.to...)
		cursor, last = end, p.hunk
	}
	updated = append(updated, t.lines[cursor:]...)
	return bytes.Join(updated, nil), nil
}

// RebaseText applies the change from → to to current, a text that may have
// changed since from, with ApplyHunks: it is current with the lines that
// changed between from and to changed the same way. It returns to when current
// is from, and current when from is to.
func RebaseText(current, from, to []byte) ([]byte, error) {
	if bytes.Equal(current, from) {
		return bytes.Clone(to), nil
	}
	if bytes.Equal(from, to) {
		return bytes.Clone(current), nil
	}
	return ApplyHunks(current, DiffText(from, to))
}

// UnifiedDiff writes the change from → to as a unified diff of the file name.
func UnifiedDiff(name string, from, to []byte) string {
	ops := diffOps(from, to)
	hunks := diffHunks(ops)
	if len(hunks) == 0 {
		return ""
	}
	var result bytes.Buffer
	result.WriteString("--- a/" + name + "\n+++ b/" + name + "\n")
	for _, hunk := range hunks {
		oldCount, newCount := len(hunk.oldLines), len(hunk.newLines)
		oldStart, newStart := hunk.oldStart+1, 1
		if oldCount == 0 {
			oldStart--
		}
		newBefore := 0
		for _, prior := range ops[:hunk.start] {
			if prior.kind != '-' {
				newBefore++
			}
		}
		newStart = newBefore + 1
		if newCount == 0 {
			newStart--
		}
		fmt.Fprintf(&result, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, op := range ops[hunk.start:hunk.end] {
			result.WriteByte(op.kind)
			result.Write(op.line)
			if len(op.line) == 0 || op.line[len(op.line)-1] != '\n' {
				result.WriteString("\n\\ No newline at end of file\n")
			}
		}
	}
	return result.String()
}

// diffCellLimit bounds the line table of a diff; larger texts get one hunk
// from their first to their last changed line.
const diffCellLimit = 2_000_000

type diffOp struct {
	kind byte // ' ' kept, '-' removed, '+' added
	line []byte
}

// lineHunk is a hunk over ops[start:end]; its old lines start at oldStart.
type lineHunk struct {
	start, end int
	oldStart   int
	oldLines   [][]byte
	newLines   [][]byte
}

// diffLines splits a text after each newline. A text that ends in a newline
// has an empty last piece, which a diff keeps so that the end of the text
// anchors a hunk.
func diffLines(source []byte) [][]byte {
	if len(source) == 0 {
		return nil
	}
	return bytes.SplitAfter(source, []byte("\n"))
}

// hunkLines splits a hunk's text into lines, without the empty piece after a
// final newline.
func hunkLines(text string) [][]byte {
	lines := diffLines([]byte(text))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	return lines
}

func diffOps(before, after []byte) []diffOp {
	oldLines, newLines := diffLines(before), diffLines(after)
	n, m := len(oldLines), len(newLines)
	if n == 0 {
		ops := make([]diffOp, 0, m)
		for _, line := range newLines {
			ops = append(ops, diffOp{kind: '+', line: line})
		}
		return ops
	}
	if m == 0 {
		ops := make([]diffOp, 0, n)
		for _, line := range oldLines {
			ops = append(ops, diffOp{kind: '-', line: line})
		}
		return ops
	}
	if int64(n+1)*int64(m+1) > diffCellLimit {
		return diffSingleHunkOps(oldLines, newLines)
	}
	width := m + 1
	lengths := make([]uint32, (n+1)*width)
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			at := i*width + j
			if bytes.Equal(oldLines[i], newLines[j]) {
				lengths[at] = lengths[(i+1)*width+j+1] + 1
			} else {
				down, right := lengths[(i+1)*width+j], lengths[i*width+j+1]
				if down >= right {
					lengths[at] = down
				} else {
					lengths[at] = right
				}
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	for i, j := 0, 0; i < n || j < m; {
		if i < n && j < m && bytes.Equal(oldLines[i], newLines[j]) {
			ops = append(ops, diffOp{kind: ' ', line: oldLines[i]})
			i++
			j++
			continue
		}
		if j < m && (i == n || lengths[i*width+j+1] > lengths[(i+1)*width+j]) {
			ops = append(ops, diffOp{kind: '+', line: newLines[j]})
			j++
			continue
		}
		ops = append(ops, diffOp{kind: '-', line: oldLines[i]})
		i++
	}
	return ops
}

func diffSingleHunkOps(oldLines, newLines [][]byte) []diffOp {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && bytes.Equal(oldLines[prefix], newLines[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && bytes.Equal(oldLines[len(oldLines)-1-suffix], newLines[len(newLines)-1-suffix]) {
		suffix++
	}
	ops := make([]diffOp, 0, len(oldLines)+len(newLines))
	for i := 0; i < prefix; i++ {
		ops = append(ops, diffOp{kind: ' ', line: oldLines[i]})
	}
	for i := prefix; i < len(oldLines)-suffix; i++ {
		ops = append(ops, diffOp{kind: '-', line: oldLines[i]})
	}
	for i := prefix; i < len(newLines)-suffix; i++ {
		ops = append(ops, diffOp{kind: '+', line: newLines[i]})
	}
	for i := suffix; i > 0; i-- {
		ops = append(ops, diffOp{kind: ' ', line: oldLines[len(oldLines)-i]})
	}
	return ops
}

func diffHunks(ops []diffOp) []lineHunk {
	var changed []int
	for i, op := range ops {
		if op.kind != ' ' {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	var spans [][2]int
	start := max(0, changed[0]-3)
	lastChange := changed[0]
	for _, index := range changed[1:] {
		if index-lastChange <= 6 {
			lastChange = index
			continue
		}
		spans = append(spans, [2]int{start, min(len(ops), lastChange+4)})
		start = max(0, index-3)
		lastChange = index
	}
	spans = append(spans, [2]int{start, min(len(ops), lastChange+4)})

	hunks := make([]lineHunk, 0, len(spans))
	oldLine := 0
	spanIndex := 0
	for index, op := range ops {
		if spanIndex < len(spans) && index == spans[spanIndex][0] {
			hunk := lineHunk{start: spans[spanIndex][0], end: spans[spanIndex][1], oldStart: oldLine}
			for at := hunk.start; at < hunk.end; at++ {
				switch ops[at].kind {
				case ' ':
					hunk.oldLines = append(hunk.oldLines, ops[at].line)
					hunk.newLines = append(hunk.newLines, ops[at].line)
				case '-':
					hunk.oldLines = append(hunk.oldLines, ops[at].line)
				case '+':
					hunk.newLines = append(hunk.newLines, ops[at].line)
				}
			}
			hunks = append(hunks, hunk)
			spanIndex++
		}
		if op.kind != '+' {
			oldLine++
		}
	}
	return hunks
}

// lineText is a text split into lines, with a hash of each line so a
// window of lines is found in one pass.
type lineText struct {
	lines  [][]byte
	hashes []uint64
	count  int // lines, not counting the empty piece after a final newline
}

func newLineText(text []byte) *lineText {
	lines := diffLines(text)
	count := len(lines)
	if count > 0 && len(lines[count-1]) == 0 {
		count--
	}
	return &lineText{lines: lines, hashes: hashLines(lines), count: count}
}

func hashLines(lines [][]byte) []uint64 {
	hashes := make([]uint64, len(lines))
	for i, line := range lines {
		h := uint64(14695981039346656037) // FNV-1a
		for _, b := range line {
			h = (h ^ uint64(b)) * 1099511628211
		}
		hashes[i] = h
	}
	return hashes
}

// equalAt reports whether want sits at line index at.
func (t *lineText) equalAt(at int, want [][]byte, hashes []uint64) bool {
	if at < 0 || at+len(want) > len(t.lines) {
		return false
	}
	for k := range want {
		if t.hashes[at+k] != hashes[k] || !bytes.Equal(t.lines[at+k], want[k]) {
			return false
		}
	}
	return true
}

// find returns every line index where want starts: one pass with a rolling
// hash, each hit confirmed byte for byte. An empty want is at every index.
func (t *lineText) find(want [][]byte, hashes []uint64) []int {
	var at []int
	if len(want) == 0 {
		for q := 0; q <= len(t.lines); q++ {
			at = append(at, q)
		}
		return at
	}
	m := len(want)
	if m > len(t.lines) {
		return nil
	}
	const base = 1099511628211
	var target, rolling, high uint64 = 0, 0, 1
	for k := 0; k < m; k++ {
		target = target*base + hashes[k]
		rolling = rolling*base + t.hashes[k]
		if k > 0 {
			high *= base
		}
	}
	for q := 0; ; q++ {
		if rolling == target && t.equalAt(q, want, hashes) {
			at = append(at, q)
		}
		if q+m >= len(t.lines) {
			return at
		}
		rolling = (rolling-t.hashes[q]*high)*base + t.hashes[q+m]
	}
}

// locate finds where a hunk applies: the line index its kept old lines start
// at, those lines, and the new lines that replace them.
func (t *lineText) locate(hunk Hunk) (int, [][]byte, [][]byte, *HunkError) {
	if hunk.Line < 1 {
		return 0, nil, nil, &HunkError{Reason: "hunk_mismatch", Message: "names no line: lines count from 1"}
	}
	old, neu := hunkLines(hunk.Old), hunkLines(hunk.New)
	expected := hunk.Line - 1
	if len(old) == 0 {
		if t.count == 0 && expected == 0 {
			return 0, nil, neu, nil
		}
		return 0, nil, nil, &HunkError{Reason: "hunk_needs_context", Message: "inserts with no old lines; give at least one unchanged line next to the insertion in both old and new"}
	}
	oldHashes := hashLines(old)
	prefix := 0
	for prefix < len(old) && prefix < len(neu) && bytes.Equal(old[prefix], neu[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(neu)-prefix && bytes.Equal(old[len(old)-1-suffix], neu[len(neu)-1-suffix]) {
		suffix++
	}
	// trimmed tries the windows that drop unchanged lines, fewest first, at
	// one place: at(left) is where the window that drops left lines from the
	// start must begin.
	trimmed := func(maxRight int, at func(left int) int) (int, [][]byte, [][]byte, bool) {
		maxLeft := min(prefix, maxDroppedContext)
		for dropped := 1; dropped <= maxLeft+maxRight; dropped++ {
			for left := 0; left <= min(maxLeft, dropped); left++ {
				right := dropped - left
				// A window keeps at least one unchanged line on each side that
				// has one, so it cannot shrink to the changed lines alone.
				if right > maxRight || left == prefix && prefix > 0 || right == suffix && suffix > 0 || len(old)-left-right == 0 {
					continue
				}
				window := old[left : len(old)-right]
				if t.equalAt(at(left), window, oldHashes[left:len(old)-right]) {
					return at(left), window, neu[left : len(neu)-right], true
				}
			}
		}
		return 0, nil, nil, false
	}
	if hunk.End {
		// The end pins the hunk, wherever lines above it moved; unchanged
		// lines may be missing only above the change.
		if at := t.count - len(old); t.equalAt(at, old, oldHashes) {
			return at, old, neu, nil
		}
		if start, from, to, ok := trimmed(0, func(left int) int { return t.count - (len(old) - left) }); ok {
			return start, from, to, nil
		}
		expected = max(t.count-len(old), 0)
	} else {
		if t.equalAt(expected, old, oldHashes) {
			return expected, old, neu, nil
		}
		switch places := t.find(old, oldHashes); len(places) {
		case 0:
			// Not elsewhere either: unchanged lines around the change may
			// have changed since, so they may be missing, but only here.
			if start, from, to, ok := trimmed(min(suffix, maxDroppedContext), func(left int) int { return expected + left }); ok {
				return start, from, to, nil
			}
		case 1:
			return places[0], old, neu, nil
		default:
			lines := make([]int, 0, maxReportedPlaces)
			for _, q := range places[:min(len(places), maxReportedPlaces)] {
				lines = append(lines, q+1)
			}
			return 0, nil, nil, &HunkError{Reason: "hunk_ambiguous", Matches: lines, Places: len(places), Message: fmt.Sprintf("is not at its line, and its old lines stand at %d places (lines %s); add unchanged lines around the change so they stand at one", len(places), joinInts(lines))}
		}
	}
	err := &HunkError{Reason: "hunk_mismatch"}
	for k := range old {
		if expected+k >= t.count || !bytes.Equal(t.lines[expected+k], old[k]) {
			err.At, err.Expected = expected+k+1, string(old[k])
			if expected+k < t.count {
				err.Found = string(t.lines[expected+k])
			}
			break
		}
	}
	where := "anywhere in the text"
	if hunk.End {
		where = "at the end of the text"
	}
	err.Message = fmt.Sprintf("does not match: line %d is %q, where the hunk expects %q, and its old lines do not stand together %s", err.At, strings.TrimRight(err.Found, "\r\n"), strings.TrimRight(err.Expected, "\r\n"), where)
	return 0, nil, nil, err
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ", ")
}
