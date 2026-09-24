package render

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const testScore = `cicada 1
tempo 120
key a minor
instrument tone {
  voice mono {
    let shape = env(gate, 120ms);
    out = sine(pitch) * shape;
  }
}
track lead tone {}
pattern one notes steps=4 { 1 . . . }
scene main { lead=one }
song { main }
`

func TestWAVDeterministicAndPlayable(t *testing.T) {
	score, ds := notation.Parse([]byte(testScore))
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	var first, second bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000}, &first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(score, Options{SampleRate: 48_000}, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("same score produced different PCM")
	}
	data := first.Bytes()
	if report.Bars != 1 || report.Frames != 96_000 || len(data) != 68+int(report.Frames)*6 || report.Peak < 0.01 {
		t.Fatalf("unexpected report or size: %+v, %d bytes", report, len(data))
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || string(data[36:40]) != "data" {
		t.Fatal("invalid WAV header")
	}
	if got := binary.LittleEndian.Uint32(data[40:44]); got != uint32(report.Frames)*6 {
		t.Fatalf("data chunk is %d bytes", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != 48_000 {
		t.Fatalf("sample rate is %d", got)
	}
	if bytes.Equal(data[44:44+int(report.Frames)*6], make([]byte, int(report.Frames)*6)) {
		t.Fatal("audio is silent")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

func TestWAVRejectsShortWrite(t *testing.T) {
	score, ds := notation.Parse([]byte(testScore))
	if len(ds) != 0 {
		t.Fatalf("score diagnostics: %+v", ds)
	}
	if _, err := WAV(score, Options{}, shortWriter{}); err != io.ErrShortWrite {
		t.Fatalf("want short write, got %v", err)
	}
}

func TestWAVAppliesStepGate(t *testing.T) {
	const source = `cicada 1
tempo 120
key a minor
instrument tone { voice mono { let shape = env(gate, 30000ms); out = sine(pitch) * shape; } }
track lead tone {}
pattern one notes steps=4 gate=50 { 1 . . . }
scene main { lead=one }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	var wav bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000}, &wav); err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	frame := func(index int) []byte { return data[44+index*6 : 44+(index+1)*6] }
	if bytes.Equal(frame(100), make([]byte, 6)) {
		t.Fatal("note onset is silent")
	}
	for _, index := range []int{9000, 10000, 12000} {
		if !bytes.Equal(frame(index), make([]byte, 6)) {
			t.Fatalf("gate remained open at frame %d: %v", index, frame(index))
		}
	}
}

func TestWAVSceneSwitchSlideAndRestGate(t *testing.T) {
	for _, tc := range []struct {
		name, oldFirst, targetFirst string
		wantSlideDifference         bool
	}{
		{name: "carry", oldFirst: ".", targetFirst: "5", wantSlideDifference: true},
		{name: "rest", oldFirst: "1", targetFirst: ".", wantSlideDifference: false},
		{name: "probability miss", oldFirst: "1", targetFirst: "5%1", wantSlideDifference: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldSteps := append([]string{tc.oldFirst}, strings.Fields(strings.Repeat(". ", 14))...)
			oldSteps = append(oldSteps, "1~")
			targetSteps := append([]string{tc.targetFirst}, strings.Fields(strings.Repeat(". ", 15))...)
			source := "cicada 1\ntempo 120\nkey a minor\nseed 42\n" +
				"instrument tone { voice mono { let shape = env(gate, 30000ms); out = sine(pitch) * shape; } }\n" +
				"track lead tone {}\n" +
				"pattern old notes steps=16 gate=55 { " + strings.Join(oldSteps, " ") + " }\n" +
				"pattern next notes steps=16 gate=55 { " + strings.Join(targetSteps, " ") + " }\n" +
				"scene first { lead=old }\nscene second { lead=next }\n" +
				"song { first second }\n"
			score, diagnostics := notation.Parse([]byte(source))
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity != "warning" || diagnostic.Code != "CICADA-SLIDE-REST" || tc.name != "carry" {
					t.Fatalf("score diagnostics: %+v", diagnostics)
				}
			}
			var withSlide bytes.Buffer
			if _, err := WAV(score, Options{SampleRate: 48_000}, &withSlide); err != nil {
				t.Fatal(err)
			}
			reference, diagnostics := notation.Parse([]byte(strings.Replace(source, "1~ }", "1 }", 1)))
			if len(diagnostics) != 0 {
				t.Fatalf("reference diagnostics: %+v", diagnostics)
			}
			var withoutSlide bytes.Buffer
			if _, err := WAV(reference, Options{SampleRate: 48_000}, &withoutSlide); err != nil {
				t.Fatal(err)
			}
			start, end := 44+93_300*6, 44+96_000*6
			different := !bytes.Equal(withSlide.Bytes()[start:end], withoutSlide.Bytes()[start:end])
			if different != tc.wantSlideDifference {
				t.Fatalf("slide changed late first-bar PCM=%v, want %v", different, tc.wantSlideDifference)
			}
			assertLiveSceneBoundaryMatchesWAV(t, score, withSlide.Bytes())
		})
	}
}

func assertLiveSceneBoundaryMatchesWAV(t *testing.T, score *notation.Score, wav []byte) {
	t.Helper()
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project diagnostics: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	live, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !live.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play command rejected")
	}
	limiter, err := mix.NewLimiter(48_000)
	if err != nil {
		t.Fatal(err)
	}
	latency := limiter.LatencyFrames()
	const first, last = 90_000, 99_000
	var left, right [128]float32
	for position := 0; position < last+latency; position += len(left) {
		live.Render(left[:], right[:])
		for i := range left {
			frame := position + i - latency
			if frame < first || frame >= last {
				continue
			}
			for channel, value := range [...]float32{left[i], right[i]} {
				index := 44 + frame*6 + channel*3
				pcm := int32(uint32(wav[index]) | uint32(wav[index+1])<<8 | uint32(wav[index+2])<<16)
				if pcm&(1<<23) != 0 {
					pcm |= ^int32(0xffffff)
				}
				if delta := math.Abs(float64(value) - float64(pcm)/8388607); delta > 2e-7 {
					t.Fatalf("offline/live mismatch at frame %d channel %d: %.8f vs %.8f (delta %.8f)", frame, channel, float64(pcm)/8388607, value, delta)
				}
			}
		}
	}
}

func TestBuiltInAcidRendersDeterministically(t *testing.T) {
	const source = `cicada 1
tempo 130
key a minor
track bass acid { cutoff = 620hz reso = 0.7 envmod = 0.6 decay = 380ms filter = diode }
pattern riff acid steps=16 gate=55 { 1^ . 1~ 5 . 1 7, . | 1^ . 1'~ 1 5^ 3 1 . }
scene main { bass=riff }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	var first, second bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000}, &first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(score, Options{SampleRate: 48_000}, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("built-in acid render changed between runs")
	}
	if report.Peak < .001 || report.Peak > 4 || len(first.Bytes()) != 68+int(report.Frames)*6 {
		t.Fatalf("invalid acid render: report=%+v bytes=%d", report, len(first.Bytes()))
	}
}

func TestBuiltInDrumsRenderDeterministically(t *testing.T) {
	const source = `cicada 1
tempo 120
key a minor
seed 42
track kit drums { bd_tune = 55hz bd_decay = 400ms ch_metal = on }
pattern beat drums steps=4 { bd: X...; sd: .x..; ch: ..x.; oh: x...; cp: .x..; rs: ...x; }
scene main { kit=beat }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	var first, second bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000}, &first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WAV(score, Options{SampleRate: 48_000}, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("same drum score produced different PCM")
	}
	if report.Peak < .001 || len(first.Bytes()) != 68+int(report.Frames)*6 {
		t.Fatalf("invalid drum render: %+v", report)
	}
}

func TestVerifyWAVChecksMusicalDurationAndSignal(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(testScore))
	if len(diagnostics) != 0 {
		t.Fatalf("score diagnostics: %+v", diagnostics)
	}
	var output bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000, Bars: 1, TailSec: .25}, &output)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.wav")
	if err := os.WriteFile(path, output.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	options := VerifyOptions{SampleRate: 48_000, Bits: 24, Bars: 1, TailSec: .25, PeakMaxDB: 0, DCMaxDB: 0}
	verified, err := VerifyWAV(path, options)
	if err != nil || verified.Frames != report.Frames {
		t.Fatalf("verify: %+v, %v", verified, err)
	}
	tooStrict := options
	tooStrict.PeakMaxDB = -80
	if _, err := VerifyWAV(path, tooStrict); err == nil {
		t.Fatal("accepted a peak above the requested ceiling")
	}
	wrongBars := options
	wrongBars.Bars = 2
	if _, err := VerifyWAV(path, wrongBars); err == nil {
		t.Fatal("accepted wrong bar count")
	}
	data := output.Bytes()
	data[44+report.Frames*6+16] = 2 // change the stored bar count
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, options); err == nil {
		t.Fatal("accepted corrupt timing metadata")
	}
	data[44+report.Frames*6+16] = 1
	for frame := 0; frame < 10_000; frame++ {
		copy(data[44+frame*6:], []byte{0xff, 0xff, 0x7f})
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	tooMuchDC := options
	tooMuchDC.DCMaxDB = -60
	if _, err := VerifyWAV(path, tooMuchDC); err == nil {
		t.Fatal("accepted a DC offset above the requested ceiling")
	}
}

func TestDryMixerPansTrackLeft(t *testing.T) {
	source := bytes.Replace([]byte(testScore), []byte("track lead tone {}"), []byte("track lead tone { level = -12db pan = -1 }"), 1)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	var output bytes.Buffer
	_, err := WAV(score, Options{SampleRate: 48_000}, &output)
	if err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	leftAudible := false
	for frame := 0; frame < 10_000; frame++ {
		index := 44 + frame*6
		if data[index] != 0 || data[index+1] != 0 || data[index+2] != 0 {
			leftAudible = true
		}
		if data[index+3] != 0 || data[index+4] != 0 || data[index+5] != 0 {
			t.Fatalf("right channel audible at frame %d", frame)
		}
	}
	if !leftAudible {
		t.Fatal("left channel was silent")
	}
}

func TestMasterLimiterReducesHotSignalWithoutClipping(t *testing.T) {
	source := bytes.Replace([]byte(testScore), []byte("out = sine(pitch) * shape;"), []byte("out = sine(pitch) * shape * 8;"), 1)
	score, diagnostics := notation.Parse(source)
	if len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	var output bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48_000}, &output)
	if err != nil {
		t.Fatal(err)
	}
	ceiling := math.Pow(10, -.3/20)
	if report.Peak <= 1 || report.PreLimiterOvers == 0 || report.ClippedSamples != 0 || float64(report.OutputPeak) > ceiling+1e-6 {
		t.Fatalf("limiter did not contain hot signal: %+v", report)
	}
}
