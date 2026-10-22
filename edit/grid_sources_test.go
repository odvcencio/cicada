package edit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
)

type ownedGridCase struct {
	name, owner, pattern string
	intent               edit.Intent
	check                func(*testing.T, edit.Pattern)
}

func ownedGridCases() []ownedGridCase {
	return []ownedGridCase{
		{"toggle", "lead.cicada", "line", &edit.ToggleStep{Entity: "step:line/1", Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if p.Data[1] == nil {
				t.Fatal("toggle lost note")
			}
		}},
		{"pitch", "lead.cicada", "line", &edit.SetPitch{Entity: "step:line/0", Pitch: 62, Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if p.Data[0].Note != 62 {
				t.Fatal("pitch lost")
			}
		}},
		{"accent", "lead.cicada", "line", &edit.ToggleModifier{Entity: "step:line/0", Modifier: "accent", Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if !p.Data[0].Accent {
				t.Fatal("accent lost")
			}
		}},
		{"chance", "lead.cicada", "line", &edit.CycleStep{Entity: "step:line/0", Field: "chance", Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if p.Data[0].Probability == 100 {
				t.Fatal("chance lost")
			}
		}},
		{"drum velocity", "drums.cicada", "beat", &edit.SetDrumVelocity{Entity: "step:beat/bd/0", Velocity: 80, Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if p.Lanes["bd"][0].Velocity == 100 {
				t.Fatal("velocity lost")
			}
		}},
		{"step", "lead.cicada", "line", &edit.SetStep{Entity: "step:line/1", Mode: "note", Pitch: 65, Ratchet: 2, Chance: 75, Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			n := p.Data[1]
			if n == nil || n.Note != 65 || n.Ratchet != 2 || n.Probability != 75 {
				t.Fatal("articulation lost", n)
			}
		}},
		{"settings", "lead.cicada", "line", &edit.SetPatternSettings{Entity: "pattern:line", Swing100: 5500, Gate: 65, Transpose: 12}, func(t *testing.T, p edit.Pattern) {
			if p.Transpose != 12 {
				t.Fatal("settings transpose lost")
			}
		}},
		{"range", "lead.cicada", "line", &edit.SetRange{Entity: "pattern:line", Operation: "transpose", First: 0, Last: 3, Amount: 2, Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if p.Data[0].Note != 62 || p.Data[4].Note != 64 {
				t.Fatal("range escaped selection")
			}
		}},
		{"resize", "lead.cicada", "line", &edit.ResizePattern{Entity: "pattern:line", Length: 12, Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if len(p.Data) != 12 || p.Data[0].Note != 60 || p.Data[11] != nil {
				t.Fatal("resize lost material")
			}
		}},
		{"chord", "keys.cicada", "harmony", &edit.SetChord{Entity: "step:harmony/0", Notes: []int{60, 64, 67}, Hold: 2, Shared: "pattern"}, func(t *testing.T, p edit.Pattern) {
			if !reflect.DeepEqual(p.Data[0].Notes, []int{60, 64, 67}) || !p.Data[1].Tie {
				t.Fatal("chord or hold lost", p.Data[0])
			}
		}},
	}
}

func gridSourcesFixture(newline string) (multiFileCompiler, []byte) {
	entry := []byte(strings.ReplaceAll("cicada 2\n// entry is not a pattern buffer\ntempo 120\nkey d minor\ntrack lead acid {}\ntrack keys piano {}\ntrack drums drums {}\nscene main { lead=line keys=harmony drums=beat }\nsong { main*2 }\n", "\n", newline))
	files := []notation.SourceFile{{Path: "main.cicada", Source: entry}}
	for _, f := range []struct{ path, text string }{
		{"lead.cicada", "// lead sentinel\npattern line acid steps=8 { c4 . . . e4 . . . } // keep lead\n"},
		{"keys.cicada", "// keys sentinel\npattern harmony notes steps=8 { c4 - . . g4 . . . } // keep keys\n"},
		{"drums.cicada", "// drums sentinel\npattern beat drums steps=8 { bd: x....... sd: ..x..... } // keep drums\n"},
	} {
		files = append(files, notation.SourceFile{Path: f.path, Source: []byte(strings.ReplaceAll(f.text, "\n", newline))})
	}
	return multiFileCompiler{path: "main.cicada", files: files}, entry
}

func TestGridEditsUseOwnedSourcesWithCompleteProjectContext(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, injected := range []bool{false, true} {
			for _, c := range ownedGridCases() {
				t.Run(c.name+fmt.Sprintf("/%q/host=%v", newline, injected), func(t *testing.T) {
					compiler, entry := gridSourcesFixture(newline)
					before, err := compiler.Compile(entry, nil)
					if err != nil {
						t.Fatal(err)
					}
					opts := edit.Options{Path: compiler.path, Edition: 2, Sources: compiler.files, Compiler: compiler}
					if injected {
						opts.ParseProject = compiler.parse
					}
					r, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: []edit.Intent{c.intent}}, opts)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(r.Source, entry) || len(r.Files) != 1 || r.Files[0].Path != c.owner {
						t.Fatal("edit escaped its owner", r)
					}
					for _, f := range compiler.files {
						if f.Path == c.owner {
							if !bytes.Equal(r.Files[0].Before, f.Source) || !bytes.Contains(r.Files[0].After, []byte("// keep ")) {
								t.Fatal("owner comments or original bytes lost")
							}
							if newline == "\r\n" && bytes.Contains(bytes.ReplaceAll(r.Files[0].After, []byte(newline), nil), []byte("\n")) {
								t.Fatal("CRLF lost")
							}
						}
					}
					found := false
					for i, p := range r.Plan.Patterns {
						if p.ID == c.pattern {
							found = true
							c.check(t, p)
						} else {
							original := before.Patterns[i]
							p.CopySource, p.CopyForTrack = nil, nil
							original.CopySource, original.CopyForTrack = nil, nil
							if !reflect.DeepEqual(p, original) {
								t.Fatal("changed another pattern", p.ID)
							}
						}
					}
					if !found {
						t.Fatal("lost edited pattern")
					}
				})
			}
		}
	}
}

func TestGridSourceBatchAccumulatesAndRefusesAtomically(t *testing.T) {
	c, entry := gridSourcesFixture("\r\n")
	opts := edit.Options{Path: c.path, Edition: 2, Sources: c.files, Compiler: c, ParseProject: c.parse}
	intents := []edit.Intent{&edit.SetPitch{Entity: "step:line/0", Pitch: 62, Shared: "pattern"}, &edit.SetChord{Entity: "step:harmony/0", Notes: []int{60, 64, 67}, Hold: 2, Shared: "pattern"}, &edit.ToggleStep{Entity: "step:beat/bd/1", Shared: "pattern"}, &edit.SetPitch{Entity: "step:line/4", Pitch: 67, Shared: "pattern"}}
	r, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: intents}, opts)
	if err != nil || r == nil || len(r.Files) != 3 || !bytes.Equal(r.Source, entry) {
		t.Fatal("batch lost a source", err)
	}
	for _, p := range r.Plan.Patterns {
		if p.ID == "line" && (p.Data[0].Note != 62 || p.Data[4].Note != 67) {
			t.Fatal("later edit lost earlier owner changes")
		}
	}
	intents = append(intents, &edit.SetPitch{Entity: "step:line/0", Pitch: 128, Shared: "pattern"})
	r, err = edit.Apply(entry, edit.Envelope{Version: 1, Intents: intents}, opts)
	if err == nil || r != nil {
		t.Fatal("invalid final edit produced a partial batch", err)
	}
	unchanged, original := gridSourcesFixture("\r\n")
	if !bytes.Equal(entry, original) || !reflect.DeepEqual(c.files, unchanged.files) {
		t.Fatal("batch mutated caller buffers")
	}
}

func TestGridSharedPoliciesKeepTheirOwnersAndOtherUses(t *testing.T) {
	for _, policy := range []string{"pattern", "detach", "definition"} {
		t.Run(policy, func(t *testing.T) {
			c, entry := gridSourcesFixture("\r\n")
			c.files[1].Source = []byte("// lead sentinel\r\npattern line acid steps=8 { use motif use motif +12 } // keep lead\r\n")
			c.files[2].Source = []byte("pattern harmony notes steps=4 { use motif }\r\n")
			c.files = append(c.files, notation.SourceFile{Path: "phrase.cicada", Source: []byte("// phrase stays shared\r\nphrase motif { c4 . e4 . }\r\n")})
			before := bytes.Clone(c.files[4].Source)
			r, err := edit.Apply(entry, edit.Envelope{Version: 1, Intents: []edit.Intent{&edit.SetPitch{Entity: "step:line/0", Pitch: 62, Shared: policy}}}, edit.Options{Path: c.path, Edition: 2, Sources: c.files, Compiler: c, ParseProject: c.parse})
			if err != nil {
				t.Fatal(err)
			}
			owner := "lead.cicada"
			if policy == "definition" {
				owner = "phrase.cicada"
			}
			if !bytes.Equal(r.Source, entry) || len(r.Files) != 1 || r.Files[0].Path != owner {
				t.Fatal("shared edit chose the wrong source", r)
			}
			for _, p := range r.Plan.Patterns {
				switch p.ID {
				case "line":
					last := uint8(72)
					if policy == "definition" {
						last = 74
					}
					if p.Data[0].Note != 62 || p.Data[4].Note != last {
						t.Fatal("shared use transpose or edited pitch lost", p.Data)
					}
				case "harmony":
					want := uint8(60)
					if policy == "definition" {
						want = 62
					}
					if p.Data[0].Note != want {
						t.Fatal("shared policy changed the wrong use")
					}
				}
			}
			if policy == "detach" && !bytes.Contains(r.Files[0].After, []byte("use motif +12")) {
				t.Fatal("detach expanded another use")
			}
			if !bytes.Equal(c.files[4].Source, before) {
				t.Fatal("edit mutated unrelated input")
			}
		})
	}
}

func TestGridOwnedSourceWritersProtectImportedLibraries(t *testing.T) {
	for _, c := range ownedGridCases() {
		t.Run(c.name, func(t *testing.T) {
			compiler, entry := gridSourcesFixture("\r\n")
			entry = []byte(strings.NewReplacer("lead=line", "lead=pack.line", "keys=harmony", "keys=pack.harmony", "drums=beat", "drums=pack.beat").Replace(string(entry)))
			compiler.files[0].Source = entry
			for i := 1; i < len(compiler.files); i++ {
				compiler.files[i].Library = "pack"
			}
			body, _ := json.Marshal(edit.Envelope{Version: 1, Intents: []edit.Intent{c.intent}})
			body = bytes.ReplaceAll(body, []byte("pattern:"+c.pattern+`"`), []byte("pattern:pack."+c.pattern+`"`))
			body = bytes.ReplaceAll(body, []byte("step:"+c.pattern+"/"), []byte("step:pack."+c.pattern+"/"))
			var env edit.Envelope
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatal(err)
			}
			r, err := edit.Apply(entry, env, edit.Options{Path: compiler.path, Edition: 2, Sources: compiler.files, Compiler: compiler, ParseProject: compiler.parse})
			if err == nil || r != nil || !strings.Contains(err.Error(), "local variation of library pattern") {
				t.Fatal("grid writer did not protect library", err)
			}
		})
	}
}
