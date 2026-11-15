package main

import edits "m31labs.dev/cicada/edit"

// studioUnifiedDiff is the unified diff Studio's history and apply's dry run
// show, of a file named score.cicada until the caller renames it.
func studioUnifiedDiff(before, after []byte) string {
	return edits.UnifiedDiff("score.cicada", before, after)
}

// studioApplySourceDiff applies the change from → to to current, the score on
// disk, which may have changed since: Undo, Redo and Revert.
func studioApplySourceDiff(current, from, to []byte) ([]byte, error) {
	return edits.RebaseText(current, from, to)
}
