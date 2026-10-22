package control

import (
	"encoding/json"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/midi"
)

func TestChannelCapturePreservesItsKindAndValidatesEveryMIDIChannel(t *testing.T) {
	for channel := uint8(0); channel < 16; channel++ {
		r := Recording{Track: "keys", Pattern: "phrase", Notes: []Note{{Tick: 17, EndTick: 120, Note: 60, Velocity: 90}}}
		shared := midi.SharedControls{ID: uint64(channel) + 1, Device: "keys", Channel: channel, Kind: midi.SharedChannelKind, NoteControls: midi.NoteControls{Timbre: .5}}
		r.BindShared(0, shared)
		shared.PitchCents, shared.Pressure = 200, .8
		r.AddShared(shared, 960)
		if err := ValidateRecordings([]Recording{r}); err != nil || r.Groups[0].Kind != midi.SharedChannelKind || r.Groups[0].Points[1].Tick != 960 {
			t.Fatal("ordinary channel metadata or release movement was lost", channel, r, err)
		}
		r.Groups[0].Kind = "unknown"
		if ValidateRecordings([]Recording{r}) == nil {
			t.Fatal("unknown controller kind was accepted")
		}
		r.Groups[0].Kind = ""
		if err := ValidateRecordings([]Recording{r}); channel != 0 && channel != 15 && err == nil {
			t.Fatal("a non-master channel lost its required ordinary kind")
		}
	}
}

func TestSharedCaptureKeepsRawSamplesAndTailWithoutStretchingGates(t *testing.T) {
	recording := Recording{Track: "keys", Pattern: "phrase", Notes: []Note{{Tick: 3857, EndTick: 3960, Note: 60, Velocity: 100}, {Tick: 3860, EndTick: 4100, Note: 64, Velocity: 90}}}
	shared := midi.SharedControls{ID: 7, Device: "native:surface:g0", Channel: 0, NoteControls: midi.NoteControls{Timbre: .5}}
	if recording.BindShared(0, shared) != 1 || recording.BindShared(1, shared) != 0 {
		t.Fatal("notes did not share one seed")
	}
	shared.PitchCents, shared.Pressure = 100, .7
	recording.AddShared(shared, 4200)
	shared.PitchCents = -50
	recording.AddShared(shared, 4200)
	if recording.Notes[0].Group != 1 || recording.Notes[1].Group != 1 || recording.Notes[0].EndTick != 3960 || recording.Notes[1].EndTick != 4100 || len(recording.Groups[0].Points) != 3 || recording.ExpressionPointCount() != 3 {
		t.Fatal("shared capture lost samples or stretched a note", recording)
	}
	if err := ValidateRecordings([]Recording{recording}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(recording)
	var restored Recording
	if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(recording, restored) {
		t.Fatal("raw JSON checkpoint changed the shared take", err)
	}
	foreign := shared
	foreign.ID++
	if recording.AddShared(foreign, 4300) || len(recording.Groups[0].Points) != 3 {
		t.Fatal("a reconnect or another input rewrote the old lane")
	}
}

func TestSharedCaptureValidationBudgetsNotesAndLanesTogether(t *testing.T) {
	r := Recording{Track: "keys", Pattern: "phrase", Notes: []Note{{Tick: 0, EndTick: 1, Note: 60, Velocity: 90, Group: 1}}, Groups: []RecordedGroup{{ID: 1, Device: "a", Points: []NoteExpression{{Timbre: .5}}}}}
	r.Notes[0].Expressions = make([]NoteExpression, MaxExpressionPoints)
	if ValidateRecordings([]Recording{r}) == nil {
		t.Fatal("shared samples bypassed the gesture budget")
	}
	r.Notes[0].Expressions = nil
	r.Notes[0].Group = 2
	if ValidateRecordings([]Recording{r}) == nil {
		t.Fatal("a note referenced a missing group")
	}
	r.Notes[0].Group = 1
	r.Groups[0].Points[0].Tick = -1
	if ValidateRecordings([]Recording{r}) == nil {
		t.Fatal("negative shared timing was accepted")
	}
}

func TestSharedCaptureKeepsLeadInBeforeTheFirstNote(t *testing.T) {
	r := Recording{Track: "keys", Pattern: "phrase", Notes: []Note{{Tick: 3857, EndTick: 4100, Note: 60, Velocity: 90}}}
	lead := []NoteExpression{{Tick: 0, Timbre: .5}, {Tick: 3840, Pressure: .7, Timbre: .8}}
	shared := midi.SharedControls{ID: 1, Device: "surface", NoteControls: midi.NoteControls{Pressure: .7, Timbre: .8}}
	if added := r.BindShared(0, shared, lead...); added != 0 || !reflect.DeepEqual(r.Groups[0].Points, lead) {
		t.Fatal("captured lead-in was replaced by a note-on seed", r)
	}
	lead[0].Pressure = 1
	if r.Groups[0].Points[0].Pressure != 0 {
		t.Fatal("pending input owns the saved group's storage")
	}
	if err := ValidateRecordings([]Recording{r}); err != nil {
		t.Fatal(err)
	}
}
