package migration

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestFixSourcePreservesMeaningAcrossConciseNotation(t *testing.T) {
	source := []byte("cicada 1\n// keep this comment\ninstrument tone { param cutoff: hz = 720Hz voice mono { out = saw(cutoff) } }\ntrack bass acid {}\npattern riff acid steps=2 { 1 . }\nscene main { bass = riff }\nscene hold { bass = keep }\nscene quiet { bass = off }\nsong { main hold quiet }\n")
	fixed, changed, err := FixSource(source)
	if err != nil || !changed {
		t.Fatalf("fix failed: %v", err)
	}
	for _, old := range [][]byte{[]byte("cicada 1"), []byte("param cutoff:"), []byte("pattern riff acid"), []byte("steps="), []byte("bass = keep"), []byte("bass = off")} {
		if bytes.Contains(fixed, old) {
			t.Fatalf("legacy spelling remains: %s", old)
		}
	}
	if !bytes.Contains(fixed, []byte("// keep this comment")) || !bytes.Contains(fixed, []byte("bass = stop")) {
		t.Fatalf("comment or stop action was lost: %s", fixed)
	}
	again, changed, err := FixSource(fixed)
	if err != nil || changed || !bytes.Equal(fixed, again) {
		t.Fatalf("fix is not stable: %v", err)
	}
}

func TestFixPreservesSceneParameterPathsAndSemanticEquality(t *testing.T) {
	source := []byte("track bass acid { cutoff = 700Hz }\npattern riff acid steps=1 { 1 }\nscene drop { bass = riff bass.cutoff = 900Hz // keep the scene value\n}\nsong { drop }\n")
	beforeScore, beforeDiagnostics := notation.Parse(source)
	if hasDiagnosticErrors(beforeDiagnostics) {
		t.Fatalf("source parse: %+v", beforeDiagnostics)
	}
	before, beforeDiagnostics := project.FromScore(beforeScore)
	if before == nil || hasDiagnosticErrors(beforeDiagnostics) {
		t.Fatalf("source project: %+v", beforeDiagnostics)
	}
	fixed, changed, err := FixSource(source)
	if err != nil || !changed {
		t.Fatalf("fix failed: changed=%v err=%v", changed, err)
	}
	if !bytes.Contains(fixed, []byte("bass.cutoff = 900Hz // keep the scene value")) {
		t.Fatalf("fix rewrote or dropped the P1 scene setting: %s", fixed)
	}
	afterScore, afterDiagnostics := notation.Parse(fixed)
	if hasDiagnosticErrors(afterDiagnostics) {
		t.Fatalf("fixed score parse: %+v", afterDiagnostics)
	}
	after, afterDiagnostics := project.FromScore(afterScore)
	if after == nil || hasDiagnosticErrors(afterDiagnostics) || !reflect.DeepEqual(before, after) {
		t.Fatalf("fix changed P1 project meaning: %+v", afterDiagnostics)
	}
	again, changed, err := FixSource(fixed)
	if err != nil || changed || !bytes.Equal(again, fixed) {
		t.Fatalf("fixed P1 score is not stable: changed=%v err=%v\n%s", changed, err, again)
	}
}

func TestFixNamedMixerMigrationIsTypedAndPCMExact(t *testing.T) {
	root := migrationRepoRoot(t)
	var cases []struct {
		name string
		path string
	}
	err := filepath.WalkDir(filepath.Join(root, "testdata", "edition1", "examples"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".cicada" {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			cases = append(cases, struct {
				name string
				path string
			}{relative, path})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"language/testdata/mixer-p2.cicada",
		"language/testdata/scene-settings.cicada",
		"migration/testdata/named-mixer-legacy.cicada",
	} {
		cases = append(cases, struct {
			name string
			path string
		}{relative, filepath.Join(root, relative)})
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].name < cases[j].name })
	if len(cases) == 0 {
		t.Fatal("no fix exactness cases found")
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source, err := os.ReadFile(testCase.path)
			if err != nil {
				t.Fatal(err)
			}
			beforeScore, diagnostics := notation.Parse(source)
			if beforeScore == nil || hasDiagnosticErrors(diagnostics) {
				t.Fatalf("legacy score parse: %+v", diagnostics)
			}
			before, diagnostics := project.FromScore(beforeScore)
			if before == nil || hasDiagnosticErrors(diagnostics) {
				t.Fatalf("legacy score compile: %+v", diagnostics)
			}
			fixed, _, err := FixSource(source)
			if err != nil {
				t.Fatalf("fix failed: %v", err)
			}
			if testCase.name == "migration/testdata/named-mixer-legacy.cicada" {
				for _, want := range []string{
					"send room = 0.25 pre", "send hall = 0.125 pre", "out = sfx", "mute = on",
					"bus music", "insert = comp",
				} {
					if !bytes.Contains(fixed, []byte(want)) {
						t.Errorf("migration omitted %q:\n%s", want, fixed)
					}
				}
			}
			afterScore, diagnostics := notation.ParseEdition(fixed, 2)
			if afterScore == nil || hasDiagnosticErrors(diagnostics) {
				t.Fatalf("fixed score parse: %+v", diagnostics)
			}
			after, diagnostics := project.FromScore(afterScore)
			if after == nil || hasDiagnosticErrors(diagnostics) || !project.SemanticEqual(before, after) {
				t.Fatalf("typed project changed after fix: %+v", diagnostics)
			}

			bars := 0
			for _, entry := range beforeScore.Song {
				bars += entry.Bars
			}
			if bars > 16 {
				bars = 16
			}
			if bars == 0 {
				t.Fatal("score has no song length")
			}
			pcmHash := func(score *notation.Score) string {
				t.Helper()
				var wav bytes.Buffer
				if _, err := render.WAV(score, render.Options{SampleRate: 48_000, Bits: 24, Bars: bars, TailSec: 3}, &wav); err != nil {
					t.Fatalf("render PCM24: %v", err)
				}
				return fmt.Sprintf("%x", sha256.Sum256(wav.Bytes()[44:]))
			}
			beforeHash, afterHash := pcmHash(beforeScore), pcmHash(afterScore)
			if beforeHash != afterHash {
				t.Fatalf("cicada fix changed PCM24 hash: before=%s after=%s", beforeHash, afterHash)
			}
			t.Logf("%s\t%s\ttrue\t%s\t%s", testCase.name, fixRewriteSummary(source, fixed), beforeHash, afterHash)
		})
	}
}

func migrationRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate migration test source")
	}
	return filepath.Dir(filepath.Dir(file))
}

func fixRewriteSummary(before, after []byte) string {
	var rewrites []string
	if bytes.Contains(before, []byte("send_a =")) && !bytes.Contains(after, []byte("send_a =")) {
		rewrites = append(rewrites, "send_a to named delay send")
	}
	if bytes.Contains(before, []byte("send_b =")) && !bytes.Contains(after, []byte("send_b =")) {
		rewrites = append(rewrites, "send_b to named reverb send")
	}
	if bytes.Contains(before, []byte("send_pre =")) && !bytes.Contains(after, []byte("send_pre =")) {
		rewrites = append(rewrites, "send_pre to per-send pre")
	}
	if bytes.Contains(before, []byte("bus = sfx")) && !bytes.Contains(after, []byte("bus = sfx")) {
		rewrites = append(rewrites, "bus to out")
	}
	if bytes.Contains(before, []byte("fx comp {")) && bytes.Contains(after, []byte("insert = comp")) {
		rewrites = append(rewrites, "bare comp to music-bus insert")
	}
	if bytes.Contains(before, []byte("level = off")) && !bytes.Contains(after, []byte("level = off")) {
		rewrites = append(rewrites, "level off to mute")
	}
	if len(rewrites) == 0 {
		if bytes.Equal(before, after) {
			return "none"
		}
		return "edition-1 canonicalization (header, units, or pattern spelling)"
	}
	if !bytes.Equal(before, after) && strings.Contains(string(before), "cicada 1") {
		rewrites = append(rewrites, "edition-1 canonicalization")
	}
	return strings.Join(rewrites, "; ")
}
