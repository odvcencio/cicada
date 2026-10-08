package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
