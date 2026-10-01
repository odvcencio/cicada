package project

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestArrangementJSONRequiresNestedFields(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntrack bass acid {}\npattern hit { c3 }\narrange { place p bass hit { at = 960ticks length = 240ticks } marker m { at = 1920ticks } }\n"))
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(data); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ construct, field, pointer string }{
		{"arrangement", "placements", "/arrange/placements"},
		{"arrangement", "markers", "/arrange/markers"},
		{"placement", "id", "/arrange/placements/0/id"},
		{"placement", "track", "/arrange/placements/0/track"},
		{"placement", "content", "/arrange/placements/0/content"},
		{"placement", "at_tick", "/arrange/placements/0/at_tick"},
		{"placement", "length_ticks", "/arrange/placements/0/length_ticks"},
		{"marker", "id", "/arrange/markers/0/id"},
		{"marker", "at_tick", "/arrange/markers/0/at_tick"},
	} {
		t.Run(tc.pointer, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(data, &root); err != nil {
				t.Fatal(err)
			}
			object := root["arrange"].(map[string]any)
			if tc.construct == "placement" {
				object = object["placements"].([]any)[0].(map[string]any)
			} else if tc.construct == "marker" {
				object = object["markers"].([]any)[0].(map[string]any)
			}
			delete(object, tc.field)
			broken, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeJSON(broken)
			var diagnostic *JSONError
			if !errors.As(err, &diagnostic) || diagnostic.Code != "CICADA-PARAM" || diagnostic.Pointer != tc.pointer || !strings.Contains(diagnostic.Error(), "missing "+tc.field) {
				t.Fatalf("missing %s accepted or unclear diagnostic: %+v", tc.pointer, err)
			}
		})
	}
}
