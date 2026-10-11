package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func m5RouteCases() []m4RouteCase {
	var cases []m4RouteCase
	add := func(route, name, source string, body studioEdit, status int) {
		cases = append(cases, m4RouteCase{name: route[5:] + "/" + name, route: route, source: source, body: body, wantStatus: status})
	}
	for _, preset := range []string{"warm-pad", "poly-brass"} {
		add("/api/instrument", preset, presetEditScore, studioEdit{Action: "add-preset", Pattern: preset, NewName: "voice-b", Track: "keys"}, 200)
	}
	for _, c := range []struct {
		name string
		body studioEdit
	}{
		{"unknown preset", studioEdit{Action: "add-preset", Pattern: "ghost", NewName: "voice-b", Track: "keys"}},
		{"duplicate track", studioEdit{Action: "add-preset", Pattern: "warm-pad", NewName: "voice-b", Track: "bass"}},
		{"invalid name", studioEdit{Action: "add-preset", Pattern: "warm-pad", NewName: "voice-b", Track: "BAD"}},
	} {
		add("/api/instrument", c.name, presetEditScore, c.body, 422)
	}
	notes := []studioTakeNote{{Tick: 0, EndTick: 180, Note: 60, Velocity: 90}, {Tick: 120, EndTick: 240, Note: 64, Velocity: 100}}
	drums := []studioTakeNote{{Tick: 120, EndTick: 120, Note: 36, Velocity: 80}, {Tick: 120, EndTick: 120, Note: 38, Velocity: 127}}
	expr := []studioTakeNote{{Tick: 0, EndTick: 360, Note: 60, Velocity: 96, Expressions: []studioTakeExpression{{Tick: 0, PitchCents: 50, Pressure: .25, Timbre: .75}, {Tick: 120, PitchCents: 100, Pressure: .5, Timbre: .25}, {Tick: 240, PitchCents: -50, Pressure: 1, Timbre: 1}}}}
	add("/api/record", "acid slide", studioScore, studioEdit{Track: "bass", Pattern: "pulse", Take: notes}, 200)
	add("/api/record", "simultaneous drums", studioScore, studioEdit{Track: "drums", Pattern: "beat", Take: drums}, 200)
	add("/api/record", "expression", studioScore, studioEdit{Track: "bass", Pattern: "pulse", Take: expr}, 200)
	add("/api/record", "expression comments", strings.Replace(studioScore, "{ 1 . 5 . }", "{ 1 . 5 .\n bend: 10ct . . . // keep\n}", 1), studioEdit{Track: "bass", Pattern: "pulse", Take: expr}, 200)
	add("/api/record", "multiple tracks", studioScore, studioEdit{Recordings: []studioTakeRecording{{Track: "bass", Pattern: "pulse", Notes: notes}, {Track: "drums", Pattern: "beat", Notes: drums}}}, 200)
	add("/api/record", "empty", studioScore, studioEdit{}, 400)
	add("/api/record", "unknown track", studioScore, studioEdit{Track: "ghost", Pattern: "pulse", Take: notes}, 422)
	add("/api/record", "wrong track", studioScore, studioEdit{Track: "bass", Pattern: "beat", Take: notes}, 422)
	add("/api/record", "invalid note", studioScore, studioEdit{Track: "bass", Pattern: "pulse", Take: []studioTakeNote{{Note: 128, Velocity: 90}}}, 422)
	add("/api/record", "invalid expression", studioScore, studioEdit{Track: "bass", Pattern: "pulse", Take: []studioTakeNote{{Note: 60, Velocity: 90, Expressions: []studioTakeExpression{{Pressure: 2}}}}}, 422)
	for _, kind := range []string{"tine_ep", "organ_full", "fm_bass"} {
		add("/api/record", kind, string(recordedKeysSource(kind)), studioEdit{Track: "part", Pattern: "take", Take: []studioTakeNote{{Note: 60, Velocity: 90, EndTick: 60}}}, 200)
	}
	for _, layout := range []string{"loose", "manifest", "inherited"} {
		source := "cicada 2\n" + studioScore
		edition := 0
		if layout != "loose" {
			edition = 2
		}
		if layout == "inherited" {
			source = strings.TrimPrefix(source, "cicada 2\n")
		}
		cases = append(cases, m4RouteCase{name: "record/" + layout, route: "/api/record", source: source, edition: edition, body: studioEdit{Track: "bass", Pattern: "pulse", Take: notes}, wantStatus: 200})
		cases = append(cases, m4RouteCase{name: "instrument/" + layout, route: "/api/instrument", source: source, edition: edition, body: studioEdit{Action: "add-preset", Pattern: "warm-pad", NewName: "voice-b", Track: "keys"}, wantStatus: 200})
	}
	for _, item := range []studioLibraryItem{{Path: "std/synth", Name: "glassbass"}, {Path: "std/fx", Name: "drive"}, {Path: "std/fx", Name: "delay"}, {Path: "std/fx", Name: "reverb"}, {Path: "std/fx", Name: "comp"}, {Path: "std/presets", Name: "acid-bite"}} {
		for _, track := range []string{"lead", "extra"} {
			add("/api/library/insert", item.Name+"/"+track, studioLibraryScore, studioEdit{Path: item.Path, Item: item.Name, Track: track}, 200)
		}
	}
	add("/api/library/insert", "invalid track", studioLibraryScore, studioEdit{Path: "std/fx", Item: "drive", Track: "BAD"}, 422)
	add("/api/library/insert", "missing item", studioLibraryScore, studioEdit{Path: "std/fx", Item: "ghost", Track: "lead"}, 422)
	add("/api/library/save", "values", studioLibraryScore, studioEdit{Track: "lead", Name: "bright"}, 200)
	add("/api/library/save", "defaults", strings.Replace(studioLibraryScore, "cutoff=900Hz pan=0.2", "cutoff=0.54kHz pan=0 mute=off", 1), studioEdit{Track: "lead", Name: "defaults"}, 200)
	add("/api/library/save", "invalid name", studioLibraryScore, studioEdit{Track: "lead", Name: "_private"}, 422)
	add("/api/library/save", "missing track", studioLibraryScore, studioEdit{Track: "ghost", Name: "bright"}, 422)
	var expanded []m4RouteCase
	for _, c := range cases {
		for _, newline := range []string{"\n", "\r\n"} {
			v := c
			v.source = strings.ReplaceAll(c.source, "\n", newline)
			if newline == "\n" {
				v.name += "/lf"
			} else {
				v.name += "/crlf"
			}
			expanded = append(expanded, v)
		}
	}
	return expanded
}

func TestM5RouteParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	golden := map[string]gridPatternRouteSnapshot{}
	data, err := os.ReadFile("testdata/m5-route-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	cases := m5RouteCases()
	if len(golden) != len(cases) {
		t.Fatalf("%d captures, %d cases", len(golden), len(cases))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := m4Route(t, c)
			assertM4Capture(t, c, got)

			want, ok := golden[c.name]
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("route parity\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}
