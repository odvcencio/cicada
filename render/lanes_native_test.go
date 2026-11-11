//go:build !tinygo && !js

package render

import (
	"bytes"
	"math"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
)

// laneTestVoice is a deterministic mono voice that turns NaN at frame nanAt
// (never when nanAt is negative), so a drive insert after it faults there.
type laneTestVoice struct {
	phase  uint32
	frame  int
	nanAt  int
	gainOn float32
}

func (v *laneTestVoice) NoteOn(note, velocity uint8, _, _ bool) {
	v.gainOn = float32(velocity) / 127 * float32(note) / 64
}
func (v *laneTestVoice) NoteOff() { v.gainOn *= .5 }
func (v *laneTestVoice) Next() float32 {
	v.frame++
	if v.frame-1 == v.nanAt {
		return float32(math.NaN())
	}
	v.phase = v.phase*1664525 + 1013904223
	return float32(int32(v.phase)) / 2147483648 * v.gainOn * .2
}

// laneTestTracks builds count tracks of laneTestVoice, each with a drive
// insert, as renderBlock's direct mixer sees them.
func laneTestTracks(t *testing.T, count, nanTrack, nanAt int) []trackRuntime {
	t.Helper()
	tracks := make([]trackRuntime, count)
	for i := range tracks {
		voice := &laneTestVoice{phase: uint32(i+1) * 2654435761, nanAt: -1, gainOn: 1}
		if i == nanTrack {
			voice.nanAt = nanAt
		}
		drive, err := fx.NewDrive(48000)
		if err != nil {
			t.Fatal(err)
		}
		params := fx.DefaultDriveParams()
		params.GainDB, params.Shape = 12, fx.DriveShape(i%4)
		if err := drive.SetParams(params); err != nil {
			t.Fatal(err)
		}
		tracks[i] = trackRuntime{name: "t" + string(rune('a'+i)), voice: voice, insert: drive, mixer: mix.NewTrack(-6, float64(i%3-1)*.4, false), sendB: .2}
	}
	return tracks
}

// renderLaneBlocks renders blocks of 1024 frames with note events every 300
// frames and returns the bytes written and the first error.
func renderLaneBlocks(t *testing.T, tracks []trackRuntime, jobs int) ([]byte, error) {
	t.Helper()
	const block = 1024
	limiter, err := mix.NewLimiter(48000)
	if err != nil {
		t.Fatal(err)
	}
	reverb, err := fx.NewReverb(48000)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := newWAVEncoder(32, false, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	state := busMixerState{outputGain: 1}
	if lanes := newTrackLanes(tracks, jobs, block); lanes != nil {
		defer lanes.close()
		state.lanes = lanes
	}
	var out bytes.Buffer
	buffer := make([]byte, block*encoder.frameBytes())
	report := Report{Frames: 1 << 30}
	for b := range 12 {
		start := int64(b * block)
		var events []scheduled
		for at := (start + 299) / 300 * 300; at < start+block; at += 300 {
			for ti := range tracks {
				kind := seq.NoteOn
				if (at/300+int64(ti))%3 == 0 {
					kind = seq.NoteOff
				}
				events = append(events, scheduled{track: ti, event: seq.Event{Kind: kind, Sample: at, Note: uint8(40 + ti), Velocity: 100}})
			}
		}
		if err := renderBlock(&out, tracks, nil, reverb, nil, 0, 0, 0, 1, state, limiter, nil, &encoder, events, start, block, buffer, &report); err != nil {
			return out.Bytes(), err
		}
	}
	return out.Bytes(), nil
}

func TestDirectLanesAreBitIdentical(t *testing.T) {
	defer renderlanes.ForceEveryLane()()
	serial, err := renderLaneBlocks(t, laneTestTracks(t, 6, -1, 0), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(serial) == 0 || bytes.Count(serial, []byte{0}) == len(serial) {
		t.Fatal("serial render is silent")
	}
	for _, jobs := range []int{2, 3, 4, 8} {
		got, err := renderLaneBlocks(t, laneTestTracks(t, 6, -1, 0), jobs)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(serial, got) {
			t.Fatalf("jobs %d: bytes differ from the serial render", jobs)
		}
	}
}

// A fault in one track inside a lane's segment stops the render at the frame
// and with the error the serial loop gives, after the same output.
func TestDirectLanesFaultMatchesSerial(t *testing.T) {
	defer renderlanes.ForceEveryLane()()
	const nanAt = 2500
	serial, serialErr := renderLaneBlocks(t, laneTestTracks(t, 5, 3, nanAt), 1)
	if serialErr == nil {
		t.Fatal("the serial render did not fault")
	}
	for _, jobs := range []int{2, 4} {
		got, err := renderLaneBlocks(t, laneTestTracks(t, 5, 3, nanAt), jobs)
		if err == nil || err.Error() != serialErr.Error() {
			t.Fatalf("jobs %d: error %v, serial %v", jobs, err, serialErr)
		}
		if !bytes.Equal(serial, got) {
			t.Fatalf("jobs %d: output before the fault differs", jobs)
		}
	}
}
