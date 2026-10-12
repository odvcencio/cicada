package notation

import (
	"bytes"
	"strings"
	"testing"
)

const sceneTempoSource = "cicada 1\ntempo 120\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene main {\n  bass = riff\n}\nscene outro {\n  bass = riff\n  tempo = 84\n}\nsong { main*2 outro*2 }\n"

func TestSceneTempoIsASetting(t *testing.T) {
	score, diagnostics := Parse([]byte(sceneTempoSource))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
	outro := score.Scenes[1]
	if len(outro.Settings) != 1 || outro.Settings[0].Path != "tempo" || outro.Settings[0].Value != "84" {
		t.Fatalf("tempo was not parsed as a setting: %+v", outro)
	}
	if len(outro.Bindings) != 1 || outro.Bindings[0].Track != "bass" {
		t.Fatalf("bindings changed: %+v", outro.Bindings)
	}
}

func TestSceneTempoRejectsOutOfRange(t *testing.T) {
	for _, value := range []string{"19", "301", "84Hz"} {
		src := strings.Replace(sceneTempoSource, "tempo = 84", "tempo = "+value, 1)
		_, diagnostics := Parse([]byte(src))
		if len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-UNIT" {
			t.Fatalf("tempo = %s: want one CICADA-UNIT diagnostic, got %+v", value, diagnostics)
		}
	}
	for _, value := range []string{"20", "300", "96.5"} {
		src := strings.Replace(sceneTempoSource, "tempo = 84", "tempo = "+value, 1)
		if _, diagnostics := Parse([]byte(src)); len(diagnostics) != 0 {
			t.Fatalf("tempo = %s: %+v", value, diagnostics)
		}
	}
}

func TestTrackNamedTempoStillBinds(t *testing.T) {
	src := "cicada 1\ntrack tempo acid {}\npattern riff acid steps=1 { 1 }\nscene main {\n  tempo = riff\n}\nsong { main }\n"
	score, diagnostics := Parse([]byte(src))
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics: %+v", diagnostics)
	}
	scene := score.Scenes[0]
	if len(scene.Bindings) != 1 || scene.Bindings[0].Track != "tempo" || scene.Bindings[0].Pattern != "riff" || len(scene.Settings) != 0 {
		t.Fatalf("track binding named tempo changed: %+v", scene)
	}
}

func TestSceneTempoFormatRoundTrip(t *testing.T) {
	src := "cicada 1\ntempo 120\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene outro {\n  tempo = 84\n  bass = riff\n}\nsong { outro }\n"
	doc, err := ParseDocument([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := Format(doc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(formatted)
	if strings.Index(text, "bass = riff") > strings.Index(text, "tempo = 84") {
		t.Fatalf("tempo setting must follow bindings:\n%s", text)
	}
	score, diagnostics := Parse(formatted)
	if len(diagnostics) != 0 || len(score.Scenes[0].Settings) != 1 || score.Scenes[0].Settings[0].Path != "tempo" {
		t.Fatalf("round trip lost the setting: %+v\n%s", diagnostics, text)
	}
	again, err := ParseDocument(formatted)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Format(again)
	if err != nil || !bytes.Equal(formatted, twice) {
		t.Fatalf("not idempotent: %v\n%s", err, twice)
	}
}

func TestSceneTempoIsRejectedUnderArrange(t *testing.T) {
	src := "cicada 2\ntempo 120\ntrack bass acid {}\npattern riff acid steps=4 { 1 . 5 . }\nscene main {\n  bass = riff\n  tempo = 84\n}\narrange {\n  place intro bass riff {\n    at = @1.1.1\n    length = 1bar\n  }\n}\n"
	_, diagnostics := Parse([]byte(src))
	var found bool
	for _, d := range diagnostics {
		if d.Code == "CICADA-UNSUPPORTED" && strings.Contains(d.Message, "scene main") && strings.Contains(d.Message, "tempo") && d.Position.Line == 7 {
			found = true
		}
	}
	if !found {
		t.Fatalf("want CICADA-UNSUPPORTED naming scene main at line 7, got %+v", diagnostics)
	}
}
