package render

import (
	"bytes"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

const tapeRenderScore = `cicada 2
tempo 74
fx echo tape { time = 3/16 feedback = 0.6 wow = 0.5 flutter = 0.2 saturation = 0.4 damp = 3200Hz }
track rim drums { send echo = 0.8 pre }
pattern hit drums { rs: x... .... .... .... }
automate echo.time { @1.1.1 608ms @3.1.1 300ms smooth }
scene main { rim = hit }
song { main*4 }
`

func TestTapeEchoRendersAndStems(t *testing.T) {
	score, ds := notation.Parse([]byte(tapeRenderScore))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if !UsesScheduledEngine(score, Options{}) {
		t.Fatal("tape must render through the engine")
	}
	var wav bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48000, TailSec: 6}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputPeak <= 0 {
		t.Fatal("silent render")
	}
	again := bytes.Buffer{}
	if _, err := WAV(score, Options{SampleRate: 48000, TailSec: 6}, &again); err != nil || !bytes.Equal(wav.Bytes(), again.Bytes()) {
		t.Fatal("tape render is not deterministic", err)
	}
	dir := filepath.Join(t.TempDir(), "stems")
	if _, err := Stems(score, Options{SampleRate: 48000, TailSec: 1}, dir); err != nil {
		t.Fatal(err)
	}
}
