package render_test

import (
	"bytes"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

const liveTone = "cicada 2\ntempo 120\ninstrument tone {\n  param cutoff = 300Hz\n  voice mono { out = lowpass(saw(pitch), cutoff) * env(gate, 400ms) * 0.4 }\n}\ntrack lead tone {}\npattern line { c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 c3 }\n"

func renderSource(t *testing.T, source string) []byte {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("%+v", ds)
	}
	var wav bytes.Buffer
	if _, err := render.WAV(score, render.Options{SampleRate: 48000, Bits: 32, Block: 256, Bars: 2}, &wav); err != nil {
		t.Fatal(err)
	}
	return wav.Bytes()
}

// A scene setting or automation lane on an authored parameter changes the
// sound; the render goes through the engine, which drives the parameter.
func TestSceneAndAutomationMoveAnAuthoredParameter(t *testing.T) {
	static := renderSource(t, liveTone+"scene main { lead = line }\nsong { main*2 }\n")
	scene := renderSource(t, liveTone+"scene main { lead = line }\nscene bright { lead = line lead.cutoff = 4000Hz }\nsong { main bright }\n")
	lane := renderSource(t, liveTone+"scene main { lead = line }\nsong { main*2 }\nautomate lead.cutoff {\n  @1.1.1 300Hz\n  @2.4.4 4000Hz\n}\n")
	if bytes.Equal(static, scene) || bytes.Equal(static, lane) {
		t.Fatal("moving the authored cutoff did not change the render")
	}
	score, _ := notation.Parse([]byte(liveTone + "scene main { lead = line }\nsong { main*2 }\nautomate lead.cutoff {\n  @1.1.1 300Hz\n}\n"))
	if !render.UsesScheduledEngine(score, render.Options{}) {
		t.Fatal("a score that automates an authored parameter does not use the engine")
	}
	if !strings.Contains(string(static[:4]), "RIFF") {
		t.Fatal("not a WAV")
	}
}

// A track that plays a preset of an authored instrument can move the
// instrument's parameter; the render goes through the engine.
func TestAPresetTrackMovesItsInstrumentParameter(t *testing.T) {
	preset := strings.Replace(liveTone, "track lead tone {}", "preset soft { instrument = tone cutoff = 600Hz }\ntrack lead soft {}", 1)
	static := renderSource(t, preset+"scene main { lead = line }\nsong { main*2 }\n")
	moved := renderSource(t, preset+"scene main { lead = line }\nscene bright { lead = line lead.cutoff = 4000Hz }\nsong { main bright }\n")
	if bytes.Equal(static, moved) {
		t.Fatal("the scene setting on a preset track did not change the render")
	}
}
