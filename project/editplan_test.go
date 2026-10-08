package project

import (
	"bytes"
	"os"
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
