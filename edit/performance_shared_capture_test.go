package edit_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestRecordedSharedGesturesKeepIndependentLayersAndReleaseTail(t *testing.T) {
	source := []byte("instrument glass { voice poly { out=sine(pitch)*env(gate,1800ms)*velocity*(0.1+pressure*0.1+timbre*0.1) } }\ntrack keys glass {}\npattern take notes { . . . . } // keep this\nscene main { keys=take }\nsong { main*4 }\n")
	recording := edit.Recording{Track: "keys", Pattern: "take", Notes: []edit.TakeNote{
		{Tick: 3857, EndTick: 3960, Note: 60, Velocity: 100, Group: 2, Expressions: []edit.TakeExpression{{Tick: 3857, PitchCents: 25, Pressure: .2, Timbre: .6}}},
		{Tick: 3860, EndTick: 4100, Note: 64, Velocity: 90, Group: 3},
	}, Groups: []edit.TakeGroup{
		{Points: []edit.TakeExpression{{Tick: 3857, Timbre: .5}}},
		{Points: []edit.TakeExpression{{Tick: 3857, Timbre: .5}, {Tick: 3977, PitchCents: 200, Pressure: .8, Timbre: .9}, {Tick: 3977, PitchCents: -50, Pressure: .7, Timbre: .8}, {Tick: 8000, PitchCents: 100, Pressure: .6, Timbre: .2}}},
		{Points: []edit.TakeExpression{{Tick: 3860, Pressure: .1, Timbre: .5}}},
	}}
	result, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{recording}})
	if err != nil {
		t.Fatal(err)
	}
	p := result.Plan.Patterns[0].Performance
	if p.LengthTicks != 7680 || len(p.Groups) != 2 || p.Groups[0].Name != "mpe-2" || len(p.Groups[0].Points) != 3 || p.Groups[0].Points[1].Value.PitchCents != -50 || p.Groups[0].Points[2].Tick != 4160 {
		t.Fatal("shared capture lost a tail, duplicate-tick resolution or stable lane name", p)
	}
	if p.Notes[0].Tick != 17 || p.Notes[0].EndTick != 120 || p.Notes[0].Group != 1 || p.Notes[1].Group != 2 || p.Notes[0].Expressions[0].Params.PitchCents != 25 || p.Notes[0].Expressions[0].Params.Pressure != .2 {
		t.Fatal("master controls were baked into a member or changed a gate", p.Notes)
	}
	if len(recording.Groups[1].Points) != 4 || !bytes.Contains(result.Source, []byte("// keep this")) {
		t.Fatal("commit mutated raw samples or comments")
	}
	score, ds := notation.Parse(result.Source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	reopened, ds := project.FromScore(score)
	if len(ds) != 0 || !reflect.DeepEqual(reopened.Patterns[0].Performance.Kernel(), p) {
		t.Fatal("saved source did not reopen with the same lanes", ds)
	}
	edited, err := applyM5(t, result.Source, &edit.SetSharedExpression{Entity: "pattern:take", GroupIndex: 0, Index: 2, Tick: 4160, PitchCents: 75, Pressure: .3, Timbre: .4})
	if err != nil || edited.Plan.Patterns[0].Performance.Groups[0].Points[2].Value.PitchCents != 75 {
		t.Fatal("captured lane is not editable", err)
	}
	if _, err = applyM5(t, result.Source, &edit.RecordTake{Timing: "grid", Recordings: []edit.Recording{recording}}); err == nil || !strings.Contains(err.Error(), "Keep played timing") {
		t.Fatal("grid commit silently discarded shared controls", err)
	}
}

func TestNeutralCapturedGroupsDoNotRequireAnExpressiveSound(t *testing.T) {
	source := []byte("track keys piano {}\npattern take notes { . . . . }\nscene main { keys=take }\nsong { main }\n")
	recording := edit.Recording{Track: "keys", Pattern: "take", Notes: []edit.TakeNote{{Tick: 17, EndTick: 120, Note: 60, Velocity: 90, Group: 1}}, Groups: []edit.TakeGroup{{Points: []edit.TakeExpression{{Tick: 17, Timbre: .5}, {Tick: 8000, Timbre: .5}}}}}
	for _, timing := range []string{"played", "grid"} {
		result, err := applyM5(t, source, &edit.RecordTake{Timing: timing, Recordings: []edit.Recording{recording}})
		if err != nil {
			t.Fatal(timing, err)
		}
		if p := result.Plan.Patterns[0].Performance; p != nil && (len(p.Groups) != 0 || p.Notes[0].Group != 0 || p.LengthTicks != 3840) {
			t.Fatal("neutral input invented expression or stretched the loop", p)
		}
	}
}

func TestRecordedSharedGesturesRejectInvalidBindingsAndCombinedBudget(t *testing.T) {
	source := []byte("track keys acid {}\npattern take { . . . . }\nscene main { keys=take }\nsong { main }\n")
	base := edit.Recording{Track: "keys", Pattern: "take", Notes: []edit.TakeNote{{Tick: 0, EndTick: 120, Note: 60, Velocity: 90, Group: 1}}}
	for _, timing := range []string{"played", "grid"} {
		if _, err := applyM5(t, source, &edit.RecordTake{Timing: timing, Recordings: []edit.Recording{base}}); err == nil {
			t.Fatal("a missing shared lane was accepted", timing)
		}
	}
	base.Groups = []edit.TakeGroup{{Points: make([]edit.TakeExpression, 65536)}}
	base.Notes[0].Expressions = []edit.TakeExpression{{Timbre: .5}}
	if _, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{base}}); err == nil || !strings.Contains(err.Error(), "gesture points") {
		t.Fatal("shared points bypassed the total expression limit", err)
	}
}

func TestRecordedSharedLeadInPreservesTheCommonOrigin(t *testing.T) {
	source := []byte("track keys acid {}\npattern take { . . . . }\nscene main { keys=take }\nsong { main*4 }\n")
	r := edit.Recording{Track: "keys", Pattern: "take", Notes: []edit.TakeNote{{Tick: 3857, EndTick: 4100, Note: 60, Velocity: 90, Group: 1}}, Groups: []edit.TakeGroup{{Points: []edit.TakeExpression{{Tick: 0, Timbre: .5}, {Tick: 3840, Pressure: .7, Timbre: .8}}}}}
	result, err := applyM5(t, source, &edit.RecordTake{Timing: "played", Recordings: []edit.Recording{r}})
	if err != nil {
		t.Fatal(err)
	}
	p := result.Plan.Patterns[0].Performance
	if p.Notes[0].Tick != 3857 || p.Groups[0].Points[1].Tick != 3840 || p.LengthTicks != 7680 {
		t.Fatal("keeping a take trimmed the captured lead-in", p)
	}
}
