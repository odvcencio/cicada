package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The scores the review of the effect intents probed (#319), as regression
// inputs.
const (
	probeScore   = "cicada 2\ntempo 120\n// the bass line\ntrack bass acid { cutoff = 620Hz }\ntrack keys piano {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"
	probeMaster  = probeScore + "fx ceiling limiter {}\nfx stereo width {}\nfx glue comp {}\nmaster { insert = glue -> stereo -> ceiling }\n"
	probeRemoval = "cicada 2\ntempo 120\n\n// glue for the mix\nfx glue comp { threshold = -18dB }\n\nfx grit drive {} // keep this one\nfx tilt eq {}\nfx room reverb {}\nfx ceiling limiter { ceiling = -1dBTP }\ntrack bass acid {\n  cutoff = 620Hz\n  insert = grit -> tilt\n  send room = -12dB pre\n}\ntrack keys piano { send room = -6dB insert = tilt }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\nbus music { insert = glue }\nmaster { insert = ceiling }\n"
	probeLegacy  = "cicada 1\ntempo 120\nfx delay { time = 1/8 }\ntrack bass acid { send_a = 0.3 }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"
	probePreset  = "cicada 2\ntempo 120\nfx grit drive { gain = 6dB }\npreset dirty { instrument=grit mix=0.7 }\nfx crunch dirty {}\ntrack bass acid { insert = grit -> crunch }\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n"
)

var doubledCode = regexp.MustCompile(`(CICADA-[A-Z-]+): (CICADA-[A-Z-]+):`)

// TestEffectIntentProbesAreNamedRefusalsOrValidEdits applies each probe of
// the review with apply --json. A probe either writes a score that passes
// check, or is refused with intent_refused, a reason from its kind's
// catalog list and the expected field; none ends in invalid_result, and no
// message repeats its diagnostic code.
func TestEffectIntentProbesAreNamedRefusalsOrValidEdits(t *testing.T) {
	for _, tc := range []struct {
		name, score, intents, reason, field string
	}{
		{"drive with settings", probeScore, `{"kind":"addeffect","name":"grit","effectkind":"drive","inserton":"bass","settings":{"gain":"6dB"}}`, "", ""},
		{"eq in CRLF", strings.ReplaceAll(probeScore, "\n", "\r\n"), `{"kind":"addeffect","name":"tilt","effectkind":"eq","inserton":"keys"}`, "", ""},
		{"limiter in CRLF", strings.ReplaceAll(probeScore, "\n", "\r\n"), `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":-1}}`, "", ""},
		{"limiter on a one-line master", probeScore + "master { level = 3dB }\n", `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"-1dBTP"}}`, "", ""},
		{"master comp sidechain", probeScore, `{"kind":"addeffect","name":"glue","effectkind":"comp","inserton":"master","settings":{"sidechain":"bass"}}`, "sidechain_unavailable", "settings.sidechain"},
		{"track comp sidechain", probeScore, `{"kind":"addeffect","name":"glue","effectkind":"comp","inserton":"keys","settings":{"sidechain":"nosuch"}}`, "sidechain_unavailable", "settings.sidechain"},
		{"music comp sidechain", probeScore, `{"kind":"addeffect","name":"glue","effectkind":"comp","inserton":"music","settings":{"sidechain":"bass"}}`, "", ""},
		{"convolution without asset", probeScore, `{"kind":"addeffect","name":"space","effectkind":"convolution","inserton":"master","settings":{"mix":0.2}}`, "convolution_needs_asset", "settings.asset"},
		{"convolution with an unknown asset", probeScore, `{"kind":"addeffect","name":"space","effectkind":"convolution","inserton":"master","settings":{"asset":"vocal"}}`, "unknown_asset", "settings.asset"},
		{"sfx", probeScore, `{"kind":"addeffect","name":"glue","effectkind":"comp","inserton":"sfx"}`, "effect_route", "inserton"},
		{"name of a pattern", probeScore, `{"kind":"addeffect","name":"riff","effectkind":"drive"}`, "", ""},
		{"name of a scene", probeScore, `{"kind":"addeffect","name":"main","effectkind":"eq"}`, "", ""},
		{"ceiling above range", probeScore, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"3dBTP"}}`, "value_out_of_range", "settings.ceiling"},
		{"unknown setting", probeScore, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceil":"-1dBTP"}}`, "unknown_setting", "settings.ceil"},
		{"track eq frequency", probeScore, `{"kind":"addeffect","name":"tilt","effectkind":"eq","inserton":"keys","settings":{"frequency":"21000Hz"}}`, "value_out_of_range", "settings.frequency"},
		{"master eq frequency", probeScore, `{"kind":"addeffect","name":"tilt","effectkind":"eq","inserton":"master","settings":{"frequency":"21000Hz"}}`, "", ""},
		{"name of a voice", probeScore, `{"kind":"addeffect","name":"piano","effectkind":"drive"}`, "", ""},
		{"spaced unit", probeScore, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"-1 dBTP"}}`, "invalid_value", "settings.ceiling"},
		{"wrong unit", probeScore, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"-1dB"}}`, "invalid_value", "settings.ceiling"},
		{"unrouted limiter", probeScore, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","settings":{"ceiling":-1}}`, "effect_needs_insert", "inserton"},
		{"vocal chain", probeScore, `{"kind":"addeffect","name":"bass_vocal_hp","effectkind":"eq"},{"kind":"addeffect","name":"bass_vocal_comp","effectkind":"comp"},{"kind":"addeffect","name":"bass_vocal_deess","effectkind":"deesser"},{"kind":"setparam","entity":"param:bass.insert","value":"bass_vocal_hp -> bass_vocal_comp -> bass_vocal_deess"}`, "", ""},
		{"setparam ceiling above range", probeMaster, `{"kind":"setparam","entity":"param:ceiling.ceiling","value":"0.5dBTP"}`, "value_out_of_range", "value"},
		{"setparam ceiling", probeMaster, `{"kind":"setparam","entity":"param:ceiling.ceiling","value":-1.5}`, "", ""},
		{"setparam width bypass", probeMaster, `{"kind":"setparam","entity":"param:stereo.bass_mono","value":0}`, "", ""},
		{"setparam master sidechain", probeMaster, `{"kind":"setparam","entity":"param:glue.sidechain","value":"bass"}`, "sidechain_unavailable", "value"},
		{"setparam lookahead above range", probeMaster, `{"kind":"setparam","entity":"param:ceiling.lookahead","value":"20ms"}`, "value_out_of_range", "value"},
		{"edition 1 without confirmation", probeLegacy, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"-1dBTP"}}`, "edition_upgrade_needed", "confirmupgrade"},
		{"edition 1 confirmed", probeLegacy, `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":"-1dBTP"},"confirmupgrade":true}`, "", ""},
		{"legacy send", probeLegacy, `{"kind":"removeeffect","name":"delay"}`, "effect_in_use", "name"},
		{"preset target", probePreset, `{"kind":"removeeffect","name":"grit"}`, "effect_in_use", "name"},
		{"remove a limiter", probeRemoval, `{"kind":"removeeffect","name":"ceiling"}`, "", ""},
		{"remove the music comp", probeRemoval, `{"kind":"removeeffect","name":"glue"}`, "", ""},
		{"remove a commented drive", probeRemoval, `{"kind":"removeeffect","name":"grit"}`, "", ""},
		{"remove a reverb", probeRemoval, `{"kind":"removeeffect","name":"room"}`, "", ""},
		{"remove a shared eq", probeRemoval, `{"kind":"removeeffect","name":"tilt"}`, "", ""},
		{"place before", probeRemoval, `{"kind":"addeffect","name":"fuzz","effectkind":"drive","inserton":"bass","before":"tilt"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			score := filepath.Join(dir, "song.cicada")
			if err := os.WriteFile(score, []byte(tc.score), 0o644); err != nil {
				t.Fatal(err)
			}
			document := probeApply(t, score, tc.intents)
			if doubledCode.MatchString(document) {
				t.Fatalf("a message repeats its code:\n%s", document)
			}
			var result applyJSONResult
			if err := json.Unmarshal([]byte(document), &result); err != nil {
				t.Fatal(err)
			}
			failure := result.Error
			if tc.reason == "" {
				if !result.OK || !result.Written {
					t.Fatalf("refused: %+v", failure)
				}
				if checked, err := runCheckJSON(t, score); err != nil || !checked.Valid {
					written, _ := os.ReadFile(score)
					t.Fatalf("the written score does not pass check: %v\n%s", err, written)
				}
				return
			}
			if failure == nil || failure.Code != "intent_refused" || failure.Reason != tc.reason || failure.Field != tc.field {
				t.Fatalf("want intent_refused %s on %s: %+v", tc.reason, tc.field, failure)
			}
			if written, _ := os.ReadFile(score); string(written) != tc.score {
				t.Fatal("a refused intent changed the score")
			}
		})
	}
}

// probeApply runs apply --write --json and returns its document.
func probeApply(t *testing.T, score, intents string) string {
	t.Helper()
	envelope := filepath.Join(filepath.Dir(score), "intents.json")
	if err := os.WriteFile(envelope, []byte(`{"version":1,"intents":[`+intents+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	_ = applyCommandWithHooks([]string{score, envelope, "--write", "--json"}, &stdout, nil)
	return stdout.String()
}

// TestEffectIntentsRefuseEditsToOtherFiles applies the review's multi-file
// probes: the tracks and master live in parts/tracks.cicada, and the edit
// writes only main.cicada, so each is refused with the other file named.
func TestEffectIntentsRefuseEditsToOtherFiles(t *testing.T) {
	for _, tc := range []struct {
		name, intents, reason, field string
	}{
		{"insert on a track there", `{"kind":"addeffect","name":"fuzz","effectkind":"drive","inserton":"keys"}`, "declared_in_other_file", "inserton"},
		{"insert on master there", `{"kind":"addeffect","name":"ceiling","effectkind":"limiter","inserton":"master","settings":{"ceiling":-1}}`, "declared_in_other_file", "inserton"},
		{"remove an effect routed there", `{"kind":"removeeffect","name":"grit"}`, "route_in_other_file", "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{
				"cicada.mod":          "project mf\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/tracks.cicada\"\n",
				"main.cicada":         "tempo 120\nfx grit drive {}\npattern riff { a1 . c2 . }\nscene main { bass = riff }\nsong { main*2 }\n",
				"parts/tracks.cicada": "track bass acid { insert = grit }\ntrack keys piano {}\nmaster { level = 0dB }\n",
			} {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var result applyJSONResult
			if err := json.Unmarshal([]byte(probeApply(t, filepath.Join(dir, "main.cicada"), tc.intents)), &result); err != nil {
				t.Fatal(err)
			}
			failure := result.Error
			if failure == nil || failure.Code != "intent_refused" || failure.Reason != tc.reason || failure.Field != tc.field || !strings.HasSuffix(failure.File, "parts/tracks.cicada") {
				t.Fatalf("want %s naming parts/tracks.cicada: %+v", tc.reason, failure)
			}
		})
	}
}
