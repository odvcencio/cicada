package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestArrangementJSONIdentifierRoundTrip(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntrack bass acid {}\npattern hit { c3 }\narrange { place p bass hit { at = 960ticks length = 240ticks } marker m { at = 1920ticks } }\n"))
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"placement", "marker"} {
		for _, id := range []string{"two words", "Uppercase", "1number", "has.dot", "é", "_intro-2", "a", strings.Repeat("a", 64)} {
			t.Run(kind+"/"+id, func(t *testing.T) {
				var root map[string]any
				if err := json.Unmarshal(data, &root); err != nil {
					t.Fatal(err)
				}
				root["arrange"].(map[string]any)[kind+"s"].([]any)[0].(map[string]any)["id"] = id
				raw, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err := DecodeJSON(data)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "placement" {
					candidate.Arrange.Placements[0].ID = id
				} else {
					candidate.Arrange.Markers[0].ID = id
				}
				canonical, encodeErr := CanonicalJSON(candidate)
				decoded, decodeErr := DecodeJSON(raw)
				if !validID(id) {
					for _, diagnostic := range []error{encodeErr, decodeErr} {
						if diagnostic == nil || !strings.Contains(diagnostic.Error(), kind) || !strings.Contains(diagnostic.Error(), "ID") {
							t.Errorf("invalid %s ID %q accepted or unclear diagnostic: %v", kind, id, diagnostic)
						}
					}
					return
				}
				if encodeErr != nil || decodeErr != nil {
					t.Fatalf("valid ID rejected: encode=%v decode=%v", encodeErr, decodeErr)
				}
				source, err := ToSource(decoded)
				if err != nil {
					t.Fatal(err)
				}
				score, ds := notation.Parse(source)
				if hasErrors(ds) {
					t.Fatal(ds)
				}
				again, ds := FromScore(score)
				if again == nil || hasErrors(ds) {
					t.Fatal(ds)
				}
				roundTrip, err := CanonicalJSON(again)
				if err != nil || !bytes.Equal(canonical, roundTrip) {
					t.Fatalf("%s ID %q changed during JSON/source round trip: %v", kind, id, err)
				}
			})
		}
	}
}

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
