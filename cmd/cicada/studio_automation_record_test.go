package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/project"
)

func automationRecordStudio(t *testing.T) (*studio, []byte, *liveplay.Player) {
	t.Helper()
	isolateState(t)
	path := studioTestPath(t, curveScore)
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	score, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	player, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.stream, s.transport.playing, s.transport.audioNull, s.transport.sampleRate = player, true, true, liveSampleRate
	t.Cleanup(func() { s.shutdown() })
	return s, []byte(curveScore), player
}

func beginAutomation(t *testing.T, s *studio, path string, replace bool) string {
	t.Helper()
	source, _ := os.ReadFile(s.path)
	revision, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(map[string]any{"replace": replace})
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "start", Path: path, Revision: studioRevision(source), SourceRevision: revision, Value: value})
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	return s.automationRecording.ID
}

func TestRecordedAutomationUsesNativeClockLivePreviewAndOneUndo(t *testing.T) {
	s, source, player := automationRecordStudio(t)
	id := beginAutomation(t, s, "bass.level", false)
	for _, v := range []float64{-12, -3, -9} {
		if _, err := io.CopyN(io.Discard, player, int64(liveSampleRate/8*8)); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal(map[string]any{"number": v})
		r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision(source), Name: id, Value: value})
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		if got, ok := player.OverrideValue(0, kernel.ParamMixGain); !ok || got != float32(v) {
			t.Fatal("movement was not heard", got, ok)
		}
	}
	before, _ := os.ReadFile(s.path)
	if !bytes.Equal(source, before) {
		t.Fatal("recording movements saved partial source")
	}
	points := append([]recordedAutomationPoint(nil), s.automationRecording.Points...)
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Revision: studioRevision(source), Name: id})
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, after)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lane := range p.Automation {
		if lane.Path == "bass.level" {
			found = true
			if len(lane.Points) != len(points) {
				t.Fatal("lost recorded points")
			}
			for i, p := range lane.Points {
				if p.Tick != points[i].Tick || *p.Value.Number != points[i].Value {
					t.Fatal("stored curve differs from captured movement", i)
				}
			}
		}
	}
	if !found || s.automationRecording != nil {
		t.Fatal("take was not committed")
	}
	if _, err = os.Stat(s.automationDraftPath()); !os.IsNotExist(err) {
		t.Fatal("committed recovery draft remained", err)
	}
	r = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	restored, _ := os.ReadFile(s.path)
	if !bytes.Equal(source, restored) {
		t.Fatal("one Undo did not restore exact original source")
	}
}

func TestRecordedAutomationRecoveryAndConflictRetainsDraft(t *testing.T) {
	s, source, player := automationRecordStudio(t)
	id := beginAutomation(t, s, "bass.pan", false)
	io.CopyN(io.Discard, player, int64(liveSampleRate/4*8))
	value := json.RawMessage(`{"number":0.3}`)
	if r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision(source), Name: id, Value: value}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	s.automationRecording.Active = false
	if err := s.persistAutomationRecording(true); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.shutdown()
	r := studioCall(t, reopened.routes(), "/api/automation/recording", nil)
	if r.Code != 200 || reopened.automationRecording == nil || reopened.automationRecording.Active || len(reopened.automationRecording.Points) < 2 {
		t.Fatal("draft not recovered", r.Body.String())
	}
	external := append(bytes.Clone(source), []byte("// saved elsewhere\n")...)
	os.WriteFile(s.path, external, 0600)
	r = studioCall(t, reopened.routes(), "/api/automation/recording", studioEdit{Action: "keep", Revision: studioRevision(source), Name: id})
	if r.Code != 409 || reopened.automationRecording == nil {
		t.Fatal("conflicting take was not retained", r.Code, r.Body.String())
	}
	actual, _ := os.ReadFile(s.path)
	if !bytes.Equal(actual, external) {
		t.Fatal("conflict overwrote outside save")
	}
	r = studioCall(t, reopened.routes(), "/api/automation/recording", studioEdit{Action: "discard", Revision: studioRevision(source), Name: id})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
}

func TestRecordedAutomationRejectsReplacementAndInvalidValues(t *testing.T) {
	s, source, _ := automationRecordStudio(t)
	revision, _ := project.ReadScoreRevision(s.path)
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "start", Path: "bass.cutoff", Revision: studioRevision(source), SourceRevision: revision, Value: json.RawMessage(`{"replace":false}`)})
	if r.Code != 409 {
		t.Fatal("replaced curve without explicit choice", r.Code, r.Body.String())
	}
	id := beginAutomation(t, s, "bass.cutoff", true)
	count := len(s.automationRecording.Points)
	for _, value := range []json.RawMessage{json.RawMessage(`{"number":50000}`), json.RawMessage(`{"number":"bad"}`)} {
		r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision(source), Name: id, Value: value})
		if r.Code < 400 || len(s.automationRecording.Points) != count {
			t.Fatal("invalid movement changed take", r.Code)
		}
	}
	actual, _ := os.ReadFile(s.path)
	if !bytes.Equal(actual, source) {
		t.Fatal("invalid movement changed score")
	}
	if _, err := os.Stat(filepath.Join(takeRoot(s.path), ".cicada", "automation-take.json")); err != nil {
		t.Fatal("no recovery checkpoint", err)
	}
}

func TestRecordedAutomationStopRetainsReplacementUntilKept(t *testing.T) {
	s, source, player := automationRecordStudio(t)
	id := beginAutomation(t, s, "bass.cutoff", true)
	if _, err := io.CopyN(io.Discard, player, int64(liveSampleRate/4*8)); err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision(source), Name: id, Value: json.RawMessage(`{"number":1200}`)})
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	s.transport.mu.Lock()
	s.transport.playing = false
	s.transport.mu.Unlock()
	r = studioCall(t, s.routes(), "/api/automation/recording", nil)
	if r.Code != 200 || s.automationRecording.Active || len(s.automationRecording.Points) < 2 {
		t.Fatal("Stop did not retain a finished pass", r.Body.String())
	}
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Revision: studioRevision(source), Name: id})
	if r.Code != 200 {
		t.Fatal("replacement did not save", r.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, after)
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range p.Automation {
		if lane.Path == "bass.cutoff" && (len(lane.Points) != 2 || *lane.Points[1].Value.Number != 1200) {
			t.Fatal("replacement retained the old curve", lane)
		}
	}
	r = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
	undone, _ := os.ReadFile(s.path)
	if r.Code != 200 || !bytes.Equal(undone, source) {
		t.Fatal("one Undo did not restore replaced curve", r.Body.String())
	}
}
