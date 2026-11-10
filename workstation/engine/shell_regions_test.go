package main

import "testing"

// Rule R1: a region's revision fields change only when the server rendered it
// at the new revision. New HTML patches it; an unchanged digest advances it;
// everything else is stale and keeps its fields.
func TestRuleR1(t *testing.T) {
	for _, c := range []struct {
		name                             string
		id                               string
		held, rendered, unchanged, write bool
		want                             int
	}{
		{"new HTML after a write", "dock-clip", false, true, false, true, regionPatch},
		{"new HTML from a read", "center-session", false, true, false, false, regionPatch},
		{"digest unchanged after a write", "actions", false, false, true, true, regionAdvance},
		{"digest unchanged from a read", "center-mixer", false, false, true, false, regionAdvance},
		{"not rendered by a write", "center-session", false, false, false, true, regionStale},
		{"not rendered by a read", "dialog-history", false, false, false, false, regionStale},
		{"revision-only region after a write", "transport", false, false, false, true, regionAdvance},
		{"revision-only region after a read", "transport", false, false, false, false, regionStale},
		{"held Code lens with new HTML", "code", true, true, false, true, regionStale},
		{"held Code lens reported unchanged", "code", true, false, true, true, regionStale},
	} {
		if got := ruleR1(c.id, c.held, c.rendered, c.unchanged, c.write); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestShellPatchOrderNamesEveryRegionOnce(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range shellPatchOrder {
		if seen[id] {
			t.Errorf("%s appears twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 20 || shellPatchOrder[0] != "dock-clip" {
		t.Errorf("patch order %v", shellPatchOrder)
	}
}
