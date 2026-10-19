package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestNoteClockBoundsFallbackAndAudition(t *testing.T) {
	s := PlaybackState{Playing: true, Bar: 2, Step: 3}
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		bpm     float64
		want    int64
	}{
		{"no elapsed", 0, 120, 4320},
		{"one eighth note", 250 * time.Millisecond, 120, 4800},
		{"stale clock", time.Hour, 120, 4800},
		{"negative elapsed", -time.Second, 120, 4320},
		{"invalid tempo", time.Second, math.NaN(), 4320},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.Playhead().NoteTick(tc.elapsed, tc.bpm); got != tc.want {
				t.Fatalf("recording tick=%d, want %d", got, tc.want)
			}
		})
	}
	s.Playing = false
	if got := s.Playhead().NoteTick(time.Second, 120); got != 4320 {
		t.Fatalf("stopped recording advanced to %d", got)
	}
	s.Playing, s.Bar, s.Step = true, 2, 16
	s.Range = &PlaybackRange{Start: 3840, End: 7680, Loop: true}
	if got := s.Playhead().NoteTick(250*time.Millisecond, 120); got != 4080 {
		t.Fatalf("loop recording did not wrap: %d", got)
	}
	s.Range.Loop = false
	if got := s.Playhead().NoteTick(250*time.Millisecond, 120); got != 7680 {
		t.Fatalf("audition recording advanced beyond its end: %d", got)
	}
}

// The input starts on each beat in bar two. Simulate the native audio frame
// arriving 37 ms before each note, then commit the same JSON that Live sends.
// The old 480-PPQ clock placed these notes on steps 9, 11, 13 and 15 instead.
func TestNativeNoteClockCommitsBeatsToTheirPlayedSteps(t *testing.T) {
	for _, bpm := range []int64{60, 120, 180, 240} {
		t.Run(fmt.Sprint(bpm), func(t *testing.T) {
			clock, err := seq.NewClock(48000, bpm*1000)
			if err != nil {
				t.Fatal(err)
			}
			take := Recording{Track: "keys", Pattern: "take"}
			for beat := int64(0); beat < 4; beat++ {
				want := (4 + beat) * seq.PPQ
				sample := clock.SampleAtTick(want) - 1776 // 37 ms at 48 kHz.
				tick := clock.TickAtSample(sample)
				frame := liveplay.TransportFrame{
					Bar: int32(tick/seq.TicksPerBar + 1), Beat: int32(tick%seq.TicksPerBar/seq.PPQ + 1),
					Tick: int32(tick % seq.PPQ), TempoMilli: uint32(bpm * 1000), Playing: true,
				}
				var raw [liveplay.TransportFrameSize]byte
				frame.Encode(raw[:])
				state := PlaybackState{Playing: true, Bar: int64(frame.Bar), Step: tick%seq.TicksPerBar/seq.TicksPerStep + 1}
				state.Frames.Transport = base64.StdEncoding.EncodeToString(raw[:])
				got := state.Playhead().NoteTick(37*time.Millisecond, 100) // Native tempo wins.
				if delta := got - want; delta < -1 || delta > 1 {
					t.Fatalf("beat %d recorded at %d instead of %d", beat+1, got, want)
				}
				take.Notes = append(take.Notes, Note{Tick: got, EndTick: got + seq.PPQ/2, Note: 60 + int(beat), Velocity: 100})
			}
			data, _ := json.Marshal([]Recording{take})
			var recordings []edit.Recording
			if err := json.Unmarshal(data, &recordings); err != nil {
				t.Fatal(err)
			}
			source := []byte("cicada 2\ntrack keys acid {}\npattern take acid steps=16 { . . . . . . . . . . . . . . . . }\nscene main { keys=take }\nsong { main*4 }\n")
			result, err := edit.Apply(source, edit.Envelope{Version: edit.EnvelopeVersion, Intents: []edit.Intent{&edit.RecordTake{Recordings: recordings}}}, edit.Options{Compiler: noteClockCompiler{}})
			if err != nil {
				t.Fatal(err)
			}
			p, err := noteClockProject(result.Source)
			if err != nil {
				t.Fatal(err)
			}
			for step, note := range p.Patterns[0].Data {
				if step%4 == 0 {
					if note == nil || note.Note != uint8(60+step/4) {
						t.Fatalf("step %d lost its played beat: %+v\n%s", step+1, note, result.Source)
					}
				} else if note != nil {
					t.Fatalf("unexpected note at step %d: %+v", step+1, note)
				}
			}
		})
	}
}

type noteClockCompiler struct{}

func (noteClockCompiler) Compile(source []byte, _ map[string][]byte) (*edit.Plan, error) {
	p, err := noteClockProject(source)
	if err != nil {
		return nil, err
	}
	return project.EditPlan(p, source), nil
}

func noteClockProject(source []byte) (*project.Project, error) {
	score, diagnostics := notation.Parse(source)
	if score == nil {
		return nil, fmt.Errorf("invalid recorded score: %+v", diagnostics)
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("invalid recorded score: %s", d.Message)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		return nil, fmt.Errorf("invalid recorded project: %+v", diagnostics)
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("invalid recorded project: %s", d.Message)
		}
	}
	return p, nil
}
