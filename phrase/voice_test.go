package phrase

import (
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestDefaultVoiceIsAKeyboardBass(t *testing.T) {
	if DefaultVoice != "fm_bass" || keyboard.ID(DefaultVoice) == 0 {
		t.Fatalf("default voice %q is not a keyboard patch", DefaultVoice)
	}
	if DefaultParams().Voice != DefaultVoice {
		t.Fatalf("DefaultParams voice = %q", DefaultParams().Voice)
	}
	// An empty voice, as an older caller of the WASM API sends, selects the default.
	params := DefaultParams()
	params.Seed, params.Voice = 4242, ""
	empty, err := Generate(params)
	if err != nil {
		t.Fatal(err)
	}
	params.Voice = DefaultVoice
	named, err := Generate(params)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Notation != named.Notation || !strings.Contains(empty.Notation, "track bass fm_bass {}\n") {
		t.Fatalf("empty voice did not select %s:\n%s", DefaultVoice, empty.Notation)
	}
	if strings.Contains(empty.Notation, "acid") {
		t.Fatalf("default output names the acid voice:\n%s", empty.Notation)
	}
}

func TestVoicesListsTheDefaultFirstAndEveryNameIsValid(t *testing.T) {
	voices := Voices()
	if len(voices) != len(keyboard.Names)+2 || voices[0] != DefaultVoice {
		t.Fatalf("voices = %v", voices)
	}
	// The default comes first, then the piano and the other keyboard patches, and
	// acid is one voice among them, listed last. Studio's selector shows this order.
	if voices[1] != "piano" || voices[len(voices)-1] != "acid" {
		t.Fatalf("voices should list piano second and acid last: %v", voices)
	}
	seen := map[string]bool{}
	for _, voice := range voices {
		if seen[voice] || !validVoice(voice) {
			t.Fatalf("voice %q is duplicated or invalid", voice)
		}
		seen[voice] = true
	}
	for _, want := range append([]string{"acid", "piano"}, keyboard.Names[:]...) {
		if !seen[want] {
			t.Fatalf("voice %q is missing from %v", want, voices)
		}
	}
}

// The voice is the whole difference between two generated scores: the bars, the
// draw trace and every other line stay the same.
func TestVoiceChangesOnlyTheTrackLine(t *testing.T) {
	for _, structure := range []Structure{A, AABA, ABAC} {
		params := DefaultParams()
		params.Seed, params.Scale, params.Structure = 91, Dorian, structure
		reference, err := Generate(params)
		if err != nil {
			t.Fatal(err)
		}
		for _, voice := range Voices() {
			params.Voice = voice
			result, err := Generate(params)
			if err != nil {
				t.Fatalf("voice %s: %v", voice, err)
			}
			want := strings.Replace(reference.Notation, "track bass "+DefaultVoice+" {}\n", "track bass "+voice+" {}\n", 1)
			if result.Notation != want {
				t.Fatalf("voice %s changed more than the track line:\n%s\nwant:\n%s", voice, result.Notation, want)
			}
			if !reflect.DeepEqual(result.Bars, reference.Bars) || !reflect.DeepEqual(result.Trace, reference.Trace) {
				t.Fatalf("voice %s changed the bars or the draw trace", voice)
			}
		}
	}
}

// Every offered voice plays the generator's bars: the source compiles to the
// same patterns the generator returns, in every scale.
func TestEveryVoiceCompilesToTheGeneratorBars(t *testing.T) {
	for _, voice := range Voices() {
		for scale := Minor; scale <= Blues; scale++ {
			for _, structure := range []Structure{AABA, ABAC} {
				for seed := uint64(0); seed < 3; seed++ {
					params := DefaultParams()
					params.Voice, params.Scale, params.Structure, params.Seed = voice, scale, structure, seed
					result, err := Generate(params)
					if err != nil {
						t.Fatalf("voice=%s scale=%d structure=%d seed=%d: %v", voice, scale, structure, seed, err)
					}
					score, diagnostics := notation.Parse([]byte(result.Notation))
					for _, diagnostic := range diagnostics {
						if diagnostic.Severity == "error" {
							t.Fatalf("voice=%s scale=%d seed=%d: %+v\n%s", voice, scale, seed, diagnostic, result.Notation)
						}
					}
					if compiled, diagnostics := project.FromScore(score); compiled == nil {
						t.Fatalf("voice=%s scale=%d seed=%d does not compile: %+v\n%s", voice, scale, seed, diagnostics, result.Notation)
					}
					for index, entry := range score.Song {
						actual, err := compiledScenePattern(score, entry.Scene)
						if err != nil || !reflect.DeepEqual(actual, result.Bars[index]) {
							t.Fatalf("bar %d parity: voice=%s scale=%d structure=%d seed=%d error=%v\n%s", index, voice, scale, structure, seed, err, result.Notation)
						}
					}
				}
			}
		}
	}
}

func TestUnsupportedVoicesAreRefused(t *testing.T) {
	// Percussion models start at octave 4, so they would sound two octaves above
	// the generator's bars; drums, guitar and audio are not note voices here.
	for _, voice := range []string{"drums", "model_bd", "guitar", "audio", "Acid", "fm bass", "nope", " "} {
		params := DefaultParams()
		params.Voice = voice
		if _, err := Generate(params); err == nil || !strings.Contains(err.Error(), "unknown voice") {
			t.Errorf("voice %q: error = %v", voice, err)
		}
	}
	// The message leads with the default patch and names acid last, as the
	// voice list does.
	params := DefaultParams()
	params.Voice = "nope"
	if _, err := Generate(params); err == nil || !strings.Contains(err.Error(), "use a keyboard patch such as fm_bass, piano or acid") {
		t.Errorf("unknown voice message = %v", err)
	}
}

func TestMutateAndEvolveWriteTheNamedVoice(t *testing.T) {
	params := DefaultParams()
	params.Seed, params.Structure, params.Voice = 17, A, "piano"
	base, err := Generate(params)
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := Mutate(params, base.Bars[0], []Op{{Kind: NudgeDegree}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	evolved, err := Evolve(params, base.Bars[0], 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	for name, notation := range map[string]string{"generate": base.Notation, "mutate": mutated.Notation, "evolve": evolved.Notation} {
		if !strings.Contains(notation, "track bass piano {}\n") {
			t.Errorf("%s did not write the piano track:\n%s", name, notation)
		}
	}
}
