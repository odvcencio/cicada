package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// kitModScore is baobab's lab score: a kit lane plays an instrument that
// reads mod(), which kits do not support.
const kitModScore = `cicada 2
tempo 120
lfo wob { wave = sample-hold rate = 1/16 }
instrument kk {
  voice mono {
    let e = env(gate, 200ms)
    out = sine(pitch * (1 + 2 * mod(wob))) * e * velocity * 0.4
  }
}
kit lab { bd = kk }
track k lab {}
pattern beat drums { bd: x..x }
scene main { k = beat }
song { main*4 }
`

// TestKitModulationErrorHasOneCode is baobab's finding: the error printed
// "CICADA-PARAM: CICADA-UNSUPPORTED: graph modulators ...". It now has one
// code, the one that names the cause, and names the kit, lane and instrument.
func TestKitModulationErrorHasOneCode(t *testing.T) {
	score, ds := notation.Parse([]byte(kitModScore))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	_, ds = FromScore(score)
	if len(ds) == 0 {
		t.Fatal("a kit lane that reads mod() compiled")
	}
	d := ds[0]
	if d.Code != "CICADA-UNSUPPORTED" || strings.HasPrefix(d.Message, "CICADA-") || strings.Count(d.Error(), "CICADA-") != 1 || !strings.HasPrefix(d.Message, "kit lab lane bd plays instrument kk, which reads mod();") {
		t.Fatalf("diagnostic %+v prints %q", d, d.Error())
	}
}

// TestNoDiagnosticCarriesTwoCodes compiles every score under examples/ and
// testdata/ (the invalid ones included) and fails when a diagnostic's message
// still starts with a code of its own.
func TestNoDiagnosticCarriesTwoCodes(t *testing.T) {
	var paths []string
	for _, root := range []string{"../examples", "../testdata"} {
		filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".cicada") {
				paths = append(paths, path)
			}
			return nil
		})
	}
	if len(paths) < 50 {
		t.Fatalf("found only %d scores", len(paths))
	}
	failing := 0
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		score, ds := notation.Parse(source)
		if score != nil && !hasErrors(ds) {
			var compiled []notation.Diagnostic
			_, compiled = FromScore(score)
			ds = append(ds, compiled...)
		}
		for _, d := range ds {
			if d.Severity == "error" {
				failing++
			}
			if strings.HasPrefix(d.Message, "CICADA-") {
				t.Errorf("%s: %q carries two codes", path, d.Error())
			}
		}
	}
	if failing == 0 {
		t.Fatal("no score reported an error; the invalid corpus is missing")
	}
}

// TestVelocityRowLengthPointsAtTheRow is banyan's finding: a velocity row one
// value short reported at the place line of the arrangement, with neither the
// pattern nor the counts. It now reports at the velocity row, names the
// pattern and gives both counts, with one code.
func TestVelocityRowLengthPointsAtTheRow(t *testing.T) {
	for _, tc := range []struct {
		row    string
		column int
		counts string
	}{
		{"  velocity: 90 80 70 60\n", 22, "velocity row has 4 values, the note row expands to 5 cells"},
		{"  velocity: 90 80 70 60 50 40\n", 28, "velocity row has 6 values, the note row expands to 5 cells"},
	} {
		source := "cicada 2\ntempo 120\ntrack piano acid {}\npattern piano-a {\n  c3 d3 e3 f3 g3\n" + tc.row + "}\narrange {\n  place piano-a-1 piano piano-a { at = @1.1.1 length = 2bars }\n}\n"
		score, ds := notation.Parse([]byte(source))
		if hasErrors(ds) {
			t.Fatal(ds)
		}
		_, ds = FromScore(score)
		if len(ds) != 1 {
			t.Fatalf("%q: %v", tc.row, ds)
		}
		d := ds[0]
		want := "pattern piano-a: " + tc.counts + "; the velocity row must match the expanded pattern length"
		if d.Code != "CICADA-ROW" || d.Message != want || d.Position.Line != 6 || d.Position.Column != tc.column {
			t.Fatalf("%q: %s", tc.row, d.Error())
		}
	}
}

// TestSymlinkedAssetNamesTheCause is ebony's finding: an asset under a folder
// that is a symlink to another project failed with "path escapes from
// parent", which names neither the symlink nor the remedy. The message now
// names the symlink, the copy or hard-link remedy and the pack documentation.
func TestSymlinkedAssetNamesTheCause(t *testing.T) {
	dir, _, score := assetFixture(t)
	elsewhere := t.TempDir()
	if err := os.Rename(filepath.Join(dir, "vocal.wav"), filepath.Join(elsewhere, "vocal.wav")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "ir")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	score.Assets[0].Path = "ir/vocal.wav"
	ds := VerifyAssets(score, dir)
	if len(ds) != 1 || ds[0].Code != "CICADA-ASSET-PATH" {
		t.Fatalf("%+v", ds)
	}
	for _, want := range []string{"ir/vocal.wav goes through the symlink ir, which leads outside the project folder", "cp -al", "docs/sampler/packs.md"} {
		if !strings.Contains(ds[0].Message, want) {
			t.Fatalf("message %q lacks %q", ds[0].Message, want)
		}
	}
}
