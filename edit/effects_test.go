package edit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const effectScore = `cicada 2
tempo 120
// the bass line
track bass acid { cutoff = 620Hz }
track keys piano {}
pattern riff { a1 . c2 . }
scene main { bass = riff }
song { main*2 }
`

func applyEffectIntent(t *testing.T, source string, intent Intent) (string, error) {
	t.Helper()
	result, err := Apply([]byte(source), Envelope{Version: 1, Intents: []Intent{intent}}, Options{Compiler: parseCompiler{}})
	if err != nil {
		return "", err
	}
	return string(result.Source), nil
}

// TestAddEffectRefusesWithReasons checks that each route the language does
// not allow is refused with a reason code, the field at fault and a message
// that names the real reason.
func TestAddEffectRefusesWithReasons(t *testing.T) {
	eight := strings.Replace(effectScore, "track keys piano {}", "fx a drive {}\nfx b drive {}\nfx c drive {}\nfx d drive {}\nfx e drive {}\nfx f drive {}\nfx g drive {}\nfx h drive {}\ntrack keys piano { insert = a -> b -> c -> d -> e -> f -> g -> h }", 1)
	withReverb := effectScore + "fx room reverb {}\n"
	withDelay := effectScore + "fx echo delay {}\n"
	withGlue := strings.Replace(effectScore, "track keys piano {}", "fx glue comp {}\nbus music { insert = glue }\ntrack keys piano {}", 1)
	for _, tc := range []struct {
		name, source         string
		intent               *AddEffect
		reason, field, words string
	}{
		{"drive on master", effectScore, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "master"}, "effect_route", "inserton", "a drive can be inserted on a track, not on master"},
		{"drive on music", effectScore, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "music"}, "effect_route", "inserton", "a drive can be inserted on a track, not on bus music"},
		{"limiter on a track", effectScore, &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "bass"}, "effect_route", "inserton", "a limiter can be inserted on master, not on track bass"},
		{"delay as insert", effectScore, &AddEffect{Name: "echo", EffectKind: "delay", InsertOn: "bass"}, "effect_is_return", "inserton", "a delay is a send return, not an insert"},
		{"second reverb", withReverb, &AddEffect{Name: "hall", EffectKind: "reverb"}, "effect_single_instance", "effectkind", "the score already has the reverb return room"},
		{"tape beside a delay", withDelay, &AddEffect{Name: "warble", EffectKind: "tape"}, "effect_single_instance", "effectkind", "the score already has the delay return echo"},
		{"unknown target", effectScore, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "nosuch"}, "unknown_insert_target", "inserton", `inserton "nosuch" is not a track, music or master`},
		{"music taken", withGlue, &AddEffect{Name: "squash", EffectKind: "comp", InsertOn: "music"}, "music_insert_taken", "inserton", "bus music already has the insert glue"},
		{"full chain", eight, &AddEffect{Name: "i", EffectKind: "drive", InsertOn: "keys"}, "insert_chain_full", "inserton", "track keys already has 8 inserts"},
		{"name taken", effectScore, &AddEffect{Name: "bass", EffectKind: "drive"}, "name_taken", "name", "bass already names an effect, a track or a bus"},
		{"unknown kind", effectScore, &AddEffect{Name: "fuzz", EffectKind: "fuzzbox"}, "unknown_effect_kind", "effectkind", `unknown effect kind "fuzzbox"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyEffectIntent(t, tc.source, tc.intent)
			var reason *ReasonError
			var located *IntentError
			if !errors.As(err, &reason) || reason.Reason != tc.reason || !strings.Contains(err.Error(), tc.words) || !errors.As(err, &located) || located.Field != tc.field {
				t.Fatalf("got %v (%+v)", err, reason)
			}
		})
	}
}

// TestAddEffectWritesDeclarationAndRoute checks the exact text of a new
// effect with settings, its place at the end of an insert chain, and a new
// master declaration, with LF and CRLF.
func TestAddEffectWritesDeclarationAndRoute(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll(strings.Replace(effectScore, "track keys piano {}", "fx tilt eq {}\ntrack keys piano { insert = tilt }", 1), "\n", newline)
		got, err := applyEffectIntent(t, source, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "keys", Settings: map[string]json.RawMessage{"gain": json.RawMessage(`"6dB"`), "shape": json.RawMessage(`"hard"`)}})
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll(strings.Replace(effectScore, "track keys piano {}", "fx tilt eq {}\ntrack keys piano { insert = tilt -> grit }", 1)+"\nfx grit drive { shape = hard gain = 6dB }\n", "\n", newline)
		if got != want {
			t.Fatalf("%q:\n%s\nwant:\n%s", newline, got, want)
		}
		got, err = applyEffectIntent(t, strings.ReplaceAll(effectScore, "\n", newline), &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "master", Settings: map[string]json.RawMessage{"ceiling": json.RawMessage(`"-2dBTP"`)}})
		if err != nil {
			t.Fatal(err)
		}
		want = strings.ReplaceAll(effectScore+"\nfx ceiling limiter { ceiling = -2dBTP }\n\nmaster { insert = ceiling }\n", "\n", newline)
		if got != want {
			t.Fatalf("%q:\n%s\nwant:\n%s", newline, got, want)
		}
	}
}

// TestRemoveEffectRemovesItsRoutes removes an effect with the comment above
// it, its place in two insert chains and a send, and keeps every other byte.
func TestRemoveEffectRemovesItsRoutes(t *testing.T) {
	before := `cicada 2
tempo 120
// warm drive
fx grit drive {}
fx tilt eq {}
fx room reverb {}
track bass acid { cutoff = 620Hz insert = grit -> tilt }
track keys piano { insert = grit send room = -12dB }
pattern riff { a1 . c2 . }
scene main { bass = riff }
song { main*2 }
`
	for _, newline := range []string{"\n", "\r\n"} {
		got, err := applyEffectIntent(t, strings.ReplaceAll(before, "\n", newline), &RemoveEffect{Name: "grit"})
		if err != nil {
			t.Fatal(err)
		}
		want := strings.ReplaceAll(`cicada 2
tempo 120
fx tilt eq {}
fx room reverb {}
track bass acid { cutoff = 620Hz insert = tilt }
track keys piano { insert = none send room = -12dB }
pattern riff { a1 . c2 . }
scene main { bass = riff }
song { main*2 }
`, "\n", newline)
		if got != want {
			t.Fatalf("%q:\n%s\nwant:\n%s", newline, got, want)
		}
		got, err = applyEffectIntent(t, strings.ReplaceAll(before, "\n", newline), &RemoveEffect{Name: "room"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "room") || !strings.Contains(got, "track keys piano { insert = grit }") {
			t.Fatalf("%q: the send or return is left:\n%s", newline, got)
		}
	}
}

func TestRemoveEffectRefusesWhileUsed(t *testing.T) {
	source := `cicada 2
tempo 120
fx grit drive {}
track bass acid { insert = grit }
pattern riff { a1 . c2 . }
automate grit.mix {
  @1.1.1 0.5
}
scene main { bass = riff grit.mix = 0.8 }
song { main*2 }
`
	_, err := applyEffectIntent(t, source, &RemoveEffect{Name: "grit"})
	var reason *ReasonError
	if !errors.As(err, &reason) || reason.Reason != "effect_in_use" || len(reason.Uses) != 2 || reason.Uses[0] != "automation grit.mix (line 6)" || reason.Uses[1] != "scene main setting grit.mix (line 9)" {
		t.Fatalf("got %v (%+v)", err, reason)
	}
	if _, err := applyEffectIntent(t, source, &RemoveEffect{Name: "nosuch"}); !errors.As(err, &reason) || reason.Reason != "unknown_effect" {
		t.Fatalf("unknown effect: %v", err)
	}
}

// TestSetParamReachesMasterStageSettings sets a limiter's ceiling and a width
// stage's bass_mono bypass, which live outside the registry.
func TestSetParamReachesMasterStageSettings(t *testing.T) {
	source := effectScore + "fx ceiling limiter {}\nfx stereo width {}\nmaster { insert = stereo -> ceiling }\n"
	got, err := applyEffectIntent(t, source, &SetParam{Entity: "param:ceiling.ceiling", Value: json.RawMessage(`"-2.5dBTP"`)})
	if err != nil || !strings.Contains(got, "ceiling = -2.5dBTP") {
		t.Fatalf("limiter ceiling: %v\n%s", err, got)
	}
	got, err = applyEffectIntent(t, source, &SetParam{Entity: "param:stereo.bass_mono", Value: json.RawMessage(`"0Hz"`)})
	if err != nil || !strings.Contains(got, "bass_mono = 0Hz") {
		t.Fatalf("width bypass: %v\n%s", err, got)
	}
	if _, err := applyEffectIntent(t, source, &SetParam{Entity: "param:ceiling.ceiling", Value: json.RawMessage(`"3dBTP"`)}); err == nil {
		t.Fatal("a ceiling above 0 dBTP was accepted")
	}
}

// TestAddEffectLeavesTheRouteToALaterIntent declares a comp without inserton
// and inserts it with a setparam in the same envelope, as Studio's vocal
// chain does. cmd/cicada's TestApplyRefusesAnEffectNoChainHolds checks that
// a comp no intent routes fails validation.
func TestAddEffectLeavesTheRouteToALaterIntent(t *testing.T) {
	env := Envelope{Version: 1, Intents: []Intent{
		&AddEffect{Name: "glue", EffectKind: "comp"},
		&SetParam{Entity: "param:keys.insert", Value: json.RawMessage(`"glue"`)},
	}}
	result, err := Apply([]byte(effectScore), env, Options{Compiler: parseCompiler{}})
	if err != nil || !strings.Contains(string(result.Source), "insert = glue") || !strings.Contains(string(result.Source), "fx glue comp {}") {
		t.Fatalf("declare then route: %v\n%s", err, result.Source)
	}
}
