package edit

import (
	"encoding/json"
	"errors"
	"fmt"
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
		want := strings.ReplaceAll(strings.Replace(effectScore, "track keys piano {}", "fx tilt eq {}\ntrack keys piano { insert = tilt -> grit }", 1)+"\nfx grit drive {\n  shape = hard\n  gain = 6dB\n}\n", "\n", newline)
		if got != want {
			t.Fatalf("%q:\n%s\nwant:\n%s", newline, got, want)
		}
		got, err = applyEffectIntent(t, strings.ReplaceAll(effectScore, "\n", newline), &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "master", Settings: map[string]json.RawMessage{"ceiling": json.RawMessage(`"-2dBTP"`)}})
		if err != nil {
			t.Fatal(err)
		}
		want = strings.ReplaceAll(effectScore+"\nfx ceiling limiter {\n  ceiling = -2dBTP\n}\n\nmaster {\n  insert = ceiling\n}\n", "\n", newline)
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
		// The comment heads the effects that follow grit, so it stays.
		want := strings.ReplaceAll(`cicada 2
tempo 120
// warm drive
fx tilt eq {}
fx room reverb {}
track bass acid { cutoff = 620Hz insert = tilt }
track keys piano { send room = -12dB }
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

// TestFirstSettingKeepsTheBlockLayout adds the first insert or send to
// one-line, empty, commented and multi-line track, bus and master blocks.
// A one-line block is expanded to the layout cicada fmt gives it; an expanded
// block keeps its indentation; the file's newline is used throughout.
func TestFirstSettingKeepsTheBlockLayout(t *testing.T) {
	base := "cicada 2\ntempo 120\nBLOCK\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"
	for _, tc := range []struct {
		name, before, after, appended string
		intent                        Intent
	}{
		{"one-line track", "track bass acid { cutoff = 620Hz }", "track bass acid {\n  insert = fuzz\n  cutoff = 620Hz\n}", "\nfx fuzz drive {}\n", &AddEffect{Name: "fuzz", EffectKind: "drive", InsertOn: "bass"}},
		{"empty track with a comment", "track bass acid {}\ntrack keys piano {} // keys", "track bass acid {}\ntrack keys piano {\n  insert = tilt\n} // keys", "\nfx tilt eq {}\n", &AddEffect{Name: "tilt", EffectKind: "eq", InsertOn: "keys"}},
		{"multi-line track", "track bass acid {\n  // tone\n  cutoff = 620Hz\n}", "track bass acid {\n  insert = fuzz\n  // tone\n  cutoff = 620Hz\n}", "\nfx fuzz drive {}\n", &AddEffect{Name: "fuzz", EffectKind: "drive", InsertOn: "bass"}},
		{"one-line bus", "track bass acid {}\nbus music { level = -3dB }", "track bass acid {}\nbus music {\n  level = -3dB\n  insert = glue\n}", "\nfx glue comp {}\n", &AddEffect{Name: "glue", EffectKind: "comp", InsertOn: "music"}},
		{"one-line master", "track bass acid {}\nmaster { level = 3dB }", "track bass acid {}\nmaster {\n  level = 3dB\n  insert = ceiling\n}", "\nfx ceiling limiter {}\n", &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "master"}},
		{"indented master", "track bass acid {}\nmaster {\n    level = 3dB\n}", "track bass acid {}\nmaster {\n    level = 3dB\n    insert = ceiling\n}", "\nfx ceiling limiter {}\n", &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "master"}},
		{"first send", "fx room reverb {}\ntrack bass acid { cutoff = 620Hz }", "fx room reverb {}\ntrack bass acid {\n  cutoff = 620Hz\n  send room = -12dB\n}", "", &SetParam{Entity: "param:bass.send.room", Value: json.RawMessage(`"-12dB"`)}},
	} {
		for _, newline := range []string{"\n", "\r\n"} {
			t.Run(fmt.Sprintf("%s/%q", tc.name, newline), func(t *testing.T) {
				source := strings.ReplaceAll(strings.Replace(base, "BLOCK", tc.before, 1), "\n", newline)
				got, err := applyEffectIntent(t, source, tc.intent)
				if err != nil {
					t.Fatal(err)
				}
				want := strings.ReplaceAll(strings.Replace(base, "BLOCK", tc.after, 1)+tc.appended, "\n", newline)
				if got != want {
					t.Fatalf("got:\n%q\nwant:\n%q", got, want)
				}
			})
		}
	}
}

// TestEffectRefusalsCarryReasonAndRange covers the sidechain, range,
// placement and sfx refusals with their reason codes and fields.
func TestEffectRefusalsCarryReasonAndRange(t *testing.T) {
	withMaster := effectScore + "fx glue comp {}\nfx ceiling limiter {}\nmaster { insert = glue -> ceiling }\n"
	withChain := strings.Replace(effectScore, "track keys piano {}", "fx tilt eq {}\nfx wide chorus {}\ntrack keys piano { insert = tilt -> wide }", 1)
	for _, tc := range []struct {
		name, source         string
		intent               Intent
		reason, field, words string
	}{
		{"master comp sidechain", effectScore, &AddEffect{Name: "glue", EffectKind: "comp", InsertOn: "master", Settings: map[string]json.RawMessage{"sidechain": json.RawMessage(`"bass"`)}}, "sidechain_unavailable", "settings.sidechain", "a compressor inserted on master uses its own input"},
		{"track comp sidechain", effectScore, &AddEffect{Name: "glue", EffectKind: "comp", InsertOn: "keys", Settings: map[string]json.RawMessage{"sidechain": json.RawMessage(`"nosuch"`)}}, "sidechain_unavailable", "settings.sidechain", "on a track uses its own input"},
		{"music comp unknown sidechain", effectScore, &AddEffect{Name: "glue", EffectKind: "comp", InsertOn: "music", Settings: map[string]json.RawMessage{"sidechain": json.RawMessage(`"nosuch"`)}}, "unknown_sidechain_track", "settings.sidechain", `sidechain "nosuch" is not a track of the score, music or sfx`},
		{"setparam master sidechain", withMaster, &SetParam{Entity: "param:glue.sidechain", Value: json.RawMessage(`"bass"`)}, "sidechain_unavailable", "value", "on master uses its own input"},
		{"ceiling above range", effectScore, &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "master", Settings: map[string]json.RawMessage{"ceiling": json.RawMessage(`"3dBTP"`)}}, "value_out_of_range", "settings.ceiling", "ceiling 3dBTP is above the maximum"},
		{"setparam ceiling above range", withMaster, &SetParam{Entity: "param:ceiling.ceiling", Value: json.RawMessage(`"0.5dBTP"`)}, "value_out_of_range", "value", "ceiling 0.5dBTP is above the maximum"},
		{"pan outside", effectScore, &SetParam{Entity: "param:bass.pan", Value: json.RawMessage(`2`)}, "value_out_of_range", "value", "pan 2 is outside -1 to 1"},
		{"unknown anchor", withChain, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "keys", Before: "nosuch"}, "unknown_insert_anchor", "before", "nosuch is not in the insert chain of keys (tilt -> wide)"},
		{"both anchors", withChain, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "keys", Before: "tilt", After: "wide"}, "insert_position_conflict", "after", "give before or after, not both"},
		{"anchor in no chain", withChain, &AddEffect{Name: "grit", EffectKind: "drive", After: "nosuch"}, "unknown_insert_anchor", "after", "nosuch is not in any insert chain"},
		{"sfx", effectScore, &AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "sfx"}, "effect_route", "inserton", "bus sfx takes no inserts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := applyEffectIntent(t, tc.source, tc.intent)
			var reason *ReasonError
			var located *IntentError
			if !errors.As(err, &reason) || reason.Reason != tc.reason || !strings.Contains(err.Error(), tc.words) || !errors.As(err, &located) || located.Field != tc.field {
				t.Fatalf("got %v (%+v)", err, reason)
			}
			if tc.reason == "value_out_of_range" && (reason.Range == nil || reason.Range.Parameter == "" || reason.Range.Value == "") {
				t.Fatalf("no range: %+v", reason)
			}
		})
	}
	// Putting a comp with a sidechain in a chain checks the sidechain too.
	declared := Envelope{Version: 1, Intents: []Intent{
		&AddEffect{Name: "glue", EffectKind: "comp", Settings: map[string]json.RawMessage{"sidechain": json.RawMessage(`"bass"`)}},
		&SetParam{Entity: "param:bass.insert", Value: json.RawMessage(`"glue"`)},
	}}
	_, err := Apply([]byte(effectScore), declared, Options{Compiler: parseCompiler{}})
	var chained *ReasonError
	var at *IntentError
	if !errors.As(err, &chained) || chained.Reason != "sidechain_unavailable" || !errors.As(err, &at) || at.Index != 1 || at.Field != "value" {
		t.Fatalf("a track chain took a sidechained comp: %v", err)
	}
	got, err := applyEffectIntent(t, effectScore+"fx glue comp {}\nbus music { insert = glue }\n", &SetParam{Entity: "param:glue.sidechain", Value: json.RawMessage(`"bass"`)})
	if err != nil || !strings.Contains(got, "sidechain = bass") {
		t.Fatalf("the music comp takes a track sidechain: %v\n%s", err, got)
	}
	_, err = applyEffectIntent(t, effectScore, &AddEffect{Name: "ceiling", EffectKind: "limiter", InsertOn: "master", Settings: map[string]json.RawMessage{"ceiling": json.RawMessage(`"3dBTP"`)}})
	var reason *ReasonError
	if !errors.As(err, &reason) || *reason.Range != (ValueRange{Parameter: "ceiling", Value: "3dBTP", Min: -12, Max: 0, Unit: "dBTP"}) {
		t.Fatalf("range: %+v", reason)
	}
}

// TestAddEffectPlacesInTheChain puts a new insert in front of or behind one
// already in the chain.
func TestAddEffectPlacesInTheChain(t *testing.T) {
	source := strings.Replace(effectScore, "track keys piano {}", "fx tilt eq {}\nfx wide chorus {}\ntrack keys piano { insert = tilt -> wide }", 1)
	for _, tc := range []struct {
		intent *AddEffect
		chain  string
	}{
		{&AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "keys", Before: "tilt"}, "grit -> tilt -> wide"},
		{&AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "keys", After: "tilt"}, "tilt -> grit -> wide"},
		{&AddEffect{Name: "grit", EffectKind: "drive", InsertOn: "keys", After: "wide"}, "tilt -> wide -> grit"},
		{&AddEffect{Name: "grit", EffectKind: "drive", After: "tilt"}, "tilt -> grit -> wide"},
	} {
		got, err := applyEffectIntent(t, source, tc.intent)
		if err != nil || !strings.Contains(got, "track keys piano { insert = "+tc.chain+" }") {
			t.Fatalf("%+v: %v\n%s", tc.intent, err, got)
		}
	}
}

// TestRemoveEffectListsEveryUse refuses while a preset targets the effect or
// an edition-1 return's send level names it, and takes a comment after the
// declaration with it.
func TestRemoveEffectListsEveryUse(t *testing.T) {
	preset := "cicada 2\ntempo 120\nfx grit drive { gain = 6dB }\npreset dirty { instrument=grit mix=0.7 }\nfx crunch dirty {}\ntrack bass acid { insert = grit -> crunch }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"
	_, err := applyEffectIntent(t, preset, &RemoveEffect{Name: "grit"})
	var reason *ReasonError
	if !errors.As(err, &reason) || reason.Reason != "effect_in_use" || strings.Join(reason.Uses, "|") != "preset dirty (line 4)" {
		t.Fatalf("preset: %v (%+v)", err, reason)
	}
	legacy := "cicada 1\ntempo 120\nfx delay { time = 1/8 }\ntrack bass acid { send_a = 0.3 }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"
	if _, err := applyEffectIntent(t, legacy, &RemoveEffect{Name: "delay"}); !errors.As(err, &reason) || reason.Reason != "effect_in_use" || strings.Join(reason.Uses, "|") != "track bass send_a (line 4)" {
		t.Fatalf("legacy send: %v (%+v)", err, reason)
	}
	for _, newline := range []string{"\n", "\r\n"} {
		source := strings.ReplaceAll("cicada 2\ntempo 120\nfx grit drive {} // keep this one\nfx tilt eq {}\ntrack bass acid {\n  cutoff = 620Hz\n  insert = grit\n}\ntrack keys piano { insert = grit -> tilt }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n", "\n", newline)
		got, err := applyEffectIntent(t, source, &RemoveEffect{Name: "grit"})
		want := strings.ReplaceAll("cicada 2\ntempo 120\nfx tilt eq {}\ntrack bass acid {\n  cutoff = 620Hz\n}\ntrack keys piano { insert = tilt }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n", "\n", newline)
		if err != nil || got != want {
			t.Fatalf("%q: %v\n%q\nwant\n%q", newline, err, got, want)
		}
	}
}

// A second reverb is a return of its own (slot C): addeffect declares it
// beside the first, and a track can then send to it by name.
func TestAddEffectDeclaresASecondReverb(t *testing.T) {
	source, err := applyEffectIntent(t, effectScore+"fx room reverb {}\n", &AddEffect{Name: "hall", EffectKind: "reverb"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source, "fx room reverb") || !strings.Contains(source, "fx hall reverb") {
		t.Fatalf("source lacks both reverbs:\n%s", source)
	}
}

// addeffect refuses a return named like a built-in send path it is not:
// a second reverb named reverb or delay, or a delay named reverb.
func TestAddEffectRefusesReturnNamesThatAreSendPaths(t *testing.T) {
	withHall := effectScore + "fx hall reverb {}\n"
	for _, tc := range []struct {
		name, source string
		intent       *AddEffect
	}{
		{"second reverb named reverb", withHall, &AddEffect{Name: "reverb", EffectKind: "reverb"}},
		{"second reverb named delay", withHall, &AddEffect{Name: "delay", EffectKind: "reverb"}},
		{"delay named reverb", effectScore, &AddEffect{Name: "reverb", EffectKind: "delay"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			structureRefusal(t, tc.source, tc.intent, "invalid_name", "name", "cannot be named")
		})
	}
	if _, err := applyEffectIntent(t, effectScore, &AddEffect{Name: "reverb", EffectKind: "reverb"}); err != nil {
		t.Fatalf("a first reverb may be named reverb: %v", err)
	}
	// rename takes tracks, patterns and scenes, so it cannot give an effect
	// a send path's name.
	structureRefusal(t, withHall, &Rename{Entity: "effect:hall", Name: "reverb"}, "unknown_entity", "entity", "rename takes a track, a pattern or a scene")
}
