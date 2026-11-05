package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestStudioChorusCreateEditAndInsert(t *testing.T) {
	handler, path := studioMixerTestHandler(t, studioMixerScore)
	revision := func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return studioRevision(b)
	}
	apply := func(name string, value any) {
		t.Helper()
		r := studioCall(t, handler, "/api/mixer", map[string]any{"revision": revision(), "path": name, "value": value})
		if r.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, r.Code, r.Body.String())
		}
	}
	apply("fx.wide", "chorus")
	apply("wide.mix", .7)
	apply("bass.insert", "grit -> wide")
	response := studioCall(t, handler, "/api/mixer", nil)
	var view studioMixerView
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, effect := range view.Effects {
		if effect.Name == "wide" {
			for _, field := range effect.Fields {
				if field.Path == "wide.mix" && field.Supported {
					found = true
				}
			}
		}
	}
	if !found || !view.Tracks[0].InsertOK || !strings.Contains(strings.Join(view.Tracks[0].Inserts, " "), "wide") {
		t.Fatalf("chorus controls or insert missing: %+v", view.Tracks[0])
	}
}
