package benchscore_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/benchscore"
	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The pinned bytes of every reference. A baseline compares with a later run
// only on the same bytes, so a change here needs a stated reason and a new
// baseline (the edit benchmark prints these hashes with its table).
var referencePins = map[string]struct {
	sha256 string
	bytes  int
}{
	benchscore.Small:     {"fd69cc1ac390aae5be65aa9925d1bcaefe668d034586f0ae80e3bdd01432a802", 567},
	benchscore.Placement: {"1c52e4d5ec110237a6652ae6ae99f825469cbfa6ea26a6da95fa86a4cd3306bf", 2596},
	benchscore.LargeName: {"84396bf66e78f70bf4dfc2ae4790a0b22cba96546e8857214ff272f1647630bb", 308844},
}

func TestReferencesArePinned(t *testing.T) {
	references, err := benchscore.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range references {
		sum := sha256.Sum256(reference.Source)
		got := hex.EncodeToString(sum[:])
		pin := referencePins[reference.Name]
		if got != pin.sha256 || len(reference.Source) != pin.bytes {
			t.Errorf("%s is %d bytes with SHA-256 %s; pinned %d bytes, %s. A changed reference makes every baseline incomparable: restore it, or state why it changed, update the pin and post a new baseline", reference.Name, len(reference.Source), got, pin.bytes, pin.sha256)
		}
	}
}

func TestLargeIsTheSameEveryCall(t *testing.T) {
	first := benchscore.Large()
	first[0] ^= 1 // a caller's copy must not reach the next caller
	if second := benchscore.Large(); string(second) == string(first) || second[0] != '/' {
		t.Fatal("Large returned shared bytes")
	}
}

// largeShape counts what section E.1 of the revision lists.
type largeShape struct {
	bytes, lines, declarations                       int
	kinds                                            map[string]int
	performanceNotes, longestPerformance, placements int
	markers, automationPoints                        int
	longestName                                      string
}

func shapeOf(t *testing.T, source []byte) largeShape {
	t.Helper()
	root, w, err := notation.ParseTree(source)
	if err != nil {
		t.Fatal(err)
	}
	shape := largeShape{bytes: len(source), lines: strings.Count(string(source), "\n"), kinds: map[string]int{}}
	count := func(n *gts.Node, kind string) int {
		total := 0
		for i := 0; i < n.NamedChildCount(); i++ {
			if w.Type(n.NamedChild(i)) == kind {
				total++
			}
		}
		return total
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		kind := w.Type(n)
		if kind == "comment" || kind == "integer" {
			continue
		}
		shape.declarations++
		shape.kinds[kind]++
		switch kind {
		case "performance_pattern":
			notes := count(n, "performed_note")
			shape.performanceNotes += notes
			if notes > shape.longestPerformance {
				shape.longestPerformance = notes
				shape.longestName = w.Text(w.Field(n, "name"))
			}
		case "arrange_decl":
			shape.placements += count(n, "place_decl")
			shape.markers += count(n, "marker_decl")
		case "automate_decl":
			shape.automationPoints += count(n, "automation_point")
		}
	}
	return shape
}

// REF-L has the size and the shape of the largest production (section E.1):
// within 1% of its 309,498 bytes, and its counts of tracks, patterns,
// performances, notes, drum patterns, scenes, placements, effects,
// instruments and automation lanes.
func TestLargeHasTheShapeOfTheLargestProduction(t *testing.T) {
	shape := shapeOf(t, benchscore.Large())
	if shape.bytes < 306_403 || shape.bytes > 312_593 {
		t.Errorf("REF-L is %d bytes; want 309,498 within 1%% (306,403 to 312,593)", shape.bytes)
	}
	patterns := shape.kinds["note_pattern"] + shape.kinds["acid_pattern"] + shape.kinds["drum_pattern"] + shape.kinds["performance_pattern"]
	for _, check := range []struct {
		what      string
		got, want int
	}{
		{"tracks", shape.kinds["track_decl"], 22},
		{"patterns", patterns, 98},
		{"performances", shape.kinds["performance_pattern"], 43},
		{"notes in performances", shape.performanceNotes, 2267},
		{"drum patterns", shape.kinds["drum_pattern"], 6},
		{"scenes", shape.kinds["scene_decl"], 0},
		{"arrange blocks", shape.kinds["arrange_decl"], 1},
		{"placements", shape.placements, 226},
		{"markers", shape.markers, 9},
		{"effects", shape.kinds["fx_decl"], 45},
		{"instruments", shape.kinds["instrument_decl"], 14},
		{"automation lanes", shape.kinds["automate_decl"], 18},
		{"automation points", shape.automationPoints, 168},
		{"kits", shape.kinds["kit_decl"], 2},
		// 215 in the production, less its 8 samplers and 1 asset.
		{"top-level declarations", shape.declarations, 206},
		{"notes in the longest performance (the most one may hold)", shape.longestPerformance, 512},
	} {
		if check.got != check.want {
			t.Errorf("REF-L has %d %s; want %d", check.got, check.what, check.want)
		}
	}
	if shape.longestName != benchscore.LargePerformance {
		t.Errorf("the 512-note performance is %q; benchscore.LargePerformance names %q", shape.longestName, benchscore.LargePerformance)
	}
	t.Logf("REF-L: %d bytes, %d lines, %d declarations", shape.bytes, shape.lines, shape.declarations)
}

// Every reference is a valid score: it compiles the way cicada check and
// Studio compile it (project sources, parse, semantic model, project
// validation, the schedule rules and the engine), with no error. Under
// the portable limits apply (16 tracks), REF-L, with the production's 22
// tracks, must be refused; the score engine uses the native limits instead.
func TestReferencesCompileWithoutErrors(t *testing.T) {
	references, err := benchscore.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range references {
		t.Run(reference.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "score.cicada")
			if err := os.WriteFile(path, reference.Source, 0o600); err != nil {
				t.Fatal(err)
			}
			sources, err := project.ReadSources(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			score, diagnostics := sources.Parse()
			var p *project.Project
			if score != nil {
				var more []notation.Diagnostic
				p, more = project.FromScore(score)
				diagnostics = append(diagnostics, more...)
			}
			var errors []string
			for _, d := range diagnostics {
				if d.Severity == "error" {
					errors = append(errors, d.Error())
				}
			}
			if limits.MaxTracks == limits.PortableMaxTracks && reference.Name == benchscore.LargeName {
				if len(errors) == 0 || !strings.Contains(strings.Join(errors, "\n"), "score must have 1 to 16 tracks") {
					t.Fatalf("under portable limits REF-L should be refused for its 22 tracks; got %q", errors)
				}
				return
			}
			if len(errors) > 0 || p == nil {
				t.Fatalf("%s does not compile:\n%s", reference.Name, strings.Join(errors, "\n"))
			}
			if err := schedule.Validate(p, sources.Root, 48000, 128, schedule.Options{}); err != nil {
				t.Fatal(err)
			}
			if _, err := project.CompileEngine(p, 48000, 128); err != nil {
				t.Fatal(err)
			}
			for _, d := range diagnostics {
				t.Logf("%s: %s", reference.Name, d.Error())
			}
		})
	}
}

// CICADA_WRITE_REFERENCES=<folder> writes the three references there, for
// cicada check, Studio's browser benchmark and parser profiles.
func TestWriteReferences(t *testing.T) {
	folder := os.Getenv("CICADA_WRITE_REFERENCES")
	if folder == "" {
		t.Skip("set CICADA_WRITE_REFERENCES to a folder to write the references")
	}
	references, err := benchscore.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range references {
		if err := os.WriteFile(filepath.Join(folder, reference.Name+".cicada"), reference.Source, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
