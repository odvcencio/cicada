package main

import (
	"strings"
	"testing"

	"encoding/json"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/edit/editlog"
)

// The recorded outcomes are what the retired curvePointSource produced.
func TestAutomationPointIntentsWriteTheRecordedBytes(t *testing.T) {
	golden := m10Golden(t, "automation")
	crlf := strings.ReplaceAll(curveScore, "\n", "\r\n")
	noLane := strings.Split(curveScore, "// keep this")[0]
	oneLane := noLane + "automate bass.cutoff {\n  @2.1.1 400Hz\n}\n"
	noSong := "cicada 2\ntempo 120\ntrack bass acid { cutoff=700Hz }\npattern riff acid steps=4 { 1 . 5 . }\nscene main { bass=riff }\nsong { main*4 }\narrange {\n  place a bass riff { at = @1.1.1 length = 4bars }\n}\n"
	cases := []struct {
		name, source, path string
		point              studioCurvePoint
		remove             bool
	}{
		{"insert between", curveScore, "bass.cutoff", studioCurvePoint{At: "@3.1.1", Value: "1.25kHz", Shape: "smooth"}, false},
		{"insert crlf", crlf, "bass.cutoff", studioCurvePoint{At: "@3.1.1", Value: "1.25kHz", Shape: "smooth"}, false},
		{"insert before first", curveScore, "bass.cutoff", studioCurvePoint{At: "@1.2.1", Value: "500Hz"}, false},
		{"insert after last", curveScore, "bass.cutoff", studioCurvePoint{At: "@9.1.1", Value: "500Hz", Shape: "step"}, false},
		{"insert exponential", curveScore, "bass.cutoff", studioCurvePoint{At: "@3.1.1", Value: "500Hz", Shape: "exponential"}, false},
		{"insert curve", curveScore, "bass.cutoff", studioCurvePoint{At: "@3.1.1", Value: "500Hz", Shape: "curve", Curve: -1.5}, false},
		{"new lane", noLane, "bass.cutoff", studioCurvePoint{At: "@2.1.1", Value: "500Hz"}, false},
		{"new lane crlf", strings.ReplaceAll(noLane, "\n", "\r\n"), "bass.level", studioCurvePoint{At: "@2.1.1", Value: "-9.5dB"}, false},
		{"move", curveScore, "bass.cutoff", studioCurvePoint{OldAt: "@5.1.1", At: "@4.1.1", Value: "900Hz", Shape: "curve", Curve: -1}, false},
		{"replace in place", curveScore, "bass.cutoff", studioCurvePoint{OldAt: "@1.1.1", At: "@1.1.1", Value: "450Hz"}, false},
		{"replace same position", curveScore, "bass.cutoff", studioCurvePoint{At: "@1.1.1", Value: "450Hz"}, false},
		{"occupied", curveScore, "bass.cutoff", studioCurvePoint{OldAt: "@1.1.1", At: "@5.1.1", Value: "450Hz"}, false},
		{"stale old position", curveScore, "bass.cutoff", studioCurvePoint{OldAt: "@2.1.1", At: "@3.1.1", Value: "500Hz"}, false},
		{"stale old position no lane", noLane, "bass.cutoff", studioCurvePoint{OldAt: "@2.1.1", At: "@3.1.1", Value: "500Hz"}, false},
		{"bad old position", curveScore, "bass.cutoff", studioCurvePoint{OldAt: "later", At: "@3.1.1", Value: "500Hz"}, false},
		{"remove", curveScore, "bass.cutoff", studioCurvePoint{At: "@5.1.1"}, true},
		{"remove by old position", curveScore, "bass.cutoff", studioCurvePoint{OldAt: "@1.1.1", At: "@5.1.1"}, true},
		{"remove last point", oneLane, "bass.cutoff", studioCurvePoint{At: "@2.1.1"}, true},
		{"remove missing", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1"}, true},
		{"remove no lane", noLane, "bass.cutoff", studioCurvePoint{At: "@2.1.1"}, true},
		{"remove bad shape", curveScore, "bass.cutoff", studioCurvePoint{At: "@5.1.1", Shape: "oops"}, true},
		{"off the song", curveScore, "bass.cutoff", studioCurvePoint{At: "@8.1.1", Value: "500Hz"}, false},
		{"song end", curveScore, "bass.cutoff", studioCurvePoint{At: "@5.1.1", Value: "500Hz"}, false},
		{"off the grid", curveScore, "bass.cutoff", studioCurvePoint{At: "1ticks", Value: "500Hz"}, false},
		{"bad position", curveScore, "bass.cutoff", studioCurvePoint{At: "soon", Value: "500Hz"}, false},
		{"out of range", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1", Value: "50000Hz"}, false},
		{"wrong unit", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1", Value: "3dB"}, false},
		{"empty value", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1"}, false},
		{"bad shape", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1", Value: "500Hz", Shape: "oops"}, false},
		{"tension without curve", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1", Value: "500Hz", Curve: 1}, false},
		{"tension too large", curveScore, "bass.cutoff", studioCurvePoint{At: "@2.1.1", Value: "500Hz", Shape: "curve", Curve: 9}, false},
		{"unknown parameter", curveScore, "ghost.pan", studioCurvePoint{At: "@2.1.1", Value: "0"}, false},
		{"toggle parameter", curveScore, "bass.insert", studioCurvePoint{At: "@2.1.1", Value: "none"}, false},
		{"arrange end", noSong, "bass.level", studioCurvePoint{At: "@5.1.1", Value: "-3dB"}, false},
		{"arrange past end", noSong, "bass.level", studioCurvePoint{At: "@5.2.1", Value: "-3dB"}, false},
		{"invalid score", "cicada 2\ntrack bass acid {\n", "bass.level", studioCurvePoint{At: "@1.1.1", Value: "-3dB"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := edits.Apply([]byte(c.source), edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{curvePointIntent(c.path, c.point, c.remove)}}, edits.Options{Compiler: arrangementTextOnlyPlan{}, Edition: 2})
			var source []byte
			if err == nil {
				source = got.Source
			}
			m10Check(t, golden, c.name, source, err)
		})
	}
}

// arrangementTextOnlyPlan compiles what the old writer compiled (the parsed
// score as a project) and accepts any candidate, so only the text edit differs.
type arrangementTextOnlyPlan struct{}

func (arrangementTextOnlyPlan) Compile(source []byte, files map[string][]byte) (*edits.Plan, error) {
	plan, err := looseCompiler{}.Compile(source, files)
	if err != nil {
		return &edits.Plan{Revision: edits.Revision(source)}, nil
	}
	return plan, nil
}

func TestAutomationPointRouteLogsIntentAndRefusesDuringCapture(t *testing.T) {
	path := studioTestPath(t, curveScore)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.routes()
	point, _ := json.Marshal(studioCurvePoint{At: "@2.1.1", Value: "-9.5dB", Shape: "linear"})
	edit := studioEdit{Revision: studioRevision([]byte(curveScore)), Action: "automation-point-set", Path: "bass.level", Value: point}
	s.captureID = "take"
	response := studioCall(t, handler, "/api/automation", edit)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "finish the recording before editing automation") {
		t.Fatalf("capture: %d %s", response.Code, response.Body.String())
	}
	s.captureID = ""
	if response := studioCall(t, handler, "/api/automation", edit); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	records, _, err := editlog.ReadLog(editlog.Path(path))
	if err != nil || len(records) != 1 || len(records[0].Intents) != 1 || !strings.Contains(string(records[0].Intents[0]), `"kind":"setautomationpoint"`) || records[0].Label != "Source saved" {
		t.Fatalf("%+v %v", records, err)
	}
}
