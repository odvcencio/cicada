package render

import (
	"bytes"
	"fmt"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestQualityInstrumentOfflineNativeAndSourceRoundTrip(t *testing.T) {
	for _, id := range []string{"poly-brass", "round-bass"} {
		patch, _ := instrument.FindPatch(id)
		declaration, err := patch.Source("voice")
		if err != nil {
			t.Fatal(err)
		}
		score, ds := notation.Parse([]byte("cicada 1\ntempo 120\n" + declaration + "track keys voice {}\npattern p notes gate=70 { c4 . e4 g4~ a4 . c5 . }\nscene main { keys=p }\nsong { main }\n"))
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		semantic, ds := project.FromScore(score)
		if semantic == nil {
			t.Fatal(ds)
		}
		text, err := project.ToSource(semantic)
		if err != nil {
			t.Fatal(err)
		}
		reparsed, ds := notation.Parse(text)
		if len(ds) != 0 {
			t.Fatal(ds)
		}
		for _, rate := range []int{44100, 48000, 96000} {
			t.Run(fmt.Sprintf("%s/%d", id, rate), func(t *testing.T) {
				var original, roundTrip bytes.Buffer
				options := Options{SampleRate: rate, Bits: 32, TailSec: 0.4}
				if _, err := WAV(score, options, &original); err != nil {
					t.Fatal(err)
				}
				if _, err := WAV(reparsed, options, &roundTrip); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(original.Bytes(), roundTrip.Bytes()) {
					t.Fatal("source round trip changed the instrument sound")
				}
				assertSceneEngineMatchesWAV(t, score, original.Bytes(), rate, 0, rate)
			})
		}
	}
}
