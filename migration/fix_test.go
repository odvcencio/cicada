package migration

import (
	"bytes"
	"reflect"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
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
