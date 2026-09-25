package project

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestDrumVoiceBudgetIncludesStoredLanesAndParams(t *testing.T) {
	for _, tc := range []struct {
		name, params, rows, extra string
		tracks, voices            int
		wantError                 bool
	}{
		{name: "legacy", tracks: 5, voices: 30},
		{name: "legacy_over_cap", tracks: 6, wantError: true},
		{name: "rest_only", rows: "lt: .; cy: .;", tracks: 5, voices: 30},
		{name: "added_hit", rows: "lt: x;", tracks: 3, voices: 21},
		{name: "added_parameter", params: "cy_decay=1500ms", tracks: 3, voices: 21},
		{name: "stored_scene", extra: "pattern next drums steps=1 { cy: x; }\nscene unused { d0=next }\n", tracks: 3, voices: 19},
		{name: "unused_pattern", extra: "pattern next drums steps=1 { cy: x; }\n", tracks: 3, voices: 18},
		{name: "eleven_lanes_over_cap", rows: "lt: x; mt: x; ht: x; cb: x; cy: x;", tracks: 3, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var source strings.Builder
			source.WriteString("cicada 1\n")
			for i := 0; i < tc.tracks; i++ {
				fmt.Fprintf(&source, "track d%d drums { %s }\n", i, tc.params)
			}
			fmt.Fprintf(&source, "pattern beat drums steps=1 { bd: x; %s }\n%s", tc.rows, tc.extra)
			source.WriteString("scene all {")
			for i := 0; i < tc.tracks; i++ {
				fmt.Fprintf(&source, " d%d=beat", i)
			}
			source.WriteString(" }\nsong { all }\n")
			score, ds := notation.Parse([]byte(source.String()))
			if len(ds) != 0 {
				t.Fatalf("parse: %+v", ds)
			}
			p, ds := FromScore(score)
			if tc.wantError {
				if p != nil || len(ds) != 1 || ds[0].Code != "CICADA-LIMIT" {
					t.Fatalf("over-cap score accepted or wrong error: %+v", ds)
				}
				return
			}
			if p == nil || len(ds) != 0 {
				t.Fatalf("validate: %+v", ds)
			}
			cfg, err := CompileEngine(p, 48_000, 128)
			if err != nil {
				t.Fatal(err)
			}
			cfg.MaxVoices = tc.voices
			if _, err := engine.New(cfg); err != nil {
				t.Fatalf("expected %d voices: %v", tc.voices, err)
			}
			cfg.MaxVoices--
			if _, err := engine.New(cfg); err == nil {
				t.Fatal("engine did not enforce the allocated voice count")
			}
		})
	}
}
