package project

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestEditPlanPatternCopyRetainsTheMusicalMaterial(t *testing.T) {
	for _, check := range []struct{ name, source string }{
		{"phrase and grid", "cicada 2\nseed 42\ntrack bass acid {}\nphrase hook { 1^ . 5~*2?70 - }\npattern parent acid slot=7 { step=1/8t swing=54% gate=70% transpose=5 seed=99 use hook use hook +12 }\nscene main { bass=parent }\nsong { main }\n"},
		{"chord and register", "cicada 2\ninstrument glass { voice poly { out=sine(pitch) } }\ntrack keys glass {}\npattern parent notes { step=1/8. [c5 e5 g5]^ . }\nscene main { keys=parent }\nsong { main }\n"},
		{"drums", "cicada 2\ntrack drums drums {}\npattern parent drums { bd: X.x3x*2; sd: ..x.; }\nscene main { drums=parent }\nsong { main }\n"},
		{"performed groups", groupScore},
	} {
		t.Run(check.name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(check.source))
			p, more := FromScore(score)
			if p == nil || len(ds) != 0 || len(more) != 0 {
				t.Fatal(ds, more)
			}
			plan := EditPlan(p, []byte(check.source))
			if _, err := json.Marshal(plan.Patterns[0]); err != nil {
				t.Fatal("copy capability broke the serializable pattern view", err)
			}
			copy, err := plan.Patterns[0].CopySource("independent")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(copy, "slot") || strings.Contains(copy, "use hook") {
				t.Fatal("copy retained placement or phrase dependencies", copy)
			}
			score, ds = notation.Parse([]byte(check.source + "\n" + copy))
			q, more := FromScore(score)
			if q == nil || len(ds) != 0 || len(more) != 0 {
				t.Fatal(ds, more, copy)
			}
			before, after := p.Patterns[0], q.Patterns[len(q.Patterns)-1]
			before.ID = after.ID
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("copy changed material: before=%+v after=%+v\n%s", before, after, copy)
			}
		})
	}
}
