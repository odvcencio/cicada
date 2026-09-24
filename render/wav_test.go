package render

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"m31labs.dev/cicada/notation"
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
	if report.Bars != 1 || report.Frames != 96_000 || len(data) != 44+int(report.Frames)*6 || report.Peak < 0.01 {
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
	if bytes.Equal(data[44:], make([]byte, len(data)-44)) {
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
	if report.Peak < .001 || report.Peak > 4 || len(first.Bytes()) != 44+int(report.Frames)*6 {
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
	if report.Peak < .001 || len(first.Bytes()) != 44+int(report.Frames)*6 {
		t.Fatalf("invalid drum render: %+v", report)
	}
}
