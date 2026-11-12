//go:build !tinygo && !js

package render

import (
	"bytes"
	"math"
	"runtime"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// A caller that leaves Options.Jobs at zero, as Studio's render checks, the
// LSP intents and the audition library do, gets the serial render: no lanes,
// even with every segment forced onto them, and the bytes of Jobs 1.
func TestZeroJobsRendersSerially(t *testing.T) {
	defer renderlanes.ForceEveryLane()()
	dir, clipScore := audioRenderFixture(t, true)
	chorus, _ := driftScore(t, "cicada-chorus")
	for _, c := range []struct {
		name  string
		score *notation.Score
		opts  Options
	}{
		{"scheduled engine", clipScore, Options{AssetDir: dir, SampleRate: 48000, Bits: 32}},
		{"direct renderer", chorus, Options{SampleRate: 48000, Bits: 24, Bars: 2}},
	} {
		if scheduled := UsesScheduledEngine(c.score, c.opts); scheduled != (c.name == "scheduled engine") {
			t.Fatalf("%s: UsesScheduledEngine is %t", c.name, scheduled)
		}
		var zero, one bytes.Buffer
		report, err := WAV(c.score, c.opts, &zero)
		if err != nil {
			t.Fatal(err)
		}
		if report.Jobs != 1 || report.Lanes != 1 || report.ParallelSegments != 0 || report.SerialReason != SerialJobs {
			t.Fatalf("%s: Jobs zero reported %+v, want the serial render", c.name, report.LaneReport)
		}
		c.opts.Jobs = 1
		if _, err := WAV(c.score, c.opts, &one); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(zero.Bytes(), one.Bytes()) {
			t.Fatalf("%s: Jobs zero and Jobs 1 wrote different bytes", c.name)
		}
	}
}

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
	lanes := newTrackLanes(tracks, jobs, block)
	if lanes != nil {
		defer lanes.close()
		state.lanes = lanes
		defer func() {
			// With every segment forced onto the lanes, a run that never used
			// two of them would pass without testing them.
			if counts := lanes.counts(); runtime.GOMAXPROCS(0) > 1 && counts.parallel == 0 {
				t.Errorf("jobs %d: no segment ran on two or more lanes (%+v)", jobs, counts)
			}
		}()
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

// An audio clip track reads the transport's playing state while the render
// goroutine runs the control of later frames; the scheduled engine must give
// the serial bytes with the clip on any lane. Under the race detector this
// also checks that nothing the control path writes inside a segment is read
// by a lane.
func TestAudioClipLanesAreBitIdentical(t *testing.T) {
	defer renderlanes.ForceEveryLane()()
	dir, score := audioRenderFixture(t, true)
	if !UsesScheduledEngine(score, Options{}) {
		t.Fatal("the audio fixture must render on the scheduled engine")
	}
	render := func(jobs int) []byte {
		var out bytes.Buffer
		report, err := WAV(score, Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Jobs: jobs}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if want := min(jobs, 2); report.Lanes != want {
			t.Fatalf("jobs %d set up %d lanes, want %d", jobs, report.Lanes, want)
		}
		if jobs > 1 && runtime.GOMAXPROCS(0) > 1 && report.ParallelSegments == 0 {
			t.Fatalf("jobs %d: the forced lanes never ran: %+v", jobs, report.LaneReport)
		}
		return out.Bytes()
	}
	serial := render(1)
	for _, jobs := range []int{2, 2, 2} {
		if !bytes.Equal(serial, render(jobs)) {
			t.Fatalf("jobs %d: bytes differ from the serial render", jobs)
		}
	}
}
