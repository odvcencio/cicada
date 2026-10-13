package main

import (
	"strings"
)

type patternParityCase struct {
	name, source string
	body         studioEdit
}

func patternParityCases() []patternParityCase {
	cases := []patternParityCase{
		{"settings", studioScore, studioEdit{Action: "settings", Pattern: "pulse", Settings: &studioPatternSettings{Swing100: 5500, Gate: 60}}},
		{"step", studioScore, studioEdit{Action: "step", Pattern: "pulse", Step: 1, NoteEdit: &studioStepEdit{Mode: "note", Pitch: 62, Accent: true, Ratchet: 1, Chance: 100}}},
		{"duplicate", studioScore, studioEdit{Action: "duplicate", Pattern: "pulse", NewName: "pulse2"}},
		{"reverse", studioScore, studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: "reverse", First: 0, Last: 3}}},
		{"resize", studioScore, studioEdit{Action: "resize", Pattern: "pulse", Length: 8}},
		{"bind", strings.Replace(studioScore, "scene main", "pattern pulse2 acid { 1 . 5 . }\nscene main", 1), studioEdit{Action: "bind", Pattern: "pulse2", Scene: "main", Track: "bass"}},
		{"settings missing", studioScore, studioEdit{Action: "settings", Pattern: "pulse"}},
		{"drum transpose", studioScore, studioEdit{Action: "settings", Pattern: "beat", Settings: &studioPatternSettings{Swing100: 5500, Gate: 60, Transpose: 1}}},
		{"step missing", studioScore, studioEdit{Action: "step", Pattern: "pulse"}},
		{"step outside", studioScore, studioEdit{Action: "step", Pattern: "pulse", Step: 9, NoteEdit: &studioStepEdit{Mode: "rest", Ratchet: 1, Chance: 100}}},
		{"step mode", studioScore, studioEdit{Action: "step", Pattern: "pulse", NoteEdit: &studioStepEdit{Mode: "bad", Ratchet: 1, Chance: 100}}},
		{"step hit", studioScore, studioEdit{Action: "step", Pattern: "pulse", NoteEdit: &studioStepEdit{Mode: "hit", Ratchet: 1, Chance: 100}}},
		{"drum tie", studioScore, studioEdit{Action: "step", Pattern: "beat", Lane: "bd", NoteEdit: &studioStepEdit{Mode: "tie", Ratchet: 1, Chance: 100}}},
		{"drum velocity", studioScore, studioEdit{Action: "step", Pattern: "beat", Lane: "bd", NoteEdit: &studioStepEdit{Mode: "hit", Velocity: "4", Ratchet: 3, Chance: 50}}},
		{"bad velocity", studioScore, studioEdit{Action: "step", Pattern: "beat", Lane: "bd", NoteEdit: &studioStepEdit{Mode: "hit", Velocity: "bad", Ratchet: 1, Chance: 100}}},
		{"bad name", studioScore, studioEdit{Action: "duplicate", Pattern: "pulse", NewName: "Invalid"}},
		{"name exists", studioScore, studioEdit{Action: "duplicate", Pattern: "pulse", NewName: "beat"}},
		{"bad length", studioScore, studioEdit{Action: "resize", Pattern: "pulse", Length: 65}},
		{"resize unchanged", studioScore, studioEdit{Action: "resize", Pattern: "pulse", Length: 4}},
		{"range missing", studioScore, studioEdit{Action: "range", Pattern: "pulse"}},
		{"range outside", studioScore, studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: "clear", Last: 8}}},
		{"range copy outside", studioScore, studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: "copy", Last: 3, Target: 2}}},
		{"range mode", studioScore, studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: "bad", Last: 3}}},
		{"drum range transpose", studioScore, studioEdit{Action: "range", Pattern: "beat", Lane: "bd", Range: &studioPatternRange{Operation: "transpose", Last: 3, Amount: 1}}},
		{"missing pattern", studioScore, studioEdit{Action: "resize", Pattern: "ghost", Length: 8}},
		{"missing scene", studioScore, studioEdit{Action: "bind", Pattern: "pulse", Scene: "ghost", Track: "bass"}},
		{"bind off", studioScore, studioEdit{Action: "bind", Pattern: "off", Scene: "main", Track: "bass"}},
		{"bind keep", studioScore, studioEdit{Action: "bind", Pattern: "keep", Scene: "main", Track: "bass"}},
	}
	for _, op := range []string{"clear", "copy", "rotate", "transpose"} {
		cases = append(cases, patternParityCase{op, studioScore, studioEdit{Action: "range", Pattern: "pulse", Range: &studioPatternRange{Operation: op, First: 0, Last: 1, Target: 2, Amount: 1}}})
	}
	for _, action := range []string{"step", "duplicate", "range", "resize"} {
		body := studioEdit{Action: action, Pattern: "p", NewName: "variation", Step: 4, Length: 16, NoteEdit: &studioStepEdit{Mode: "note", Pitch: 72, Accent: true, Slide: true, Ratchet: 8, Chance: 23}, Range: &studioPatternRange{Operation: "reverse", First: 0, Last: 7}}
		cases = append(cases, patternParityCase{"shared " + action, studioPatternScore, body})
	}
	cases = append(cases, patternParityCase{"comment duplicate", strings.Replace(studioScore, "{ 1 . 5 . }", "{ 1 . 5 . } // keep me", 1), studioEdit{Action: "duplicate", Pattern: "pulse", NewName: "pulse2"}})
	cases = append(cases, patternParityCase{"slot duplicate", strings.Replace(studioPatternScore, "use hook +12 }", "use hook +12 } // keep me", 1), studioEdit{Action: "duplicate", Pattern: "p", NewName: "variation"}})
	return cases
}

func gridParityPitch(pitch int) *int { return &pitch }
