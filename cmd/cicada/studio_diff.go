package main

import (
	"bytes"
	"fmt"
)

const studioDiffCellLimit = 2_000_000

type studioDiffOp struct {
	kind byte
	line []byte
}

type studioDiffHunk struct {
	start, end int
	oldStart   int
	oldLines   [][]byte
	newLines   [][]byte
}

func studioDiffLines(source []byte) [][]byte {
	if len(source) == 0 {
		return nil
	}
	return bytes.SplitAfter(source, []byte("\n"))
}

func studioDiffOps(before, after []byte) []studioDiffOp {
	oldLines, newLines := studioDiffLines(before), studioDiffLines(after)
	n, m := len(oldLines), len(newLines)
	if n == 0 {
		ops := make([]studioDiffOp, 0, m)
		for _, line := range newLines {
			ops = append(ops, studioDiffOp{kind: '+', line: line})
		}
		return ops
	}
	if m == 0 {
		ops := make([]studioDiffOp, 0, n)
		for _, line := range oldLines {
			ops = append(ops, studioDiffOp{kind: '-', line: line})
		}
		return ops
	}
	if int64(n+1)*int64(m+1) > studioDiffCellLimit {
		return studioDiffSingleHunkOps(oldLines, newLines)
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
	ops := make([]studioDiffOp, 0, n+m)
	for i, j := 0, 0; i < n || j < m; {
		if i < n && j < m && bytes.Equal(oldLines[i], newLines[j]) {
			ops = append(ops, studioDiffOp{kind: ' ', line: oldLines[i]})
			i++
			j++
			continue
		}
		if j < m && (i == n || lengths[i*width+j+1] > lengths[(i+1)*width+j]) {
			ops = append(ops, studioDiffOp{kind: '+', line: newLines[j]})
			j++
			continue
		}
		ops = append(ops, studioDiffOp{kind: '-', line: oldLines[i]})
		i++
	}
	return ops
}

func studioDiffSingleHunkOps(oldLines, newLines [][]byte) []studioDiffOp {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && bytes.Equal(oldLines[prefix], newLines[prefix]) {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && bytes.Equal(oldLines[len(oldLines)-1-suffix], newLines[len(newLines)-1-suffix]) {
		suffix++
	}
	ops := make([]studioDiffOp, 0, len(oldLines)+len(newLines))
	for i := 0; i < prefix; i++ {
		ops = append(ops, studioDiffOp{kind: ' ', line: oldLines[i]})
	}
	for i := prefix; i < len(oldLines)-suffix; i++ {
		ops = append(ops, studioDiffOp{kind: '-', line: oldLines[i]})
	}
	for i := prefix; i < len(newLines)-suffix; i++ {
		ops = append(ops, studioDiffOp{kind: '+', line: newLines[i]})
	}
	for i := suffix; i > 0; i-- {
		ops = append(ops, studioDiffOp{kind: ' ', line: oldLines[len(oldLines)-i]})
	}
	return ops
}

func studioDiffHunks(ops []studioDiffOp) []studioDiffHunk {
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

	hunks := make([]studioDiffHunk, 0, len(spans))
	oldLine := 0
	spanIndex := 0
	for index, op := range ops {
		if spanIndex < len(spans) && index == spans[spanIndex][0] {
			hunk := studioDiffHunk{start: spans[spanIndex][0], end: spans[spanIndex][1], oldStart: oldLine}
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

func studioUnifiedDiff(before, after []byte) string {
	ops := studioDiffOps(before, after)
	hunks := studioDiffHunks(ops)
	if len(hunks) == 0 {
		return ""
	}
	var result bytes.Buffer
	result.WriteString("--- a/score.cicada\n+++ b/score.cicada\n")
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

func studioApplySourceDiff(current, from, to []byte) ([]byte, error) {
	if bytes.Equal(current, from) {
		return bytes.Clone(to), nil
	}
	if bytes.Equal(from, to) {
		return bytes.Clone(current), nil
	}
	ops := studioDiffOps(from, to)
	hunks := studioDiffHunks(ops)
	currentLines := studioDiffLines(current)
	type patch struct {
		start int
		from  [][]byte
		to    [][]byte
	}
	patches := make([]patch, 0, len(hunks))
	for _, hunk := range hunks {
		start, fromLines, toLines, err := studioFindFlexiblePatch(currentLines, hunk, hunk.oldStart)
		if err != nil {
			return nil, err
		}
		patches = append(patches, patch{start: start, from: fromLines, to: toLines})
	}
	for i := 0; i < len(patches); i++ {
		for j := i + 1; j < len(patches); j++ {
			if patches[j].start > patches[i].start {
				patches[i], patches[j] = patches[j], patches[i]
			}
		}
	}
	for _, change := range patches {
		end := change.start + len(change.from)
		if change.start < 0 || end > len(currentLines) {
			return nil, fmt.Errorf("source patch is out of range")
		}
		updated := make([][]byte, 0, len(currentLines)-len(change.from)+len(change.to))
		updated = append(updated, currentLines[:change.start]...)
		updated = append(updated, change.to...)
		updated = append(updated, currentLines[end:]...)
		currentLines = updated
	}
	return bytes.Join(currentLines, nil), nil
}

func studioFindFlexiblePatch(lines [][]byte, hunk studioDiffHunk, expected int) (int, [][]byte, [][]byte, error) {
	commonPrefix := 0
	for commonPrefix < len(hunk.oldLines) && commonPrefix < len(hunk.newLines) && bytes.Equal(hunk.oldLines[commonPrefix], hunk.newLines[commonPrefix]) {
		commonPrefix++
	}
	commonSuffix := 0
	for commonSuffix < len(hunk.oldLines)-commonPrefix && commonSuffix < len(hunk.newLines)-commonPrefix && bytes.Equal(hunk.oldLines[len(hunk.oldLines)-1-commonSuffix], hunk.newLines[len(hunk.newLines)-1-commonSuffix]) {
		commonSuffix++
	}
	for total := 0; total <= commonPrefix+commonSuffix; total++ {
		for left := 0; left <= commonPrefix && left <= total; left++ {
			right := total - left
			if right > commonSuffix {
				continue
			}
			end := len(hunk.oldLines) - right
			start, err := studioFindPatchStart(lines, hunk.oldLines[left:end], expected+left)
			if err == nil {
				return start, hunk.oldLines[left:end], hunk.newLines[left : len(hunk.newLines)-right], nil
			}
		}
	}
	return -1, nil, nil, fmt.Errorf("source patch does not match uniquely")
}

func studioFindPatchStart(lines, wanted [][]byte, expected int) (int, error) {
	bestStart, bestDistance, matches, tied := -1, int(^uint(0)>>1), 0, false
	limit := len(lines) - len(wanted)
	if limit < 0 {
		return -1, fmt.Errorf("source patch does not match")
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
		distance := studioIntAbs(start - expected)
		if distance < bestDistance {
			bestStart, bestDistance, tied = start, distance, false
		} else if distance == bestDistance {
			tied = true
		}
	}
	if matches == 0 || tied {
		return -1, fmt.Errorf("source patch has no unique matching context")
	}
	return bestStart, nil
}

func studioIntAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
