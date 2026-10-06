package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPianoWAVPreservesStereoAndSustainScenes(t *testing.T) {
	source, err := os.ReadFile("../examples/modeled-piano.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("piano source: %+v", diagnostics)
	}
	var output bytes.Buffer
	dither := false
	_, err = WAV(score, Options{SampleRate: 48_000, Bits: 32, Bars: 3, Block: 128, Dither: &dither}, &output)
	if err != nil {
		t.Fatal(err)
	}
	dataBytes := int(binary.LittleEndian.Uint32(output.Bytes()[40:44]))
	data := output.Bytes()[44 : 44+dataBytes]
	stereo, sounded := false, false
	for at := 0; at+8 <= len(data); at += 8 {
		left := math.Float32frombits(binary.LittleEndian.Uint32(data[at:]))
		right := math.Float32frombits(binary.LittleEndian.Uint32(data[at+4:]))
		if math.IsNaN(float64(left)) || math.IsInf(float64(left), 0) || math.IsNaN(float64(right)) || math.IsInf(float64(right), 0) {
			t.Fatal("piano WAV contains a nonfinite sample")
		}
		sounded = sounded || left != 0 || right != 0
		stereo = stereo || left != right
	}
	if !sounded || !stereo {
		t.Fatal("piano WAV must retain the grand's stereo radiation")
	}
	assertSceneEngineMatchesWAV(t, score, output.Bytes(), 48_000, 0, len(data)/8)
}

func TestPianoChordWAVMatchesKernel(t *testing.T) {
	source := "cicada 2\ntempo 120\ntrack grand piano {}\npattern p notes { [c3 e3 g3] . [c4 e4 g4] . }\nscene dry { grand=p }\nscene pedal { grand=p grand.sustain=1 }\nsong { dry pedal }"
	score, ds := notation.Parse([]byte(source))
	if score == nil || len(ds) != 0 {
		t.Fatalf("piano chord score: %+v", ds)
	}
	var output bytes.Buffer
	dither := false
	report, err := WAV(score, Options{SampleRate: 48_000, Bits: 32, Block: 128, Dither: &dither}, &output)
	if err != nil {
		t.Fatal(err)
	}
	assertSceneEngineMatchesWAV(t, score, output.Bytes(), 48_000, 0, int(report.Frames))
}
