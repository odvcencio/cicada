package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestAuthoredChordReleaseSurvivesScorePlaybackAndExport(t *testing.T) {
	// The first sixteenth ends at 68.75 ms. The authored release must remain
	// audible beyond the old pool's additional 30 ms fade, through both hosts.
	source := `cicada 2
tempo 120
instrument pad { voice poly {
  out = sine(pitch) * adsr(gate, 5ms, 20ms, 0.8, 850ms) * velocity * 0.15
} }
track keys pad {}
pattern chord notes { [c4 eb4 g4] . . . . . . . . . . . . . . . }
scene play { keys = chord }
scene quiet { keys = off }
song { play quiet }
`
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || hasAudioErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	var reference []byte
	for _, block := range []int{31, 128, 4096} {
		var output bytes.Buffer
		report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Block: block}, &output)
		if err != nil {
			t.Fatal(err)
		}
		if reference == nil {
			reference = append([]byte(nil), output.Bytes()...)
			assertSceneEngineMatchesWAV(t, score, reference, 48000, 0, int(report.Frames))
		} else if !bytes.Equal(reference, output.Bytes()) {
			t.Fatalf("authored chord release depends on block size %d", block)
		}
	}
	var energy float64
	for frame := 48000 / 5; frame < 48000*2/5; frame++ {
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(reference[44+frame*8:])))
		energy += value * value
	}
	if rms := math.Sqrt(energy / (48000 / 5)); rms < .001 {
		t.Fatalf("score playback truncated the authored chord release: RMS %g", rms)
	}
	for frame := 48000; frame < 48000*4; frame++ {
		value := math.Float32frombits(binary.LittleEndian.Uint32(reference[44+frame*8:]))
		if math.Abs(float64(value)) > 1e-7 {
			t.Fatalf("finished chord left an audible voice at frame %d: %g", frame, value)
		}
	}
}
