package migration

import (
	"bytes"
	"testing"
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
