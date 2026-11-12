package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Line diffs and patches of source text. A line keeps its line ending, so a
// patch keeps LF, CRLF and a missing final newline exactly as they are. The
// diff gives each change up to three lines of context; a patch finds each
// hunk's lines in a text that may have changed since, nearest the hunk's own
// line, and refuses a hunk whose changed lines are no longer there. Studio's
// Undo, Redo and Revert, and the patchtext intent, use it.

// Hunk is one change of a text patch: its old lines, which start at its line,
// become its new lines.
type Hunk struct {
	Line int    `json:"line" cicada:"required"` // the line, counted from 1, where old starts in the text the patch was made from; for an empty old, the line that new goes before
	Old  string `json:"old" cicada:"required"`  // the whole lines the hunk replaces, with their line endings, context lines included; "" to insert
	New  string `json:"new" cicada:"required"`  // the whole lines that replace them, with their line endings; "" to delete

	// missing names a field that the JSON left out. A hunk must name all
	// three, so that a forgotten old is not read as an insertion.
	missing string
}

// UnmarshalJSON decodes a hunk, refusing unknown fields and recording a
// missing one for the intent to refuse.
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
		found := false
		for key := range fields {
			found = found || strings.EqualFold(key, name)
		}
		if !found {
			h.missing = name
			break
		}
	}
	return nil
}

// HunkError reports a hunk that does not apply: Hunk is its index (from 0) and
// Line the line it names.
type HunkError struct {
	Hunk, Line int
	Message    string
}

func (e *HunkError) Error() string {
	return fmt.Sprintf("hunk %d (line %d) %s", e.Hunk+1, e.Line, e.Message)
}

// DiffText returns the hunks that turn from into to, each with up to three
// lines of context, in the order of their lines. Equal texts have none.
func DiffText(from, to []byte) []Hunk {
	lines := diffHunks(diffOps(from, to))
	hunks := make([]Hunk, 0, len(lines))
	for _, hunk := range lines {
		hunks = append(hunks, Hunk{Line: hunk.oldStart + 1, Old: string(bytes.Join(hunk.oldLines, nil)), New: string(bytes.Join(hunk.newLines, nil))})
	}
	return hunks
}

// ApplyHunks applies hunks to text, which may have changed since the hunks
// were made: each hunk's old lines are found nearest the hunk's line, with
// context lines dropped when another change moved them. ApplyHunks(from,
// DiffText(from, to)) is to. A hunk whose old lines are not in text, match two
// places equally near, or overlap another hunk's is refused with a
// *HunkError. text is not changed.
func ApplyHunks(text []byte, hunks []Hunk) ([]byte, error) {
	if len(hunks) == 0 {
		return bytes.Clone(text), nil
	}
	lines := diffLines(text)
	count := len(lines)
	if count > 0 && len(lines[count-1]) == 0 {
		count-- // the empty piece after a final newline is not a line
	}
	parsed := make([]lineHunk, len(hunks))
	for i, hunk := range hunks {
		if hunk.Line < 1 {
			return nil, &HunkError{Hunk: i, Line: hunk.Line, Message: "names no line: lines count from 1"}
		}
		parsed[i] = lineHunk{oldStart: hunk.Line - 1, oldLines: hunkLines(hunk.Old), newLines: hunkLines(hunk.New)}
		if len(parsed[i].oldLines) == 0 && hunk.Line-1 > count {
			return nil, &HunkError{Hunk: i, Line: hunk.Line, Message: fmt.Sprintf("inserts after the end of the text, which has %d lines", count)}
		}
	}
	return patchLines(lines, parsed)
}

// RebaseText applies the change from → to to current, a text that may have
// changed since from: it is current with the lines that changed between from
// and to changed the same way. It returns to when current is from, and current
// when from is to.
func RebaseText(current, from, to []byte) ([]byte, error) {
	if bytes.Equal(current, from) {
		return bytes.Clone(to), nil
	}
	if bytes.Equal(from, to) {
		return bytes.Clone(current), nil
	}
	return patchLines(diffLines(current), diffHunks(diffOps(from, to)))
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

// patchLines finds every hunk in lines, then replaces their lines, in line
// order. Hunks that find the same insertion point keep their own order.
func patchLines(lines [][]byte, hunks []lineHunk) ([]byte, error) {
	type patch struct {
		hunk, start int
		from, to    [][]byte
	}
	patches := make([]patch, 0, len(hunks))
	for i, hunk := range hunks {
		start, fromLines, toLines, err := findFlexiblePatch(lines, hunk, hunk.oldStart)
		if err != nil {
			return nil, &HunkError{Hunk: i, Line: hunk.oldStart + 1, Message: err.Error()}
		}
		patches = append(patches, patch{hunk: i, start: start, from: fromLines, to: toLines})
	}
	sort.SliceStable(patches, func(i, j int) bool { return patches[i].start < patches[j].start })
	updated := make([][]byte, 0, len(lines))
	cursor, last := 0, -1
	for _, change := range patches {
		if change.start < cursor {
			return nil, &HunkError{Hunk: change.hunk, Line: hunks[change.hunk].oldStart + 1, Message: fmt.Sprintf("overlaps hunk %d: both change the same lines", last+1)}
		}
		end := change.start + len(change.from)
		if end > len(lines) {
			return nil, &HunkError{Hunk: change.hunk, Line: hunks[change.hunk].oldStart + 1, Message: "is out of range"}
		}
		updated = append(updated, lines[cursor:change.start]...)
		updated = append(updated, change.to...)
		cursor, last = end, change.hunk
	}
	updated = append(updated, lines[cursor:]...)
	return bytes.Join(updated, nil), nil
}

// findFlexiblePatch finds a hunk's old lines nearest expected. When they are
// not there it drops context lines, fewest first, so a hunk still applies
// after a change next to it; the lines it changes are never dropped.
func findFlexiblePatch(lines [][]byte, hunk lineHunk, expected int) (int, [][]byte, [][]byte, error) {
	commonPrefix := 0
	for commonPrefix < len(hunk.oldLines) && commonPrefix < len(hunk.newLines) && bytes.Equal(hunk.oldLines[commonPrefix], hunk.newLines[commonPrefix]) {
		commonPrefix++
	}
	commonSuffix := 0
	for commonSuffix < len(hunk.oldLines)-commonPrefix && commonSuffix < len(hunk.newLines)-commonPrefix && bytes.Equal(hunk.oldLines[len(hunk.oldLines)-1-commonSuffix], hunk.newLines[len(hunk.newLines)-1-commonSuffix]) {
		commonSuffix++
	}
	ambiguous := false
	for total := 0; total <= commonPrefix+commonSuffix; total++ {
		for left := 0; left <= commonPrefix && left <= total; left++ {
			right := total - left
			if right > commonSuffix {
				continue
			}
			end := len(hunk.oldLines) - right
			start, matches := findPatchStart(lines, hunk.oldLines[left:end], expected+left)
			if start >= 0 {
				return start, hunk.oldLines[left:end], hunk.newLines[left : len(hunk.newLines)-right], nil
			}
			ambiguous = ambiguous || matches > 1
		}
	}
	if ambiguous {
		return -1, nil, nil, fmt.Errorf("matches more than one place equally near its line")
	}
	return -1, nil, nil, fmt.Errorf("does not match: its old lines are not in the text")
}

// findPatchStart returns where wanted starts nearest expected, or -1 when it
// is nowhere or at two places equally near, with the number of matches.
func findPatchStart(lines, wanted [][]byte, expected int) (int, int) {
	bestStart, bestDistance, matches, tied := -1, int(^uint(0)>>1), 0, false
	limit := len(lines) - len(wanted)
	if limit < 0 {
		return -1, 0
	}
	for start := 0; start <= limit; start++ {
		matchesWanted := true
		for offset := range wanted {
			if !bytes.Equal(lines[start+offset], wanted[offset]) {
				matchesWanted = false
				break
			}
		}
		if !matchesWanted {
			continue
		}
		matches++
		distance := start - expected
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestStart, bestDistance, tied = start, distance, false
		} else if distance == bestDistance {
			tied = true
		}
	}
	if matches == 0 || tied {
		return -1, matches
	}
	return bestStart, matches
}
