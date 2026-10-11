package edit

import (
	"bytes"
	"fmt"
	"path"
)

// Proposal groups attributed intents under one label for acceptance and undo.
type Proposal struct {
	ID       string   `json:"id,omitempty"`
	Label    string   `json:"label,omitempty"`
	Envelope Envelope `json:"envelope"`
}

// Staged is a validated proposal candidate. Hosts own its lifetime and commit.
type Staged struct {
	Result *Result
	Ops    []PreviewOp
	Diff   string
}

// Stage applies a proposal in memory and resolves previews against its shadow
// plan. Neither the proposal nor the supplied source buffers are changed.
func Stage(source []byte, proposal Proposal, opts Options) (*Staged, error) {
	result, err := Apply(source, proposal.Envelope, opts)
	if err != nil {
		return nil, err
	}
	ops, err := PreviewOps(result.Plan, proposal.Envelope.Intents)
	if err != nil {
		return nil, err
	}
	if proposal.Label != "" {
		result.Label = proposal.Label
	}
	name := path.Base(opts.Path)
	if opts.Path == "" {
		name = "score.cicada"
	}
	diff := proposalDiff(source, result.Source, name)
	for _, file := range result.Files {
		diff += proposalDiff(file.Before, file.After, path.Base(file.Path))
	}
	return &Staged{Result: result, Ops: ops, Diff: diff}, nil
}

// proposalDiff emits one unified hunk around the changed lines, with three
// context lines on either side. Display names never contain host directories.
func proposalDiff(before, after []byte, name string) string {
	if bytes.Equal(before, after) {
		return ""
	}
	lines := func(source []byte) [][]byte {
		out := bytes.SplitAfter(source, []byte("\n"))
		if len(out[len(out)-1]) == 0 {
			out = out[:len(out)-1]
		}
		return out
	}
	old, next := lines(before), lines(after)
	start := 0
	for start < len(old) && start < len(next) && bytes.Equal(old[start], next[start]) {
		start++
	}
	end := 0
	for end < len(old)-start && end < len(next)-start && bytes.Equal(old[len(old)-end-1], next[len(next)-end-1]) {
		end++
	}
	contextStart := max(0, start-3)
	contextEnd := max(0, end-3)
	oldCount, newCount := len(old)-contextStart-contextEnd, len(next)-contextStart-contextEnd
	oldStart, newStart := contextStart+1, contextStart+1
	if oldCount == 0 {
		oldStart--
	}
	if newCount == 0 {
		newStart--
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n", name, name, oldStart, oldCount, newStart, newCount)
	write := func(prefix byte, line []byte) {
		out.WriteByte(prefix)
		out.Write(line)
		if line[len(line)-1] != '\n' {
			out.WriteString("\n\\ No newline at end of file\n")
		}
	}
	for _, line := range old[contextStart:start] {
		write(' ', line)
	}
	for _, line := range old[start : len(old)-end] {
		write('-', line)
	}
	for _, line := range next[start : len(next)-end] {
		write('+', line)
	}
	for _, line := range old[len(old)-end : len(old)-contextEnd] {
		write(' ', line)
	}
	return out.String()
}
