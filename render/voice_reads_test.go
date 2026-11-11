package render

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

// TestVoiceReadsMatchesTheEngine renders a pattern with and without each
// per-step row and mark, on every kind of melodic voice, and requires the
// result to agree with notation.VoiceReads: a row the voice reads changes the
// render, a row it ignores leaves it byte-identical, and a row the voice
// refuses fails to compile.
func TestVoiceReadsMatchesTheEngine(t *testing.T) {
	dir := t.TempDir()
	dir, _ = audioRenderFixture(t, false)
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	asset := fmt.Sprintf("asset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\n", sha256.Sum256(data))
	voices := []struct{ name, declaration, kind string }{
		{"acid", "", "acid"},
		{"piano", "", "piano"},
		{"keyboard", "", "tine_ep"},
		{"modal", "", "model_marimba"},
		{"guitar", "", "guitar"},
		{"sampler", asset + "sampler hit { asset = take root = c3 mode = oneshot voices = 4 }\n", "hit"},
		{"mono graph", "instrument g { voice mono { out = saw(pitch) * env(gate, 200ms) } }\n", "g"},
		{"poly graph", "instrument g { voice poly { out = saw(pitch) * env(gate, 200ms) } }\n", "g"},
		{"expressive graph", "instrument g { voice mono { out = mix(sine(pitch), saw(pitch), timbre) * env(gate, 200ms) * velocity * (0.5 + pressure * 0.5) } }\n", "g"},
	}
	base := "a2 c3 d3 e3 a2 c3 d3 e3"
	variants := map[string][2]string{
		notation.InputAccent:   {"a2^ c3 d3 e3 a2^ c3 d3 e3", ""},
		notation.InputSlide:    {"a2~ c3 d3~ e3 a2~ c3 d3~ e3", ""},
		notation.InputRatchet:  {"a2*2 c3 d3 e3 a2*3 c3 d3 e3", ""},
		notation.InputChance:   {"a2?30 c3?30 d3?30 e3?30 a2?30 c3?30 d3?30 e3?30", ""},
		notation.InputVelocity: {base, "velocity: 20 127 20 127 20 127 20 127"},
		notation.InputPressure: {base, "pressure: 1 0 1 0 1 0 1 0"},
		notation.InputTimbre:   {base, "timbre: 1 0 1 0 1 0 1 0"},
		notation.InputBend:     {base, "bend: 200ct 0ct 200ct 0ct 200ct 0ct 200ct 0ct"},
		notation.InputVibrato:  {base, "vibrato: 80ct 80ct 80ct 80ct 80ct 80ct 80ct 80ct"},
	}
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 16, Bars: 1, TailSec: 0.2, Block: 512}
	for _, voice := range voices {
		track := "track t " + voice.kind + " {}\n"
		if voice.kind == "guitar" {
			track = "track t guitar { experimental = on }\n"
		}
		source := func(steps, row string) string {
			body := steps
			if row != "" {
				body += "\n " + row
			}
			return "cicada 2\ntempo 120\nseed 3\n" + voice.declaration + track + "pattern p { " + body + " }\nscene main { t = p }\nsong { main }\n"
		}
		render := func(text string) ([]byte, error) {
			score, ds := notation.Parse([]byte(text))
			for _, d := range ds {
				if d.Severity == "error" {
					return nil, fmt.Errorf("%s", d.Message)
				}
			}
			var out bytes.Buffer
			_, err := WAV(score, opts, &out)
			return out.Bytes(), err
		}
		plain, err := render(source(base, ""))
		if err != nil {
			t.Fatalf("%s: %v", voice.name, err)
		}
		score, _ := notation.Parse([]byte(source(base, "")))
		reads := notation.VoiceReads(score, voice.kind)
		if reads == nil {
			t.Fatalf("%s: no description", voice.name)
		}
		for _, input := range notation.StepInputs {
			with, err := render(source(variants[input][0], variants[input][1]))
			read, described := reads[input]
			switch {
			case !described:
				if err == nil {
					t.Errorf("%s %s: described as refused, but it renders", voice.name, input)
				}
			case err != nil:
				t.Errorf("%s %s: %v", voice.name, input, err)
			case read && bytes.Equal(with, plain):
				t.Errorf("%s %s: described as read, but the render does not change", voice.name, input)
			case !read && !bytes.Equal(with, plain):
				t.Errorf("%s %s: described as ignored, but the render changes", voice.name, input)
			}
		}
	}
}
