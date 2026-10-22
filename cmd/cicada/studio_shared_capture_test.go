package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
	"m31labs.dev/cicada/smf"
)

func TestStudioSharedCaptureSurvivesWriteReopenEditAndUndo(t *testing.T) {
	testStudioSharedCapture(t, "")
}

func TestStudioChannelCaptureSurvivesWriteReopenEditAndUndo(t *testing.T) {
	testStudioSharedCapture(t, "channel")
}

func testStudioSharedCapture(t *testing.T, kind string) {
	channel, prefix, proofKey := uint8(15), "mpe", "CICADA_SHARED_CAPTURE_PROOF_DIR"
	if kind == "channel" {
		channel, prefix, proofKey = 2, "midi", "CICADA_CHANNEL_CAPTURE_PROOF_DIR"
	}
	source := "tempo 96\ninstrument light { voice poly { out=sine(pitch)*adsr(gate,8ms,130ms,0.6,1800ms)*velocity*(0.04+pressure*0.08) } }\ntrack keys light { level=-12dB }\npattern phrase notes { . . . . } // original idea\nscene main { keys=phrase }\nsong { main }\n"
	_, handler, path := proposalFixture(t, source)
	take := studioEdit{Revision: studioRevision([]byte(source)), SourceRevision: studioProjectReceipt(t, path), Timing: "played", Recordings: []studioTakeRecording{{Track: "keys", Pattern: "phrase", Notes: []studioTakeNote{
		{Tick: 3857, EndTick: 4500, Note: 60, Velocity: 100, Group: 1, Expressions: []studioTakeExpression{{Tick: 3857, PitchCents: 25, Pressure: .2, Timbre: .5}}},
		{Tick: 3859, EndTick: 4500, Note: 64, Velocity: 90, Group: 1},
	}, Groups: []studioTakeGroup{{ID: 7, Device: "native:surface:g0", Channel: channel, Kind: kind, Points: []studioTakeExpression{{Tick: 3857, Pressure: .1, Timbre: .5}, {Tick: 4800, PitchCents: 200, Pressure: .9, Timbre: .8}, {Tick: 8000, Timbre: .5}}}}}}}
	if r := studioCall(t, handler, "/api/record", take); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	saved, _ := os.ReadFile(path)
	p, err := compileStudioSource(path, saved)
	if err != nil {
		t.Fatal(err)
	}
	performance := p.Patterns[0].Performance
	if performance == nil || len(performance.Groups) != 1 || performance.Groups[0].Name != prefix+"-1" || performance.LengthTicks != 7680 || performance.Notes[0].Group != 1 || performance.Notes[0].EndTick != 660 || performance.Notes[0].Expressions[0].Value.PitchCents != 25 || performance.Groups[0].Points[2].Tick != 4160 {
		t.Fatal("record API lost shared or local data", performance)
	}
	receipt, _ := project.ReadScoreRevision(path)
	change := studioEdit{Revision: studioRevision(saved), SourceRevision: receipt, Action: "shared-gesture", Pattern: "phrase", SharedExpression: &edit.SetSharedExpression{GroupIndex: 0, Index: 1, Tick: 960, PitchCents: 150, Pressure: .8, Timbre: .7}}
	if r := studioCall(t, handler, "/api/pattern", change); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	edited, _ := os.ReadFile(path)
	for _, restore := range [][]byte{saved, []byte(source)} {
		if r := studioCall(t, handler, "/api/undo", studioEdit{Revision: studioRevision(edited)}); r.Code != 200 {
			t.Fatal(r.Code, r.Body)
		}
		edited, _ = os.ReadFile(path)
		if !bytes.Equal(edited, restore) {
			t.Fatal("Undo did not restore the exact capture or original idea")
		}
	}
	if r := studioCall(t, handler, "/api/redo", studioEdit{Revision: studioRevision(edited)}); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	reopened, _ := os.ReadFile(path)
	if !bytes.Equal(reopened, saved) {
		t.Fatal("Redo changed the shared performance")
	}
	if dir := os.Getenv(proofKey); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "captured-shared.cicada"), saved, 0600); err != nil {
			t.Fatal(err)
		}
	}
	decode := func(data []byte) *notation.Score {
		t.Helper()
		score, ds := notation.Parse(data)
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		return score
	}
	var reference []byte
	for _, block := range []int{127, 4096} {
		var wav bytes.Buffer
		report, err := render.WAV(decode(saved), render.Options{Bits: 32, SampleRate: 48000, Block: block, TailSec: .1}, &wav)
		if err != nil || report.OutputPeak < .001 || report.OutputPeak >= 1 || report.ClippedSamples != 0 {
			t.Fatal("captured performance did not render safely", report, err)
		}
		if reference == nil {
			reference = bytes.Clone(wav.Bytes())
		} else if !bytes.Equal(reference, wav.Bytes()) {
			t.Fatal("shared capture changed with export block size")
		}
	}
	baseline := decode(saved)
	baseline.Patterns[0].Performance.Groups[0].Points = baseline.Patterns[0].Performance.Groups[0].Points[:1]
	var neutral bytes.Buffer
	if _, err := render.WAV(baseline, render.Options{Bits: 32, SampleRate: 48000, Block: 127, TailSec: .1}, &neutral); err != nil {
		t.Fatal(err)
	}
	first := -1
	difference := 0.0
	frames := int(binary.LittleEndian.Uint32(reference[40:44])) / 8
	for frame := 0; frame < frames; frame++ {
		for channel := range 2 {
			offset := 44 + frame*8 + channel*4
			a := math.Float32frombits(binary.LittleEndian.Uint32(reference[offset:]))
			b := math.Float32frombits(binary.LittleEndian.Uint32(neutral.Bytes()[offset:]))
			delta := math.Abs(float64(a - b))
			if delta > .000001 && first < 0 {
				first = frame
			}
			if frame < 48000*2 {
				difference = max(difference, delta)
			}
		}
	}
	gateFrame := int(660 * 48000 * 60 / (96 * 960))
	if first <= gateFrame || first >= 48000*2 || difference < .001 {
		t.Fatalf("master movement did not audibly shape released voices: first=%d gate=%d delta=%g", first, gateFrame, difference)
	}
	t.Logf("first_shared_tail_difference_frame=%d released_gate_frame=%d peak_tail_delta=%.9f", first, gateFrame, difference)
	dir := filepath.Join(t.TempDir(), "stems")
	if _, err := render.Stems(decode(saved), render.Options{Bits: 32, Block: 127, TailSec: .1}, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := render.VerifyStems(nil, dir, render.VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatal(err)
	}
	midi, err := smf.FromProject(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	var midiBytes bytes.Buffer
	if err := smf.Encode(midi, &midiBytes); err != nil {
		t.Fatal(err)
	}
	decoded, err := smf.Decode(bytes.NewReader(midiBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if err := smf.MusicalDiff(midi, decoded); err != nil {
		t.Fatal("MIDI export changed captured notes or shared controls", err)
	}
	factor := int64(decoded.PPQ) / seq.PPQ
	masterAfterRelease := false
	for _, track := range decoded.Tracks {
		for _, event := range track.Events {
			if event.Tick == 960*factor && event.Status == 0xd0 && event.Data1 == 114 {
				masterAfterRelease = true
			}
		}
	}
	if !masterAfterRelease {
		t.Fatal("MIDI bytes omitted shared pressure after the note release")
	}
	if dir := os.Getenv(proofKey); dir != "" {
		var wav bytes.Buffer
		if _, err := render.WAV(decode(saved), render.Options{Bits: 24, SampleRate: 48000, Block: 127, TailSec: .1}, &wav); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "captured-shared.wav"), wav.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "captured-shared.mid"), midiBytes.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
