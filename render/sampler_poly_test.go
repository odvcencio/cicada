package render

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestResidentSamplerWithPolyphonicSongMatchesNative(t *testing.T) {
	dir, _ := audioRenderFixture(t, false)
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	// A resident sampler must compile only its own notes. In particular, the
	// unrelated chord pattern must retain ownership by the polyphonic track.
	source := fmt.Sprintf(`cicada 2
tempo 120
asset take "take.wav" { sha256 = "%x" format = wav frames = 120000 rate = 48000Hz channels = 2 }
sampler hit { asset = take root = c3 mode = oneshot voices = 2 }
instrument glass { voice poly { out = sine(pitch) * env(gate, 130ms) * velocity * 0.12 } }
track chops hit { level = -8dB }
track harmony glass { level = -7dB }
pattern hits notes { c3 . g3 . }
pattern chords notes { [c4 eb4 g4] - . [ab3 c4 eb4] }
scene main { chops = hits harmony = chords }
scene hold { chops = keep harmony = keep }
scene quiet { chops = off harmony = off }
song { main hold quiet main }
`, sha256.Sum256(data))
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || hasAudioErrors(diagnostics) {
		t.Fatal(diagnostics)
	}
	checkNativeAudioMatchesWAVAndRange(t, dir, score)
}
