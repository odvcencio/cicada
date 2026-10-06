package liveplay

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

type trackedKeyboard struct {
	notes   [128]uint8
	pedal   float32
	release [128]bool
}

func (v *trackedKeyboard) NoteOn(note, velocity uint8) error {
	v.notes[note], v.release[note] = velocity, false
	return nil
}
func (v *trackedKeyboard) NoteOff(note uint8) {
	v.release[note] = true
	if v.pedal < .5 {
		v.notes[note] = 0
	}
}
func (v *trackedKeyboard) AllNotesOff() {
	for note := range v.notes {
		v.NoteOff(uint8(note))
	}
}
func (v *trackedKeyboard) NextStereo() (float32, float32) {
	var value float32
	for _, velocity := range v.notes {
		value += float32(velocity) * .0001
	}
	return value, value
}
func (v *trackedKeyboard) SetSustain(value float32) error {
	v.pedal = value
	if value < .5 {
		for note, released := range v.release {
			if released {
				v.notes[note] = 0
			}
		}
	}
	return nil
}
func (v *trackedKeyboard) Reset() { *v = trackedKeyboard{} }

func liveKeyboardScore(t *testing.T) (Score, *trackedKeyboard) {
	t.Helper()
	previous := keyboard.Prepare
	t.Cleanup(func() { keyboard.Prepare = previous })
	voice := new(trackedKeyboard)
	keyboard.Prepare = func(int, *keyboard.Spec) (keyboard.Voice, error) { return voice, nil }
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 3, MaxVoices: 3, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceKeys
	cfg.Track[0].Keys = &keyboard.Spec{Patch: 1}
	cfg.Track[0].Keys.Controls[127] = 1
	cfg.Track[1].Kind = engine.VoiceGraph
	cfg.Track[1].Graph = graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pressure}}}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{
		{ID: "part", Kind: "fm_ep"},
		{ID: "authored", Kind: "bell_keys", Pitched: true},
		{ID: "sample", Kind: "fm_bass"},
	}}, voice
}

func TestKeysLiveDeliveryRetainsVelocityPedalAndPitchRelease(t *testing.T) {
	score, voice := liveKeyboardScore(t)
	p, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, note := range []int{20, 109} {
		if err := p.Note("part", note, 100, true); err == nil {
			t.Fatalf("keys accepted out-of-range MIDI pitch %d", note)
		}
	}
	if err := p.NoteWithID("part", 60, 32, true, 500); err != nil {
		t.Fatal(err)
	}
	if err := p.NoteWithID("part", 64, 127, true, 501); err != nil {
		t.Fatal(err)
	}
	if err := p.SetParam(0, kernel.ParamPianoSustain, 1); err != nil {
		t.Fatal(err)
	}
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if voice.notes[60] != 32 || voice.notes[64] != 127 || voice.pedal != 1 {
		t.Fatal("native keys lost velocity or sustain")
	}
	if err := p.NoteWithID("part", 60, 0, false, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if !voice.release[60] || voice.release[64] || voice.notes[60] != 32 {
		t.Fatal("keys note identity replaced MIDI pitch release or bypassed sustain")
	}
	if err := p.SetParam(0, kernel.ParamPianoSustain, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if voice.notes[60] != 0 || voice.notes[64] != 127 || p.fault != nil {
		t.Fatal("keys pedal release changed an unrelated held pitch")
	}
}

func TestKeysLiveClassificationPreservesAuthoredOverrides(t *testing.T) {
	score, _ := liveKeyboardScore(t)
	p, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.NoteExpression("part", 7, 25, .5, .75); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
		t.Fatalf("keys expression did not return an explicit unsupported error: %v", err)
	}
	if p.notes.Load() != nil {
		t.Fatal("unsupported keys expression entered the render queue")
	}
	if err := p.NoteWithID("authored", 0, 100, true, 7); err != nil {
		t.Fatalf("authored graph inherited modeled keys pitch bounds: %v", err)
	}
	if err := p.NoteExpression("authored", 7, 25, .5, .75); err != nil {
		t.Fatalf("authored graph inherited modeled keys expression rejection: %v", err)
	}
	if err := p.Note("sample", 60, 100, true); err == nil {
		t.Fatal("unavailable sample track was normalized to a modeled keyboard")
	}
	// An expression queued for a previous graph score must report its rejection
	// if a score replacement makes that track a modeled keyboard before drain.
	p.notes.Store(&noteBatch{count: 1, inputs: [256]noteInput{{Track: "part", NoteID: 7, Expression: true, Timbre: .5}}})
	p.queueLiveNotes()
	select {
	case event := <-p.Events():
		if event.Kind != "note-error" || !strings.Contains(event.Name, "CICADA-UNSUPPORTED") {
			t.Fatalf("queued keys expression rejection: %+v", event)
		}
	default:
		t.Fatal("queued keys expression was silently discarded")
	}
	var left, right [blockFrames]float32
	p.current.Engine.Render(left[:], right[:])
	for _, value := range left {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			t.Fatal("expression rejection produced nonfinite output")
		}
	}
}

func TestKeysLiveOwnersSharePitchWithoutReleasingOtherKeys(t *testing.T) {
	score, voice := liveKeyboardScore(t)
	p, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, press := range []struct {
		id              string
		pitch, velocity int
	}{{"first", 60, 32}, {"second", 60, 100}, {"other", 64, 127}} {
		if err := p.NoteID("part", press.pitch, press.velocity, true, press.id); err != nil {
			t.Fatal(err)
		}
	}
	drainNotes(t, p)
	if voice.notes[60] != 100 || voice.notes[64] != 127 {
		t.Fatal("owned keyboard presses lost velocity or a separate pitch")
	}
	if err := p.NoteID("part", 60, 0, false, "first"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 0 || voice.release[60] || voice.release[64] {
		t.Fatalf("earlier owner released a held pitch: %+v", got)
	}
	if err := p.NoteID("part", 60, 0, false, "second"); err != nil {
		t.Fatal(err)
	}
	if got := drainNotes(t, p); len(got) != 1 || got[0].Kind != "note-off" || voice.notes[60] != 0 || voice.notes[64] != 127 {
		t.Fatalf("final owner's release changed an unrelated keyboard pitch: %+v", got)
	}
}

func TestKeysLiveReleaseOverloadClearsHeldAndQueuedNotes(t *testing.T) {
	score, voice := liveKeyboardScore(t)
	p, err := New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for i := 0; i < 128; i++ {
		if err := p.NoteID("part", 21+i%88, 100, true, fmt.Sprint("old:", i)); err != nil {
			t.Fatal(err)
		}
	}
	drainNotes(t, p)
	for i := 0; i < 128; i++ {
		pitch := 21 + i%88
		if err := p.NoteID("part", pitch, 0, false, fmt.Sprint("old:", i)); err != nil {
			t.Fatal(err)
		}
		if err := p.NoteID("part", pitch, 127, true, fmt.Sprint("new:", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 128; i++ {
		if err := p.NoteID("part", 21+i%88, 0, false, fmt.Sprint("new:", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.NoteID("part", 60, 127, true, "barrier"); err == nil {
		t.Fatal("keys press bypassed the release-overload barrier")
	}
	drainNotes(t, p)
	for note, velocity := range voice.notes {
		if velocity != 0 {
			t.Fatalf("keyboard pitch %d remained held after release overload", note)
		}
	}
	for _, order := range p.heldOrder {
		if order != 0 {
			t.Fatal("keyboard ownership survived release overload")
		}
	}
	if p.fault != nil || p.notes.Load() != nil || p.noteEpoch.Load()&1 != 0 {
		t.Fatal("keys overload left a fault, queued input, or pending barrier")
	}
}
