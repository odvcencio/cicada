package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

const studioMixerScore = `cicada 2

title "Mixer"

tempo 120

key a minor

fx room delay {
  feedback = 0.35
}

fx space reverb {}

fx grit drive {}

fx edge drive {}

fx glue comp {}

bus music {
  insert = glue
}

bus sfx {}

master {
  level = -1dB
  mute = off
  solo = off
  insert = none
}

track bass acid {
  // stored level
  level = -6dB
  mute = off
  insert = grit
  send room = 0.25
  out = music
}

pattern pulse acid {
  1 . 5 .
}

scene main {
  bass = pulse
}

song {
  main
}
`

var studioMixerEdition1Score = ""

func TestStudioMixerWriterEditsInPlaceAndKeepsComments(t *testing.T) {
	updated, before, after, changed, err := studioMixerSource([]byte(studioMixerScore), "bass.level", json.RawMessage(`-3.47`))
	if err != nil {
		t.Fatal(err)
	}
	if before != "-6dB" || after != "-3.5dB" {
		t.Fatalf("rounded change %q to %q", before, after)
	}
	if !strings.Contains(string(updated), "// stored level\n  level = -3.5dB") {
		t.Fatalf("in-place value edit lost nearby source: %s", updated)
	}
	if changed.Start == 0 || changed.End != changed.Start {
		t.Fatalf("changed source range: %+v", changed)
	}
	if string(updated) == string(studioMixerScore) {
		t.Fatal("writer did not update source")
	}
}

func TestStudioMixerWriterInsertsInFormatterOrderAndIsIdempotent(t *testing.T) {
	updated, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.pan", json.RawMessage(`0.256`))
	if err != nil {
		t.Fatal(err)
	}
	track := strings.Index(string(updated), "track bass acid {")
	level := strings.Index(string(updated[track:]), "level = -6dB")
	pan := strings.Index(string(updated[track:]), "pan = 0.26")
	mute := strings.Index(string(updated[track:]), "mute = off")
	if level < 0 || pan < 0 || mute < 0 || !(level < pan && pan < mute) {
		t.Fatalf("mixer insertion order is wrong: %s", updated[track:])
	}
	document, err := notation.ParseDocument(updated)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if string(formatted) != string(updated) {
		t.Fatalf("writer output is not formatter-clean; source:\n%s\nformatted:\n%s", updated, formatted)
	}
	second, _, _, _, err := studioMixerSource(updated, "bass.pan", json.RawMessage(`0.256`))
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(updated) {
		t.Fatal("repeating a mixer edit changed the source")
	}
}

func TestStudioMixerWriterAddsSettingsInsideInlineTrack(t *testing.T) {
	source := []byte("cicada 2\ntempo 120\nkey c minor\n// Keep this track comment.\ntrack keys acid { cutoff=700Hz }\npattern pulse notes { 1 . . . }\nscene main { keys=pulse }\nsong { main }\n")
	for _, test := range []struct {
		field string
		value string
	}{
		{"level", "-7.25"},
		{"pan", "0.125"},
		{"mute", "true"},
	} {
		t.Run(test.field, func(t *testing.T) {
			updated, _, _, _, err := studioMixerSource(source, "keys."+test.field, json.RawMessage(test.value))
			if err != nil {
				t.Fatal(err)
			}
			score, diagnostics := notation.Parse(updated)
			if score == nil || hasDiagnosticErrors(diagnostics) {
				t.Fatalf("inline mixer edit produced invalid source: %v\n%s", diagnostics, updated)
			}
			if len(score.Tracks) != 1 || !strings.Contains(string(updated), "// Keep this track comment.\ntrack keys acid {") || !strings.Contains(string(updated), "cutoff=700Hz") {
				t.Fatalf("inline mixer edit damaged its owner: %s", updated)
			}
			found := false
			for _, parameter := range score.Tracks[0].Params {
				found = found || parameter.Name == test.field
			}
			if !found {
				t.Fatalf("new %s is outside the track: %s", test.field, updated)
			}
			second, _, _, _, err := studioMixerSource(updated, "keys."+test.field, json.RawMessage(test.value))
			if err != nil || !bytes.Equal(updated, second) {
				t.Fatalf("repeating inline mixer edit changed source: %v\n%s", err, second)
			}
		})
	}
}

func TestStudioMixerWriterTrackLevelOffUsesMuteInEditionTwo(t *testing.T) {
	updated, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.level", json.RawMessage(`"off"`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "level = -6dB") || !strings.Contains(string(updated), "mute = on") {
		t.Fatalf("track off did not retain the valid level and save mute: %s", updated)
	}
}

func TestStudioMixerWriterBusAndMasterOffAlsoSaveMute(t *testing.T) {
	for _, path := range []string{"music.level", "master.level"} {
		updated, _, after, _, err := studioMixerSource([]byte(studioMixerScore), path, json.RawMessage(`"off"`))
		if err != nil || after != "off" {
			t.Fatalf("%s off: %v, after %q", path, err, after)
		}
		owner := strings.Split(path, ".")[0]
		if !strings.Contains(string(updated), owner+" {\n  level = off\n  mute = on") {
			t.Fatalf("%s off did not save the mute switch: %s", owner, updated)
		}
		document, parseErr := notation.ParseDocument(updated)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		formatted, formatErr := notation.Format(document)
		if formatErr != nil || !bytes.Equal(formatted, updated) {
			t.Fatalf("%s off is not formatter-clean: %v\n%s\nformatted:\n%s", owner, formatErr, updated, formatted)
		}
	}
}

func TestStudioMixerWriterAddsChangesAndRemovesNamedSends(t *testing.T) {
	added, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.send.space", json.RawMessage(`{"level":"-12dB","pre":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(added), "send space = -12dB pre") {
		t.Fatalf("named send was not added: %s", added)
	}
	stepped, _, after, _, err := studioMixerSource([]byte(studioMixerScore), "bass.send.space", json.RawMessage(`"-12.345dB"`))
	if err != nil || after != "send space = -12.35dB" || !strings.Contains(string(stepped), "send space = -12.35dB") {
		t.Fatalf("named send did not use its registry display step: %v, %q\n%s", err, after, stepped)
	}
	changed, before, after, _, err := studioMixerSource(added, "bass.send.space", json.RawMessage(`{"level":0.4,"pre":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if before != "send space = -12dB pre" || after != "send space = 0.4" || !strings.Contains(string(changed), "send space = 0.4") {
		t.Fatalf("named send change: %q to %q\n%s", before, after, changed)
	}
	removed, _, _, _, err := studioMixerSource(changed, "bass.send.space", json.RawMessage(`{"remove":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(removed), "send space") {
		t.Fatalf("named send was not removed: %s", removed)
	}
}

func TestStudioMixerWriterReordersAddsAndRemovesInsertChain(t *testing.T) {
	ordered, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.insert", json.RawMessage(`"edge -> grit"`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ordered), "insert = edge -> grit") {
		t.Fatalf("insert reorder was not written: %s", ordered)
	}
	added, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.insert", json.RawMessage(`"grit -> edge"`))
	if err != nil || !strings.Contains(string(added), "insert = grit -> edge") {
		t.Fatalf("insert chain add: %v\n%s", err, added)
	}
	removed, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.insert", json.RawMessage(`"none"`))
	if err != nil || !strings.Contains(string(removed), "insert = none") {
		t.Fatalf("insert chain remove: %v\n%s", err, removed)
	}
}

func TestStudioMixerWriterAddsFXBlockInFormatOrder(t *testing.T) {
	updated, before, after, _, err := studioMixerSource([]byte(studioMixerScore), "fx.texture", json.RawMessage(`{"kind":"drive"}`))
	if err != nil {
		t.Fatal(err)
	}
	if before != "absent" || after != "drive" || !strings.Contains(string(updated), "fx texture drive {}") {
		t.Fatalf("effect block was not added: %q to %q\n%s", before, after, updated)
	}
	document, err := notation.ParseDocument(updated)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil || string(formatted) != string(updated) {
		t.Fatalf("added effect is not formatter-clean: %v\nsource:\n%s\nformatted:\n%s", err, updated, formatted)
	}
}

func TestStudioMixerWriterCoversEffectUnitsAndEnums(t *testing.T) {
	updated, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "room.time", json.RawMessage(`"1/16T"`))
	if err != nil || !strings.Contains(string(updated), "time = 1/16T") {
		t.Fatalf("delay division: %v\n%s", err, updated)
	}
	updated, _, _, _, err = studioMixerSource([]byte(studioMixerScore), "room.damp", json.RawMessage(`"6khz"`))
	if err != nil || !strings.Contains(string(updated), "damp = 6000Hz") {
		t.Fatalf("effect unit normalization: %v\n%s", err, updated)
	}
	updated, _, _, _, err = studioMixerSource([]byte(studioMixerScore), "glue.makeup", json.RawMessage(`"auto"`))
	if err != nil || !strings.Contains(string(updated), "makeup = auto") {
		t.Fatalf("compressor auto makeup: %v\n%s", err, updated)
	}
}

func TestStudioMixerWriterInsertsEveryEffectFieldInFormatterOrder(t *testing.T) {
	for _, descriptor := range paramdefs.Registry {
		if descriptor.Scope != "global" || !strings.HasPrefix(descriptor.ID, "fx.") {
			continue
		}
		parts := strings.Split(descriptor.ID, ".")
		if len(parts) != 3 {
			t.Fatalf("unexpected effect descriptor ID %q", descriptor.ID)
		}
		kind := parts[1]
		name := map[string]string{"delay": "room", "reverb": "space", "drive": "grit", "comp": "glue"}[kind]
		if name == "" {
			t.Fatalf("no fixture effect for %s", kind)
		}
		var value any = descriptor.Default
		if descriptor.Curve == "enum" && len(descriptor.Values) > 0 {
			value = descriptor.Values[0]
		} else if descriptor.Curve == "toggle" {
			value = false
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		updated, _, _, _, err := studioMixerSource([]byte(studioMixerScore), name+"."+descriptor.Source, raw)
		if err != nil {
			t.Fatalf("insert %s: %v", descriptor.ID, err)
		}
		document, err := notation.ParseDocument(updated)
		if err != nil {
			t.Fatalf("parse %s: %v", descriptor.ID, err)
		}
		formatted, err := notation.Format(document)
		if err != nil || !bytes.Equal(formatted, updated) {
			t.Fatalf("%s insertion is not formatter-clean: %v\nsource:\n%s\nformatted:\n%s", descriptor.ID, err, updated, formatted)
		}
	}
}

func TestStudioMixerWriterRejectsInvalidValuesAndLeavesValidationToRoute(t *testing.T) {
	if _, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.pan", json.RawMessage(`2`)); err == nil {
		t.Fatal("accepted pan outside the parameter range")
	}
	updated, _, _, _, err := studioMixerSource([]byte(studioMixerScore), "bass.send.music", json.RawMessage(`0.3`))
	if err != nil {
		t.Fatalf("writer should make the grammar-backed unsupported send edit for validation: %v", err)
	}
	if !strings.Contains(string(updated), "send music = 0.3") {
		t.Fatalf("unsupported route edit missing from candidate: %s", updated)
	}
}

func TestStudioMixerViewAndRoutes(t *testing.T) {
	handler, path := studioMixerTestHandler(t, studioMixerScore)
	response := studioCall(t, handler, "/api/mixer", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"tracks"`) || !strings.Contains(response.Body.String(), `"addresses"`) {
		t.Fatalf("mixer view: %d %s", response.Code, response.Body.String())
	}
	script := studioCall(t, handler, "/studio-mix.js", nil)
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "faderPositionToDb") {
		t.Fatalf("Mix view script: %d %s", script.Code, script.Body.String())
	}
	var view studioMixerView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Tracks) != 1 || len(view.Returns) != 2 || len(view.Buses) != 2 || view.Master.ID != "master" {
		t.Fatalf("mixer view projection: tracks=%d buses=%d returns=%d master=%s", len(view.Tracks), len(view.Buses), len(view.Returns), view.Master.ID)
	}
	if view.Returns[0].Meter != "a" || view.Returns[1].Meter != "b" {
		t.Fatalf("effect return meter mapping: delay=%q reverb=%q", view.Returns[0].Meter, view.Returns[1].Meter)
	}
	if view.Tracks[0].Fields["level"].Address != "bass.level" || view.Tracks[0].Fields["level"].SourceRange.Start == 0 {
		t.Fatalf("track parameter address/range missing: %+v", view.Tracks[0].Fields["level"])
	}
	if view.Tracks[0].Sends[0].Address != "bass.send.delay" || view.Master.Fields["level"].Address != "master.level" || view.Tempo != 120 {
		t.Fatalf("parameter API mapping: send=%+v master=%+v tempo=%g", view.Tracks[0].Sends[0], view.Master.Fields["level"], view.Tempo)
	}

	revision := studioRevision([]byte(studioMixerScore))
	invalid := studioCall(t, handler, "/api/mixer", map[string]any{"revision": revision, "path": "bass.pan", "value": 2})
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid mixer edit: %d %s", invalid.Code, invalid.Body.String())
	}
	unsupported := studioCall(t, handler, "/api/mixer", map[string]any{"revision": revision, "path": "music.pan", "value": .4})
	if unsupported.Code != http.StatusUnprocessableEntity || !strings.Contains(unsupported.Body.String(), "CICADA-UNSUPPORTED") {
		t.Fatalf("unsupported bus pan: %d %s", unsupported.Code, unsupported.Body.String())
	}
	stale := studioCall(t, handler, "/api/mixer", map[string]any{"revision": strings.Repeat("0", 64), "path": "bass.level", "value": -3.5})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale mixer revision: %d %s", stale.Code, stale.Body.String())
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != studioMixerScore {
		t.Fatalf("rejected edits changed the score: %v", err)
	}
	accepted := studioCall(t, handler, "/api/mixer", map[string]any{"revision": revision, "path": "bass.level", "value": -3.47})
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), `"changedRange"`) {
		t.Fatalf("valid mixer edit: %d %s", accepted.Code, accepted.Body.String())
	}
	content, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "// stored level\n  level = -3.5dB") {
		t.Fatalf("source was not saved after mixer edit: %v\n%s", err, content)
	}
	off := studioCall(t, handler, "/api/mixer", map[string]any{"revision": studioRevision(content), "path": "bass.level", "value": "off"})
	if off.Code != http.StatusOK {
		t.Fatalf("saved track level off: %d %s", off.Code, off.Body.String())
	}
	content, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "level = -3.5dB") || !strings.Contains(string(content), "mute = on") {
		t.Fatalf("track level off did not persist mute: %v\n%s", err, content)
	}
	restored := studioCall(t, handler, "/api/mixer", map[string]any{"revision": studioRevision(content), "path": "bass.level", "value": -2.3})
	if restored.Code != http.StatusOK {
		t.Fatalf("restored track level: %d %s", restored.Code, restored.Body.String())
	}
	content, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(content), "level = -2.3dB") || strings.Contains(string(content), "mute = on") {
		t.Fatalf("moving the fader from Off must save the level and clear the mute: %v\n%s", err, content)
	}
	history := studioCall(t, handler, "/api/history", nil)
	if !strings.Contains(history.Body.String(), "Mix: bass level -6 dB to -3.5 dB") {
		t.Fatalf("mixer gesture history entry missing: %s", history.Body.String())
	}
	if count := strings.Count(history.Body.String(), "Mix: bass level -6 dB to -3.5 dB"); count != 1 {
		t.Fatalf("expected one history entry for the gesture, got %d: %s", count, history.Body.String())
	}
}

func TestStudioMixerEditionOneUpgradeRequiresConfirmation(t *testing.T) {
	legacy, err := os.ReadFile(filepath.Join("..", "..", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	studioMixerEdition1Score = string(legacy)
	handler, path := studioMixerTestHandler(t, studioMixerEdition1Score)
	revision := studioRevision([]byte(studioMixerEdition1Score))
	first := studioCall(t, handler, "/api/mixer", map[string]any{"revision": revision, "path": "bass.level", "value": -3.5})
	if first.Code != http.StatusConflict || !strings.Contains(first.Body.String(), "This score uses edition 1. Upgrade to edition 2 to save mixer changes?") {
		t.Fatalf("edition-1 prompt: %d %s", first.Code, first.Body.String())
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != studioMixerEdition1Score {
		t.Fatalf("unconfirmed migration changed source: %v", err)
	}
	confirmed := studioCall(t, handler, "/api/mixer", map[string]any{"revision": revision, "path": "bass.level", "value": -3.5, "confirmUpgrade": true})
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirmed edition-1 mixer edit: %d %s", confirmed.Code, confirmed.Body.String())
	}
	content, err = os.ReadFile(path)
	if err != nil || !strings.HasPrefix(string(content), "cicada 2\n") || !strings.Contains(string(content), "send delay = 0.4") || !strings.Contains(string(content), "level = -3.5dB") {
		t.Fatalf("edition-1 migration did not use the fix path: %v\n%s", err, content)
	}
	document, err := notation.ParseDocument(content)
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil || !bytes.Equal(formatted, content) {
		t.Fatalf("edition-1 upgrade is not formatter-clean: %v\nsource:\n%s\nformatted:\n%s", err, content, formatted)
	}
}

func studioMixerTestHandler(t *testing.T, source string) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := studioHandler(path)
	if err != nil {
		t.Fatal(err)
	}
	return handler, path
}

func TestStudioMixerResolvesPresetEffectKinds(t *testing.T) {
	source := []byte("cicada 2\npreset wet { instrument=builtin.delay feedback=0.3 }\nfx echo wet {}\ntrack lead acid { send echo=0.4 }\npattern melody { 1 . }\nscene main { lead=melody }\nsong { main }\n")
	filename := filepath.Join(t.TempDir(), "main.cicada")
	if err := os.WriteFile(filename, source, 0600); err != nil {
		t.Fatal(err)
	}
	view, err := buildStudioMixerView(filename, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tracks) != 1 || len(view.Tracks[0].Sends) != 1 || view.Tracks[0].Sends[0].Kind != "delay" || len(view.Returns) != 1 || view.Returns[0].Kind != "delay" {
		t.Fatalf("preset sends and returns missing: tracks=%d sends=%d returns=%d", len(view.Tracks), len(view.Tracks[0].Sends), len(view.Returns))
	}
	if view.Returns[0].SourceRange == (mixerLineRange{}) {
		t.Fatal("source range for preset instance lost")
	}
}

func TestStudioMixerWritesPresetEffectParameters(t *testing.T) {
	source := []byte("cicada 2\npreset wet { instrument=builtin.delay feedback=0.3 }\nfx echo wet {}\ntrack lead acid { send echo=0.4 }\npattern melody { 1 . }\nscene main { lead=melody }\nsong { main }\n")
	for _, value := range []json.RawMessage{json.RawMessage(`0.6`), json.RawMessage(`0.7`)} {
		updated, _, after, _, err := studioMixerSource(source, "echo.feedback", value)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(updated, []byte("preset wet { instrument=builtin.delay feedback=0.3 }")) || !bytes.Contains(updated, []byte("fx echo wet")) {
			t.Fatalf("preset binding changed: %s", updated)
		}
		score, ds := notation.Parse(updated)
		if score == nil || hasDiagnosticErrors(ds) {
			t.Fatalf("updated score invalid: %+v", ds)
		}
		resolved, ds := notation.ResolvePresets(score)
		if hasDiagnosticErrors(ds) || len(resolved.Effects) != 1 || trackMixerSourceText(resolved.Effects[0].Params, "feedback") != after {
			t.Fatalf("effect override not saved: %+v %+v", resolved.Effects, ds)
		}
		source = updated
	}
}
