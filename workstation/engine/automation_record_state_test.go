package main

import "testing"

func TestRecordingSeedUsesNewPassOnlyAndPreservesUserMovement(t *testing.T) {
	points := []curveEditorPoint{{Value: .8}, {Value: .45}}
	if value, ok := recordingSeed("", "take1", "guitar.amp_drive", "guitar.amp_drive", true, false, points); !ok || value != .8 {
		t.Fatalf("new pass seed %v %v", value, ok)
	}
	for _, tc := range []struct {
		previous, id, path string
		active, pending    bool
	}{
		{"take1", "take1", "guitar.amp_drive", true, false},
		{"", "take1", "other.amp_drive", true, false},
		{"", "take1", "guitar.amp_drive", false, false},
		{"", "take1", "guitar.amp_drive", true, true},
	} {
		if _, ok := recordingSeed(tc.previous, tc.id, tc.path, "guitar.amp_drive", tc.active, tc.pending, points); ok {
			t.Fatalf("unexpected slider update: %+v", tc)
		}
	}
}
