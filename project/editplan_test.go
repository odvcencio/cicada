package project

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

func TestEditPlanMirrorsSourceOrderAndStepTicks(t *testing.T) {
	source, err := os.ReadFile("../examples/arrangement/named.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.ParseEdition(source, 2)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	plan := EditPlan(p, source)
	if plan.Revision != edit.Revision(source) || len(plan.Placements) != 2 || plan.Placements[1].ID != "return" || plan.Placements[1].AtTick != 2*3840 {
		t.Fatalf("%+v", plan)
	}
	if got := plan.Patterns[0].LengthTicks(); got != 4*240 {
		t.Fatalf("pulse length ticks %d", got)
	}
	if plan.ResolveParam == nil {
		t.Fatal("ResolveParam missing")
	}
	if _, err := plan.ResolveParam("bass.level"); err != nil {
		t.Fatal(err)
	}
	if !plan.Names["bass"] || !plan.Names["pulse"] {
		t.Fatalf("names %v", plan.Names)
	}
}

func TestEditPlanNamesIncludeEveryAuthoredDeclaration(t *testing.T) {
	for _, c := range []struct {
		kind, declaration string
	}{
		{"preset", "preset captured { instrument=acid cutoff=900Hz }"},
		{"instrument", "instrument captured { voice mono { out=sine(pitch) } }"},
		{"kit", "kit captured { bd=builtin.bd }"},
		{"sampler", "sampler captured { asset=wave root=c4 mode=oneshot voices=4 }"},
		{"track", "track captured acid {}"},
		{"pattern", "pattern captured acid steps=4 { c4 . . . }"},
		{"phrase", "phrase captured { c4 . . . }"},
		{"scene", "scene captured { bass=pulse }"},
		{"clip", "clip captured wave { start=0frames end=480frames }"},
		{"asset", "asset captured \"recorded.wav\" { sha256=\"" + strings.Repeat("b", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }"},
		{"bus", "bus music {}"},
		{"effect", "fx captured drive {}"},
		{"export", "export captured { rate=48000Hz bits=24 normalize=off }"},
		{"import", "import \"demo/captured\""},
		{"placement", "arrange { place captured bass pulse { at=@1.1.1 length=1bar } }"},
		{"marker", "arrange { place intro bass pulse { at=@1.1.1 length=1bar } marker captured { at=@1.1.1 } }"},
	} {
		for _, location := range []string{"entry", "part"} {
			t.Run(c.kind+"/"+location, func(t *testing.T) {
				base := "asset wave \"recorded.wav\" { sha256=\"" + strings.Repeat("a", 64) + "\" format=wav frames=480 rate=48000Hz channels=1 }\ntrack bass acid {}\npattern pulse acid steps=4 { c4 . . . }\nscene main { bass=pulse }\nsong { main }\n"
				if c.kind == "placement" || c.kind == "marker" {
					base = strings.Replace(base, "song { main }\n", "", 1)
				}
				source := []byte("cicada 2\n" + base)
				bindings := map[string]string{}
				if c.kind == "import" {
					bindings["captured"] = "demo.captured"
				}
				files := []notation.SourceFile{{Path: "main.cicada", Source: source, Bindings: bindings}}
				if location == "entry" {
					source = []byte("cicada 2\n" + c.declaration + "\n" + base)
					files[0].Source = source
				} else {
					files = append(files, notation.SourceFile{Path: "part.cicada", Source: []byte(c.declaration + "\n"), Bindings: bindings})
				}
				score, ds := notation.ParseFiles(files, 2)
				if score == nil || hasErrors(ds) {
					t.Fatalf("invalid %s fixture: %v", c.kind, ds)
				}
				p, ds := FromScore(score)
				if p == nil || hasErrors(ds) {
					t.Fatalf("invalid %s project: %v", c.kind, ds)
				}
				plan := EditPlan(p, source)
				if location == "part" {
					plan = EditPlan(p, source, files...)
				}
				name := "captured"
				if c.kind == "bus" {
					name = "music"
				}
				if !plan.Names[name] {
					t.Fatalf("%s name is not occupied", c.kind)
				}
			})
		}
	}
}

func TestEditPlanNamesKeepLibraryScopesAndUnusedTemplates(t *testing.T) {
	source := []byte("cicada 2\nimport \"demo/tone\"\ntrack bass acid {}\npattern pulse acid steps=4 { c4 . . . }\nscene main { bass=pulse }\nsong { main }\n")
	files := []notation.SourceFile{
		{Path: "main.cicada", Source: source, Bindings: map[string]string{"tone": "demo.tone"}},
		{Path: "voice.cicada", Library: "demo/tone", Edition: 2, Source: []byte("instrument captured { voice mono { out=sine(pitch) } }\npreset bright { instrument=acid cutoff=900Hz }\nfx unused delay {}\nphrase motif { c4 . . . }\n")},
	}
	score, ds := notation.ParseFiles(files, 2)
	if score == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	// The imported effect is deliberately omitted from the runtime project.
	if len(p.Effects) != 0 {
		t.Fatal("unused library effect became a runtime instance")
	}
	plan := EditPlan(p, source, files...)
	for _, name := range []string{"tone", "demo.tone.captured", "demo.tone.bright", "demo.tone.unused", "demo.tone.motif"} {
		if !plan.Names[name] {
			t.Errorf("authored name %s is not occupied", name)
		}
	}
	for _, name := range []string{"captured", "bright", "unused", "motif"} {
		if plan.Names[name] {
			t.Errorf("library name %s leaked into project scope", name)
		}
	}
}

func TestEditPlanEntityIndexFollowsRecompile(t *testing.T) {
	base, err := os.ReadFile("../examples/arrangement/named.cicada")
	if err != nil {
		t.Fatal(err)
	}
	line := "  place lead-in bass pulse { at = @6.1.1 length = 1bar }\n"
	last := bytes.Replace(base, []byte("  marker chorus"), []byte(line+"  marker chorus"), 1)
	first := bytes.Replace(base, []byte("arrange {\n"), []byte("arrange {\n"+line), 1)
	compile := func(source []byte) *edit.Plan {
		t.Helper()
		score, ds := notation.ParseEdition(source, 2)
		if hasErrors(ds) {
			t.Fatal(ds)
		}
		p, ds := FromScore(score)
		if p == nil || hasErrors(ds) {
			t.Fatal(ds)
		}
		return EditPlan(p, source)
	}
	for _, c := range []struct {
		name       string
		source     []byte
		ret, intro int
	}{{"base", base, 1, 0}, {"appended", last, 1, 0}, {"prepended", first, 2, 1}} {
		plan := compile(c.source)
		if len(plan.Placements) != map[bool]int{true: 2, false: 3}[c.name == "base"] {
			t.Fatalf("%s placements %d", c.name, len(plan.Placements))
		}
		ret, err := edit.Resolve(plan, "placement:return")
		intro, err2 := edit.Resolve(plan, "placement:intro")
		if err != nil || err2 != nil || ret.Index != c.ret || intro.Index != c.intro {
			t.Fatalf("%s: return %+v intro %+v %v %v", c.name, ret, intro, err, err2)
		}
	}
}
