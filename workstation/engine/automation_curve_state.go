package main

// curveEditorPoint is one automation point. OriginalAt names the point the
// server rendered, so the editor can tell an edited point from a new one.
type curveEditorPoint struct {
	Tick       int64
	Value      float64
	Shape      string
	Curve      float64
	OriginalAt string
}

// curvePointsDiffer reports whether the editor's points differ from the saved
// ones in position, value, shape or curve. It ignores OriginalAt, which only
// the editor sets.
func curvePointsDiffer(edited, saved []curveEditorPoint) bool {
	if len(edited) != len(saved) {
		return true
	}
	for i := range edited {
		a, b := edited[i], saved[i]
		if a.Tick != b.Tick || a.Value != b.Value || a.Shape != b.Shape || a.Curve != b.Curve {
			return true
		}
	}
	return false
}
