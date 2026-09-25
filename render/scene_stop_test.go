package render

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestOfflineSceneStopMatchesLegacyOff(t *testing.T) {
	legacy := "track bass acid {}\npattern riff acid { 1 . 5 . }\nscene main { bass = riff }\nscene quiet { bass = off }\nsong { main quiet }\n"
	render := func(source string) []byte {
		t.Helper()
		score, diagnostics := notation.Parse([]byte(source))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("parse: %+v", diagnostic)
			}
		}
		var output bytes.Buffer
		if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 24}, &output); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	before := render(legacy)
	after := render(strings.Replace(legacy, "bass = off", "bass = stop", 1))
	if !bytes.Equal(before, after) {
		t.Fatal("scene stop changed offline audio")
	}
	patternNamedStop := strings.Replace(legacy, "pattern riff acid", "pattern stop acid", 1)
	patternNamedStop = strings.ReplaceAll(patternNamedStop, "bass = riff", "bass = stop")
	if !bytes.Equal(before, render(patternNamedStop)) {
		t.Fatal("legacy pattern named stop changed offline audio")
	}
}
