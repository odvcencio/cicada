package main

// The /studio shell's update regions: rule R1, which decides what a response
// does to each region, and the patch order. They are free of syscall/js so
// host tests cover them.

// regionUpdate is a region whose content changed: its new body and digest.
type regionUpdate struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
	HTML   string `json:"html"`
}

const (
	regionStale = iota
	regionPatch
	regionAdvance
)

// ruleR1 says what a response does to one region. A region's revision fields
// change only when the server rendered that region at the new revision: new
// HTML arrived (patch), or the server reported its digest unchanged
// (advance). The transport form does not follow the score, so a confirmed
// write advances it unrendered. A held region (the Code lens with unsaved
// text) is never patched or advanced. Anything else keeps its revision and is
// stale: the service refuses a write from it until it is read again.
func ruleR1(id string, held, rendered, unchanged, write bool) int {
	switch {
	case held:
		return regionStale
	case rendered:
		return regionPatch
	case unchanged || write && id == "transport":
		return regionAdvance
	}
	return regionStale
}

// shellPatchOrder is the order in which a response's regions are patched after
// the one the user acted in: the editors first, the Code lens and the hidden
// browser sections and dialogs last.
var shellPatchOrder = []string{
	"dock-clip", "center-arrange", "center-session", "center-mixer", "dock-device", "dock-automation",
	"dock-header", "status", "actions", "transport", "tempo", "project",
	"browser-instruments", "browser-generate", "browser-takes", "browser-sampler",
	"code", "dialog-history", "dialog-audio", "dialog-export",
}
