package main

import (
	"bytes"
	"os"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

func TestStudioPlayedTakeIsAtomicEditableAuditionableAndUndoable(t *testing.T) {
	source := `track keys piano {}
pattern phrase notes { . . . . }
scene main { keys=phrase }
song { main }
`
	_, handler, path := proposalFixture(t, source)
	take := studioEdit{Revision: studioRevision([]byte(source)), SourceRevision: studioProjectReceipt(t, path), Timing: "played", Recordings: []studioTakeRecording{{Track: "keys", Pattern: "phrase", Notes: []studioTakeNote{{Tick: 17, EndTick: 4107, Note: 60, Velocity: 82}, {Tick: 19, EndTick: 1220, Note: 64, Velocity: 105}}}}}
	env := edit.Envelope{Version: 1, Revision: take.Revision, DryRun: true, Intents: []edit.Intent{&edit.RecordTake{Timing: "played", Recordings: editRecordings(take.Recordings)}}}
	if r := studioCall(t, handler, "/api/intents", env); r.Code != 200 {
		t.Fatalf("played check: %d %s", r.Code, r.Body.String())
	}
	if b, _ := os.ReadFile(path); string(b) != source {
		t.Fatal("dry run changed the score")
	}
	if r := studioCall(t, handler, "/api/record", take); r.Code != 200 {
		t.Fatalf("played take: %d %s", r.Code, r.Body.String())
	}
	performed, _ := os.ReadFile(path)
	p, err := compileStudioSource(path, performed)
	if err != nil {
		t.Fatal(err)
	}
	if p.Patterns[0].Performance == nil || p.Patterns[0].Performance.Notes[0].EndTick != 4107 {
		t.Fatal("committed score lost held timing")
	}
	preview, listen, err := prepareStudioListening(p, studioListening{Kind: "pattern", Name: "phrase", Loop: true})
	if err != nil || listen.EndTick != 7680 || preview.Arrange == nil || p.Arrange != nil || len(p.Song) != 1 {
		t.Fatal("full loop audition changed or truncated the song", err)
	}
	if _, err := project.CompileEngine(preview, 48000, 128); err != nil {
		t.Fatal("performance audition did not compile", err)
	}
	if r := studioCall(t, handler, "/api/record", take); r.Code != 409 {
		t.Fatal("stale take was applied again", r.Code)
	}
	receipt, _ := project.ReadScoreRevision(path)
	change := studioEdit{Revision: studioRevision(performed), SourceRevision: receipt, Action: "performed-note", Pattern: "phrase", PerformanceNote: &edit.SetPerformedNote{Index: 1, Tick: 23, EndTick: 1200, Pitch: 67, Velocity: 88}}
	if r := studioCall(t, handler, "/api/pattern", change); r.Code != 200 {
		t.Fatalf("performed edit: %d %s", r.Code, r.Body.String())
	}
	edited, _ := os.ReadFile(path)
	receipt, _ = project.ReadScoreRevision(path)
	if r := studioCall(t, handler, "/api/pattern", studioEdit{Revision: studioRevision(edited), SourceRevision: receipt, Action: "performance-length", Pattern: "phrase", PerformanceLength: 3840}); r.Code != 422 {
		t.Fatal("loop shortening discarded held notes", r.Code)
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(edited)}); r.Code != 200 {
		t.Fatalf("edit Undo: %d %s", r.Code, r.Body.String())
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, performed) {
		t.Fatal("Undo did not restore the original performed gates")
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(performed)}); r.Code != 200 {
		t.Fatalf("take Undo: %d %s", r.Code, r.Body.String())
	}
	if b, _ := os.ReadFile(path); string(b) != source {
		t.Fatal("take Undo did not restore the exact original source")
	}
}

func TestStudioGestureEditUsesRevisionAndUndo(t *testing.T) {
	source := `track lead acid {}
pattern phrase performance length=3840ticks {
 note 60 at=17ticks length=703ticks velocity=82 {
  gesture at=0ticks bend=0ct pressure=0.2 timbre=0.5;
 }
}
scene main { lead=phrase }
song { main }
`
	_, handler, path := proposalFixture(t, source)
	receipt, _ := project.ReadScoreRevision(path)
	change := studioEdit{Revision: studioRevision([]byte(source)), SourceRevision: receipt, Action: "performed-gesture", Pattern: "phrase", PerformanceExpression: &edit.SetPerformedExpression{Entity: "wrong-pattern", NoteIndex: 0, Index: 1, Tick: 123, PitchCents: -123.5, Pressure: .7, Timbre: .8, VibratoRateHz: 7.25, VibratoDepthCents: 14.5}}
	if r := studioCall(t, handler, "/api/pattern", change); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	edited, _ := os.ReadFile(path)
	p, err := compileStudioSource(path, edited)
	if err != nil {
		t.Fatal(err)
	}
	points := p.Patterns[0].Performance.Notes[0].Expressions
	if len(points) != 2 || points[1].Tick != 123 || points[1].VibratoRateHz != 7.25 {
		t.Fatal("gesture changed precision or escaped pattern target", points)
	}
	if r := studioCall(t, handler, "/api/pattern", change); r.Code != 409 {
		t.Fatal("stale gesture accepted", r.Code)
	}
	if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(edited)}); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if b, _ := os.ReadFile(path); string(b) != source {
		t.Fatal("gesture Undo failed to restore exact source")
	}
}
