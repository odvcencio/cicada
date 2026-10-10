package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
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

func legacyPatternWrite(source []byte, b studioEdit) ([]byte, error) {
	switch b.Action {
	case "settings":
		return patternSettingsSource(source, b.Pattern, b.Settings)
	case "step":
		return patternStepSource(source, b.Pattern, b.Lane, b.Step, b.NoteEdit)
	case "duplicate":
		return duplicatePatternSource(source, b.Pattern, b.NewName)
	case "range":
		return patternRangeSource(source, b.Pattern, b.Lane, b.Range)
	case "resize":
		return resizePatternSource(source, b.Pattern, b.Length)
	case "bind":
		return bindPatternSource(source, b.Scene, b.Track, b.Pattern)
	}
	return nil, fmt.Errorf("unknown pattern action")
}

func patternIntentJSON(b studioEdit) string {
	fields := map[string]any{"entity": "pattern:" + b.Pattern}
	switch b.Action {
	case "settings":
		fields["kind"] = "setpatternsettings"
		if b.Settings != nil {
			fields["swing100"] = b.Settings.Swing100
			fields["gate"] = b.Settings.Gate
			fields["transpose"] = b.Settings.Transpose
		}
	case "step":
		fields["kind"] = "setstep"
		fields["entity"] = fmt.Sprintf("step:%s/%d", b.Pattern, b.Step)
		if b.Lane != "" {
			fields["entity"] = fmt.Sprintf("step:%s/%s/%d", b.Pattern, b.Lane, b.Step)
		}
		fields["shared"] = "pattern"
		if b.NoteEdit != nil {
			data, _ := json.Marshal(b.NoteEdit)
			var note map[string]any
			_ = json.Unmarshal(data, &note)
			for k, v := range note {
				fields[k] = v
			}
		}
	case "duplicate":
		fields["kind"] = "duplicatepattern"
		fields["name"] = b.NewName
	case "range":
		fields["kind"] = "setrange"
		fields["lane"] = b.Lane
		fields["shared"] = "pattern"
		fields["first"] = -1
		if b.Range != nil {
			data, _ := json.Marshal(b.Range)
			var r map[string]any
			_ = json.Unmarshal(data, &r)
			for k, v := range r {
				fields[k] = v
			}
		}
	case "resize":
		fields["kind"] = "resizepattern"
		fields["length"] = b.Length
		fields["shared"] = "pattern"
	case "bind":
		delete(fields, "entity")
		fields["kind"] = "bindscene"
		fields["scene"] = b.Scene
		fields["track"] = b.Track
		fields["pattern"] = b.Pattern
	}
	raw, _ := json.Marshal(fields)
	return string(raw)
}

func TestPatternIntentWriterParity(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, c := range patternParityCases() {
			t.Run(fmt.Sprintf("%s/%q", c.name, newline), func(t *testing.T) {
				source := bytes.ReplaceAll([]byte(c.source), []byte("\n"), []byte(newline))
				assertWriterParity(t, source, patternIntentJSON(c.body), studioEditLabel(c.body), func(s []byte) ([]byte, error) { return legacyPatternWrite(s, c.body) })
			})
		}
	}
}
