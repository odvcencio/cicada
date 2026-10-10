package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/testwav"
)

func m4ClipSource() (string, []byte) {
	wav := testwav.Bytes(48000, 1, 16, 48000, 1)
	return strings.Replace(clipEditScore, strings.Repeat("a", 64), fmt.Sprintf("%x", sha256.Sum256(wav)), 1), wav
}

func TestM4SettingsWriterParity(t *testing.T) {
	clipSource, wav := m4ClipSource()
	for _, newline := range []string{"\n", "\r\n"} {
		for _, c := range []struct {
			name, source, raw string
			body              studioEdit
			old               func([]byte) ([]byte, error)
		}{
			{"scene set", automationScore, `{"kind":"setscenesetting","entity":"setting:main/bass.cutoff","value":"1.25kHz"}`, studioEdit{Action: "automation-set", Scene: "main", Path: "bass.cutoff"}, func(s []byte) ([]byte, error) {
				return sceneAutomationSource(s, "main", "bass.cutoff", json.RawMessage(`"1.25kHz"`), false, 1)
			}},
			{"scene insert", automationScore, `{"kind":"setscenesetting","entity":"setting:hold/bass.level","value":-6.25}`, studioEdit{Action: "automation-set", Scene: "hold", Path: "bass.level"}, func(s []byte) ([]byte, error) {
				return sceneAutomationSource(s, "hold", "bass.level", json.RawMessage(`-6.25`), false, 1)
			}},
			{"scene remove", automationScore, `{"kind":"removescenesetting","entity":"setting:main/bass.cutoff"}`, studioEdit{Action: "automation-remove", Scene: "main", Path: "bass.cutoff"}, func(s []byte) ([]byte, error) { return sceneAutomationSource(s, "main", "bass.cutoff", nil, true, 1) }},
			{"scene missing point", automationScore, `{"kind":"removescenesetting","entity":"setting:main/bass.pan"}`, studioEdit{}, func(s []byte) ([]byte, error) { return sceneAutomationSource(s, "main", "bass.pan", nil, true, 1) }},
			{"scene forbidden", automationScore, `{"kind":"setscenesetting","entity":"setting:main/bass.insert","value":"none"}`, studioEdit{}, func(s []byte) ([]byte, error) {
				return sceneAutomationSource(s, "main", "bass.insert", json.RawMessage(`"none"`), false, 1)
			}},
			{"project", "cicada 2\n// authored header\nkey db minor\ntempo // tempo comment\n138.0\ntrack bass acid {}\npattern p { 1 . c#3 . }\nscene main { bass=p }\nsong { main }\n", `{"kind":"setprojectsettings","title":"東京 \"Studio\"","tempomilli":127125,"root":"c#","scale":"minor"}`, studioEdit{Action: "project"}, func(s []byte) ([]byte, error) {
				return projectSettingsSource(s, &studioProjectSettings{Title: "東京 \"Studio\"", TempoMilli: 127125, Root: "c#", Scale: "minor"})
			}},
			{"project defaults", studioScore, `{"kind":"setprojectsettings","title":"new","tempomilli":120000,"root":"d","scale":"major"}`, studioEdit{Action: "project"}, func(s []byte) ([]byte, error) {
				return projectSettingsSource(s, &studioProjectSettings{Title: "new", TempoMilli: 120000, Root: "d", Scale: "major"})
			}},
			{"clip", clipSource, `{"kind":"setclipsettings","entity":"clip:hit","start":480,"end":4800,"gaindb":-2,"fadein":48,"fadeout":96}`, studioEdit{Action: "clip-settings", Pattern: "hit"}, func(s []byte) ([]byte, error) {
				return clipSettingsSource(s, "hit", &studioClipSettings{Start: 480, End: 4800, GainDB: -2, FadeIn: 48, FadeOut: 96}, 2)
			}},
			{"audio track", clipSource, `{"kind":"addaudiotrack","name":"vocal-b"}`, studioEdit{Action: "audio-track", NewName: "vocal-b"}, func(s []byte) ([]byte, error) { return newAudioTrackSource(s, "vocal-b", 2) }},
			{"duplicate track", clipSource, `{"kind":"addaudiotrack","name":"vox"}`, studioEdit{}, func(s []byte) ([]byte, error) { return newAudioTrackSource(s, "vox", 2) }},
		} {
			t.Run(c.name+newline, func(t *testing.T) {
				source := []byte(strings.ReplaceAll(c.source, "\n", newline))
				path := studioTestPath(t, string(source))
				if bytes.Contains(source, []byte("asset take")) {
					if err := os.WriteFile(filepath.Join(filepath.Dir(path), "take.wav"), wav, 0600); err != nil {
						t.Fatal(err)
					}
				}
				want, oldErr := c.old(source)
				got, err := applyParityIntent(t, source, c.raw, edits.Options{Compiler: &studioCompiler{path: path}})
				if oldErr != nil {
					if err == nil || err.Error() != oldErr.Error() {
						t.Fatalf("got %v, old %v", err, oldErr)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Source, want) || got.Label != studioEditLabel(c.body) {
					t.Fatalf("got %q / %q, want %q / %q", got.Source, got.Label, want, studioEditLabel(c.body))
				}
			})
		}
	}
}
