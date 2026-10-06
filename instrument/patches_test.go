package instrument

import (
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPatchLibraryCompilesToBoundedGraphs(t *testing.T) {
	seen := map[string]bool{}
	for _, patch := range Patches() {
		t.Run(patch.ID, func(t *testing.T) {
			if seen[patch.ID] {
				t.Fatal("duplicate patch identifier")
			}
			seen[patch.ID] = true
			source, err := patch.Source("test-voice")
			if err != nil {
				t.Fatal(err)
			}
			score, ds := notation.Parse([]byte("cicada 1\n" + source + "track keys test-voice {}\npattern p notes { c4 . }\nscene main { keys=p }\nsong { main }\n"))
			if len(ds) != 0 || score == nil {
				t.Fatalf("patch syntax: %+v", ds)
			}
			program, ds := Compile(score.Instruments[0])
			if len(ds) != 0 || program == nil {
				t.Fatalf("patch graph: %+v", ds)
			}
			if program.Mode != patch.Mode || len(program.Nodes) > 128 || program.StatefulNodes > 32 {
				t.Fatalf("unexpected patch bounds: %+v", program)
			}
			if _, err := Lower(program, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	if len(seen) != 6 {
		t.Fatalf("patches: %d", len(seen))
	}
}

func TestPatchSourceRejectsInjectedOrReservedNames(t *testing.T) {
	patch, ok := FindPatch("warm-pad")
	if !ok {
		t.Fatal("missing pad")
	}
	for _, name := range []string{"acid", "drums", "audio", "bad\ntrack injected acid {}", "<script>", "Upper", ""} {
		if _, err := patch.Source(name); err == nil {
			t.Fatalf("accepted unsafe or reserved name %q", name)
		}
	}
	copy := Patches()
	copy[0].ID = "changed"
	if _, ok := FindPatch("warm-pad"); !ok {
		t.Fatal("caller changed the library")
	}
}

func TestInstrumentParameterLiteralsFollowDeclaredUnits(t *testing.T) {
	patch, _ := FindPatch("warm-pad")
	source, _ := patch.Source("pad")
	score, _ := notation.Parse([]byte("cicada 1\n" + source + "track keys pad {}\npattern p notes { c4 }\nscene s { keys=p }\nsong { s }"))
	program, ds := Compile(score.Instruments[0])
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	for _, input := range [][3]string{{"brightness", "1250.125", "1250.125Hz"}, {"brightness", "1.25kHz", "1.25kHz"}, {"attack", "0.125s", "0.125s"}, {"attack", "7.25", "7.25ms"}, {"detune", "0.00525", "0.00525"}, {"sustain", "75%", "75%"}} {
		got, err := ParameterLiteral(program, input[0], input[1])
		if err != nil || got != input[2] {
			t.Fatalf("parameter %+v: %q %v", input, got, err)
		}
	}
	for _, input := range [][2]string{{"brightness", "12ms"}, {"release", "NaN"}, {"release", "Infinity"}, {"brightness", "1e100Hz"}, {"brightness", "NaNHz"}, {"detune", "1; track injected acid {}"}, {"unknown", "1"}} {
		if _, err := ParameterLiteral(program, input[0], input[1]); err == nil {
			t.Fatalf("accepted invalid parameter %+v", input)
		}
	}
}
