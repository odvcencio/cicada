package edit_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

func TestPatternStartersFollowSongKeyAndTrackRegister(t *testing.T) {
	for _, scale := range []struct {
		name           string
		third, seventh int
	}{
		{"minor", 3, 10}, {"major", 4, 11}, {"dorian", 3, 10}, {"phrygian", 3, 10},
		{"harmonic", 3, 11}, {"mixo", 4, 10}, {"pent", 3, 10}, {"blues", 3, 10},
	} {
		for _, starter := range []string{"pulse", "arpeggio", "chords"} {
			t.Run(scale.name+"/"+starter, func(t *testing.T) {
				source := []byte(fmt.Sprintf("cicada 2\ntitle \"Keep my song\"\ntempo 137\nkey f# %s\ninstrument tone { octave=2 voice poly { out=sine(pitch)*env(gate, 300ms)*0.1 } }\ntrack keys tone { octave=4 }\npattern original notes steps=4 { 1 . . . }\nscene main { keys=off }\nsong { main*4 }\n// Keep the ending\n", scale.name))
				result := chordEdit(t, source, &edit.CreatePattern{Name: "idea", Track: "keys", Steps: 32, Scene: "main", Starter: starter})
				p := result.Plan.Patterns[1]
				if scale.name == "minor" {
					retainStarterAudioFixture(t, "melodic-"+starter, result.Source)
				}
				if p.Kind != "notes" || p.Steps != 32 || result.Plan.TempoMilli != 137000 || result.Plan.Scale != scale.name || result.Plan.Scenes[0].Bindings["keys"] != "idea" {
					t.Fatal("starting idea changed the song context or length")
				}
				if !bytes.HasPrefix(result.Source, bytes.Replace(source, []byte("keys=off"), []byte("keys=idea"), 1)) {
					t.Fatal("creation rewrote unrelated score bytes")
				}
				switch starter {
				case "pulse":
					if p.Data[0].Note != 66 || p.Data[1] != nil || p.Data[16].Note != 66 {
						t.Fatal("pulse lost its root or register")
					}
				case "arpeggio":
					for step, pitch := range map[int]int{0: 66, 2: 66 + scale.third, 4: 73, 6: 66 + scale.seventh, 8: 78, 16: 66} {
						if int(p.Data[step].Note) != pitch {
							t.Fatalf("step %d = %d, want %d", step, p.Data[step].Note, pitch)
						}
					}
				case "chords":
					if !reflect.DeepEqual(p.Data[0].Notes, []int{66, 66 + scale.third, 73}) || !p.Data[7].Tie || !reflect.DeepEqual(p.Data[8].Notes, []int{66 + scale.third, 73, 66 + scale.seventh}) || !p.Data[31].Tie {
						t.Fatal("voicing or held duration changed")
					}
				}
			})
		}
	}
}

func TestPatternStarterPartialBarsAndDrumKits(t *testing.T) {
	for _, steps := range []int{1, 7, 16, 63, 64} {
		for _, starter := range []string{"empty", "pulse", "arpeggio", "chords"} {
			result := chordEdit(t, []byte(editableChord), &edit.CreatePattern{Name: "idea", Track: "keys", Steps: steps, Starter: starter})
			if len(result.Plan.Patterns[1].Data) != steps {
				t.Fatalf("%s length %d was padded or shortened", starter, steps)
			}
		}
	}
	for _, kit := range []struct{ name, source, lane, rhythm string }{
		{"builtin", "track target drums {}", "bd", "x.......x......."},
		{"named", "kit taps { bd=builtin.bd sd=builtin.sd ch=builtin.ch }\ntrack target taps {}", "sd", "....x.......x..."},
		{"custom", "kit taps { oh=builtin.ch }\ntrack target taps {}", "oh", "x...x...x...x..."},
	} {
		t.Run(kit.name, func(t *testing.T) {
			source := []byte("cicada 2\n" + kit.source + "\npattern original notes { c4 . }\nscene main { target=off }\nsong { main }\n")
			result := chordEdit(t, source, &edit.CreatePattern{Name: "idea", Track: "target", Steps: 16, Starter: "backbeat", Scene: "main"})
			retainStarterAudioFixture(t, "drums-"+kit.name, result.Source)
			if !bytes.Contains(result.Source, []byte(kit.lane+": "+kit.rhythm)) {
				t.Fatalf("backbeat does not fit kit:\n%s", result.Source)
			}
			if result.Plan.Patterns[1].Lanes[kit.lane][0] == nil && kit.lane != "sd" {
				t.Fatal("drum starter is silent")
			}
		})
	}
}

func retainStarterAudioFixture(t *testing.T, name string, source []byte) {
	t.Helper()
	if dir := os.Getenv("CICADA_PATTERN_PROOF_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".cicada"), source, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPatternStartersRefuseIncompatibleSoundsAtomically(t *testing.T) {
	source := []byte("cicada 2\ninstrument lowvoice { voice mono { out=sine(pitch) } }\ntrack low lowvoice {}\ntrack bass acid {}\ntrack drums drums {}\npattern original notes { c4 . }\nscene main {}\nsong { main }\n")
	if _, err := (gridCompiler{}).Compile(source, nil); err != nil {
		t.Fatal(err)
	}
	for _, in := range []edit.CreatePattern{
		{Name: "idea", Track: "low", Steps: 16, Starter: "chords"},
		{Name: "idea", Track: "bass", Steps: 16, Starter: "chords"},
		{Name: "idea", Track: "low", Steps: 16, Starter: "backbeat"},
		{Name: "idea", Track: "drums", Steps: 16, Starter: "arpeggio"},
		{Name: "idea", Track: "drums", Steps: 16, Starter: "chords"},
		{Name: "idea", Track: "bass", Steps: 16, Starter: "unexpected"},
	} {
		before := bytes.Clone(source)
		result, err := edit.Apply(source, edit.Envelope{Version: 1, Intents: []edit.Intent{&in}}, edit.Options{Compiler: gridCompiler{}})
		if err == nil || result != nil || !bytes.Equal(source, before) {
			t.Fatalf("invalid starting idea mutated the score: %+v, %v", in, err)
		}
	}
	legacy := []byte(strings.Replace(string(source), "cicada 2", "cicada 1", 1))
	for _, starter := range []string{"pulse", "arpeggio"} {
		chordEdit(t, legacy, &edit.CreatePattern{Name: "idea", Track: "bass", Steps: 16, Starter: starter})
	}
}
