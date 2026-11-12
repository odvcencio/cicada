package render

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// TestVoiceInputsMatchTheEngine renders every voice with and without every
// input, through the direct mixer and through the scheduled engine (one lfo
// routed to the track forces it), and requires the result to agree with
// notation.VoiceInputs. A voice plays an input when either path's render
// changes; it ignores it when both stay byte-identical; it refuses it when
// the score does not compile. Brackets also have their meaning checked: a
// chord voice compiles them to one step of several notes, acid to a tuplet.
func TestVoiceInputsMatchTheEngine(t *testing.T) {
	started := time.Now()
	dir, _ := audioRenderFixture(t, false)
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	asset := fmt.Sprintf("asset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\n", sha256.Sum256(data))
	expressive := "out = mix(sine(pitch), saw(pitch), timbre) * env(gate, 200ms) * velocity * (0.5 + pressure * 0.5)"
	voices := []struct{ name, declaration, kind string }{
		{"acid", "", "acid"},
		{"piano", "", "piano"},
		{"keyboard", "", "tine_ep"},
		{"modal", "", "model_marimba"},
		{"guitar", "", "guitar"},
		{"sampler", asset + "sampler hit { asset = take root = c3 mode = oneshot voices = 4 }\n", "hit"},
		{"mono graph", "instrument g { voice mono { out = saw(pitch) * env(gate, 200ms) } }\n", "g"},
		{"mono expressive graph", "instrument g { voice mono { " + expressive + " } }\n", "g"},
		{"poly graph", "instrument g { voice poly { out = saw(pitch) * env(gate, 200ms) } }\n", "g"},
		{"poly expressive graph", "instrument g { voice poly { " + expressive + " } }\n", "g"},
		{"drums", "", "drums"},
		{"kit", "kit k { bd = builtin.bd sd = builtin.sd }\n", "k"},
		{"modeled kit", "kit k { bd = model.kick sd = model.snare }\n", "k"},
		// A lane bound to an instrument plays a hit as a note: velocity and
		// humanize reach it only through a velocity node, and gate through
		// gate. One kit reads neither, one reads both.
		{"graph kit", "instrument thump { voice mono { out = sine(pitch) * env(gate, 120ms) } }\nkit k { bd = thump sd = thump }\n", "k"},
		{"graph kit reading velocity", "instrument thump { voice mono { out = sine(pitch) * env(gate, 120ms) * velocity } }\nkit k { bd = thump sd = thump }\n", "k"},
	}
	base := "a2 c3 d3 e3 a2 c3 d3 e3"
	melodic := map[string]string{
		notation.InputAccent:      "a2^ c3 d3 e3 a2^ c3 d3 e3",
		notation.InputSlide:       "a2~ c3 d3~ e3 a2~ c3 d3~ e3",
		notation.InputRatchet:     "a2*2 c3 d3 e3 a2*3 c3 d3 e3",
		notation.InputChance:      "a2?30 c3?30 d3?30 e3?30 a2?30 c3?30 d3?30 e3?30",
		notation.InputBrackets:    "[a2 c3 e3] c3 d3 e3 [a2 c3 e3] c3 d3 e3",
		notation.InputVelocityRow: base + "\n velocity: 20 127 20 127 20 127 20 127",
		notation.InputPressureRow: base + "\n pressure: 1 0 1 0 1 0 1 0",
		notation.InputTimbreRow:   base + "\n timbre: 1 0 1 0 1 0 1 0",
		notation.InputBendRow:     base + "\n bend: 200ct 0ct 200ct 0ct 200ct 0ct 200ct 0ct",
		notation.InputVibratoRow:  base + "\n vibrato: 80ct 80ct 80ct 80ct 80ct 80ct 80ct 80ct",
		notation.InputSwing:       "swing = 62% " + base,
		notation.InputGate:        "gate = 25% " + base,
		notation.InputTranspose:   "transpose = 5 " + base,
		notation.InputHumanize:    "humanize = 9 " + base,
	}
	performed := "note 45 at=0ticks length=900ticks velocity=100 { gesture at=0ticks bend=0ct pressure=0.5 timbre=0.5; gesture at=400ticks bend=0ct pressure=0.5 timbre=0.5; } note 48 at=1920ticks length=900ticks velocity=100;"
	performances := map[string]string{
		notation.InputPerformedVelocity: strings.Replace(performed, "velocity=100 {", "velocity=20 {", 1),
		notation.InputGestureBend:       strings.Replace(performed, "at=400ticks bend=0ct", "at=400ticks bend=300ct", 1),
		notation.InputGesturePressure:   strings.Replace(performed, "at=400ticks bend=0ct pressure=0.5", "at=400ticks bend=0ct pressure=1", 1),
		notation.InputGestureTimbre:     strings.Replace(performed, "pressure=0.5 timbre=0.5; }", "pressure=0.5 timbre=1; }", 1),
		notation.InputGestureVibrato:    strings.Replace(performed, "pressure=0.5 timbre=0.5; }", "pressure=0.5 timbre=0.5 vibrato=80ct rate=6Hz; }", 1),
	}
	beat := "bd: x . . . x . . . sd: . . x . . . x ."
	drums := map[string]string{
		notation.InputHitAccent:   "bd: X . . . x . . . sd: . . x . . . x .",
		notation.InputHitVelocity: "bd: x3 . . . x . . . sd: . . x . . . x .",
		notation.InputRatchet:     "bd: x*2 . . . x . . . sd: . . x . . . x .",
		notation.InputChance:      "bd: x?30 . . . x?30 . . . sd: . . x . . . x .",
		notation.InputSwing:       "swing = 62% bd: x . x . x . x . sd: . . x . . . x .",
		notation.InputHumanize:    "humanize = 9 " + beat,
		notation.InputGate:        "gate = 25% " + beat,
		notation.InputTranspose:   "transpose = 0 " + beat,
	}
	opts := Options{AssetDir: dir, SampleRate: 44100, Bits: 16, Bars: 1, TailSec: 0.05, Block: 512}
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
	engine := "lfo drift { wave = triangle rate = 8bars }\nmod drift -> t.pan depth = 0.5\n"
	t.Cleanup(func() {
		t.Logf("%d voices, every input, both renderers, in %s", len(voices), time.Since(started).Round(time.Millisecond))
	})
	for _, voice := range voices {
		t.Run(voice.name, func(t *testing.T) {
			t.Parallel()
			track := "track t " + voice.kind + " {}\n"
			if voice.kind == "guitar" {
				track = "track t guitar { experimental = on }\n"
			}
			head := "cicada 2\ntempo 120\nseed 3\n" + voice.declaration + track
			plain, _ := notation.Parse([]byte(head + "pattern p { a2 }\nscene main { t = p }\nsong { main }\n"))
			table := notation.VoiceInputs(plain, voice.kind)
			if table == nil {
				t.Fatalf("%s: no description", voice.name)
			}
			cases := []struct {
				inputs   []string
				kind     string
				base     string
				variants map[string]string
			}{
				{notation.MelodicInputs, "", base, melodic},
				{notation.PerformedInputs, "performance length=3840ticks", performed, performances},
			}
			if voice.kind == "drums" || voice.kind == "k" {
				cases = []struct {
					inputs   []string
					kind     string
					base     string
					variants map[string]string
				}{{notation.DrumInputs, "drums", beat, drums}}
			}
			for _, c := range cases {
				for _, input := range c.inputs {
					effect, described := table[input]
					if !described {
						t.Errorf("%s: %s is not described", voice.name, input)
						continue
					}
					changed, refused := false, false
					for _, extra := range []string{"", engine} {
						score := func(body string) string {
							return head + extra + "pattern p " + c.kind + " { " + body + " }\nscene main { t = p }\nsong { main }\n"
						}
						without, err := render(score(c.base))
						if err != nil {
							refused = true
							continue
						}
						with, err := render(score(c.variants[input]))
						if err != nil {
							refused = true
							continue
						}
						changed = changed || !bytes.Equal(with, without)
					}
					switch {
					case effect == notation.EffectRefuses:
						if !refused {
							t.Errorf("%s %s: described as refused, but it renders", voice.name, input)
						}
					case refused:
						t.Errorf("%s %s: described as %q, but it does not compile", voice.name, input, effect)
					case effect == notation.EffectIgnores && changed:
						t.Errorf("%s %s: described as ignored, but a render changes", voice.name, input)
					case effect != notation.EffectIgnores && !changed:
						t.Errorf("%s %s: described as %q, but neither render changes", voice.name, input, effect)
					}
					if input == notation.InputBrackets && (effect == notation.EffectChord || effect == notation.EffectTuplet) {
						score, _ := notation.Parse([]byte(head + "pattern p { " + c.variants[input] + " }\nscene main { t = p }\nsong { main }\n"))
						compiled, _ := project.FromScore(score)
						chord := compiled != nil && len(compiled.Patterns) > 0 && compiled.Patterns[0].Data[0] != nil && len(compiled.Patterns[0].Data[0].Notes) > 1
						if chord != (effect == notation.EffectChord) {
							t.Errorf("%s brackets: described as %q, but the compiled step is a chord: %v", voice.name, effect, chord)
						}
					}
				}
			}
		})
	}
}
