package main

import (
	"bytes"
	"encoding/json"
	"m31labs.dev/cicada/edit/editlog"
	"m31labs.dev/cicada/project"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestStudioVocalChainAddsEditableOrderedStagesAndIsIdempotent(t *testing.T) {
	path := mixerProject(t, "project test\ncicada 2\n", studioMixerScore)
	reply := mixerRoute(t, path, studioEdit{Action: "vocal-chain", Track: "bass"})
	if reply.Code != http.StatusOK {
		t.Fatalf("%d %+v", reply.Code, reply.Body)
	}
	if !strings.Contains(string(reply.Score), "insert = grit -> bass_vocal_hp -> bass_vocal_comp -> bass_vocal_deess") {
		t.Fatalf("existing chain lost\n%s", reply.Score)
	}
	p, err := compileStudioSource(path, reply.Score)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil || len(cfg.Track[0].InsertChain) != 4 {
		t.Fatalf("chain not prepared %v", err)
	}
	view, err := buildStudioMixerView(path, reply.Score)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Tracks[0].InsertOK {
		t.Fatal("insert chain not editable")
	}
	var eq, deesser int
	for _, effect := range view.Effects {
		if effect.Kind == "eq" {
			eq = len(effect.Fields)
		}
		if effect.Kind == "deesser" {
			deesser = len(effect.Fields)
		}
	}
	if eq != 4 || deesser != 7 {
		t.Fatalf("missing descriptors EQ %d deesser %d", eq, deesser)
	}
	again := mixerRoute(t, path, studioEdit{Action: "vocal-chain", Track: "bass"})
	if again.Code != http.StatusOK || !bytes.Equal(reply.Score, again.Score) || again.Body["unchanged"] != true {
		t.Fatalf("second click changed chain: %d %+v", again.Code, again.Body)
	}
	edited := mixerRoute(t, path, studioEdit{Path: "bass_vocal_deess.threshold", Value: json.RawMessage(`"-30dB"`)})
	if edited.Code != http.StatusOK || !bytes.Contains(edited.Score, []byte("threshold = -30dB")) {
		t.Fatalf("de-esser edit %d %+v", edited.Code, edited.Body)
	}
}
func TestStudioVocalChainRefusesRecordingAndStaleRevision(t *testing.T) {
	f := newRecordingTransportFixture(t, true, true)
	r := studioCall(t, f.studio.routes(), "/api/mixer", studioEdit{Action: "vocal-chain", Track: "vox", Revision: studioRevision(f.source)})
	if r.Code < 400 || !strings.Contains(r.Body.String(), "finish the recording") {
		t.Fatalf("capture chain edit %d %s", r.Code, r.Body)
	}
	r = studioCall(t, f.studio.routes(), "/api/mixer", studioEdit{Path: "vox.level", Value: json.RawMessage(`-12`), Revision: studioRevision(f.source)})
	if r.Code != http.StatusConflict || !strings.Contains(r.Body.String(), "Finish the take") {
		t.Fatalf("capture mixer write %d %s", r.Code, r.Body)
	}
	source, _ := os.ReadFile(f.studio.path)
	if !bytes.Equal(source, f.source) {
		t.Fatal("active take source changed")
	}
	s := newTakeStudio(t, t.TempDir())
	r = studioCall(t, s.routes(), "/api/mixer", studioEdit{Action: "vocal-chain", Track: "vox", Revision: "stale"})
	if r.Code != http.StatusConflict {
		t.Fatalf("stale chain edit %d %s", r.Code, r.Body)
	}
}

// The chain's three effects and their settings reach the edit log as intents.
func TestStudioVocalChainLogsItsIntents(t *testing.T) {
	path := mixerProject(t, "project test\ncicada 2\n", studioMixerScore)
	if reply := mixerRoute(t, path, studioEdit{Action: "vocal-chain", Track: "bass"}); reply.Code != http.StatusOK {
		t.Fatalf("%d %+v", reply.Code, reply.Body)
	}
	records, _, err := editlog.ReadLog(editlog.Path(path))
	if err != nil || len(records) != 1 {
		t.Fatalf("%+v %v", records, err)
	}
	kinds := map[string]int{}
	for _, raw := range records[0].Intents {
		var intent struct{ Kind string }
		if err := json.Unmarshal(raw, &intent); err != nil {
			t.Fatal(err)
		}
		kinds[intent.Kind]++
	}
	if kinds["addeffect"] != 3 || kinds["setparam"] != 7 || records[0].Label != "Add vocal chain to bass" {
		t.Fatalf("%v %q", kinds, records[0].Label)
	}
}
