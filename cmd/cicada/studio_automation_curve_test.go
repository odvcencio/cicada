package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
)

const curveScore = "cicada 2\ntempo 120\ntrack bass acid { cutoff=700Hz }\npattern riff acid steps=4 { 1 . 5 . }\nscene main { bass=riff }\nsong { main*4 }\n// keep this\nautomate bass.cutoff {\n  @1.1.1 400Hz // low\n  @5.1.1 1600Hz smooth // high\n}\n"

func TestStudioCurvePointPreservesCommentsUnitsAndOrder(t *testing.T) {
	source := []byte(strings.ReplaceAll(curveScore, "\n", "\r\n"))
	updated, err := intentCurvePoint(t, source, "bass.cutoff", studioCurvePoint{At: "@3.1.1", Value: "1.25kHz", Shape: "smooth"}, false)
	if err != nil {
		t.Fatal(err)
	}
	p := patternProject(t, updated)
	if len(p.Automation[0].Points) != 3 || *p.Automation[0].Points[1].Value.Number != 1250 {
		t.Fatal("point not inserted in order")
	}
	for _, want := range []string{"// keep this\r\n", "400Hz // low\r\n", "smooth // high\r\n"} {
		if !bytes.Contains(updated, []byte(want)) {
			t.Fatalf("comment/newline lost: %s", updated)
		}
	}
	updated, err = intentCurvePoint(t, updated, "bass.cutoff", studioCurvePoint{OldAt: "@3.1.1", At: "@4.1.1", Value: "900Hz", Shape: "curve", Curve: -1}, false)
	if err != nil {
		t.Fatal(err)
	}
	p = patternProject(t, updated)
	if p.Automation[0].Points[1].Tick != 11520 || p.Automation[0].Points[1].Curve != -1 {
		t.Fatal("move/shape failed")
	}
	updated, err = intentCurvePoint(t, updated, "bass.cutoff", studioCurvePoint{At: "@4.1.1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	p = patternProject(t, updated)
	if len(p.Automation[0].Points) != 2 {
		t.Fatal("point retained")
	}
	for _, bad := range []studioCurvePoint{{At: "@8.1.1", Value: "500Hz"}, {At: "1ticks", Value: "500Hz"}, {At: "@2.1.1", Value: "50000Hz"}, {OldAt: "@2.1.1", At: "@3.1.1", Value: "500Hz"}, {At: "@2.1.1", Value: "500Hz", Shape: "oops"}} {
		if _, err = intentCurvePoint(t, updated, "bass.cutoff", bad, false); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestStudioCurvePointRevisionAndUndo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.cicada")
	os.WriteFile(path, []byte(curveScore), 0600)
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	point, _ := json.Marshal(studioCurvePoint{At: "@2.1.1", Value: "-9.5dB", Shape: "linear"})
	edit := studioEdit{Revision: studioRevision([]byte(curveScore)), Action: "automation-point-set", Path: "bass.level", Value: point}
	if response := studioCall(t, handler, "/api/automation", edit); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if response := studioCall(t, handler, "/api/automation", edit); response.Code != 409 {
		t.Fatal("stale point accepted")
	}
	source, _ := os.ReadFile(path)
	if response := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(source)}); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	got, _ := os.ReadFile(path)
	if string(got) != curveScore {
		t.Fatal("Undo changed original")
	}
}

// intentCurvePoint edits one automation point through the intent and the Studio compiler.
func intentCurvePoint(t *testing.T, source []byte, path string, point studioCurvePoint, remove bool) ([]byte, error) {
	t.Helper()
	return intentSource(t, source, edits.ParamWriterAuto, curvePointIntent(path, point, remove))
}
