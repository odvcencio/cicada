package render

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestSFXBusSidechainMatchesItsOnlyTrack(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "sfx-bus.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	render := func(source []byte) []byte {
		t.Helper()
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("source: %+v", diagnostic)
			}
		}
		var output bytes.Buffer
		if _, err := WAV(score, Options{SampleRate: 48_000, Bars: 1, TailSec: 1}, &output); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	sfx := render(source)
	track := render(bytes.Replace(source, []byte("sidechain = sfx"), []byte("sidechain = beat"), 1))
	if !bytes.Equal(sfx, track) {
		t.Fatal("SFX bus sidechain differed from its sole post-fader track")
	}
	music := render(bytes.Replace(source, []byte("bus = sfx"), []byte("bus = music"), 1))
	if bytes.Equal(sfx, music) {
		t.Fatal("routing the drum track through SFX did not change the render")
	}
	withoutReturn := render(bytes.Replace(source, []byte("send_b = 0.2"), []byte("send_b = 0"), 1))
	if bytes.Equal(sfx, withoutReturn) {
		t.Fatal("SFX track send did not reach the music return")
	}
}
