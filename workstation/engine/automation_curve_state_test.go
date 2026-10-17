package main

import "testing"

func TestCurvePointsDifferOnEveryKindOfEdit(t *testing.T) {
	saved := []curveEditorPoint{{Tick: 0, Value: .2, Shape: "linear"}, {Tick: 960, Value: .8, Shape: "curve", Curve: .3}}
	clone := func() []curveEditorPoint {
		out := append([]curveEditorPoint(nil), saved...)
		for i := range out {
			out[i].OriginalAt = "1.1.1" // set by the editor, not an edit
		}
		return out
	}
	if curvePointsDiffer(clone(), saved) {
		t.Fatal("an untouched editor is not dirty")
	}
	edits := map[string]func([]curveEditorPoint) []curveEditorPoint{
		"new point": func(p []curveEditorPoint) []curveEditorPoint {
			return append(p, curveEditorPoint{Tick: 1920, Value: .5})
		},
		"drag":         func(p []curveEditorPoint) []curveEditorPoint { p[1].Tick += 240; return p },
		"arrow value":  func(p []curveEditorPoint) []curveEditorPoint { p[0].Value += .005; return p },
		"shape":        func(p []curveEditorPoint) []curveEditorPoint { p[0].Shape = "step"; return p },
		"curve amount": func(p []curveEditorPoint) []curveEditorPoint { p[1].Curve = .4; return p },
		"removed":      func(p []curveEditorPoint) []curveEditorPoint { return p[:1] },
	}
	for name, edit := range edits {
		if !curvePointsDiffer(edit(clone()), saved) {
			t.Errorf("%s must mark the draft unsaved", name)
		}
	}
	// A drag that is cancelled or dragged back leaves nothing unsaved.
	moved := clone()
	moved[1].Tick += 240
	moved[1].Tick -= 240
	if curvePointsDiffer(moved, saved) {
		t.Error("restoring the point must clear the draft")
	}
}
