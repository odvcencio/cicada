package edit_test

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/edit"
)

func TestSharedLaneEditsPreserveMembershipAndRefuseDataLoss(t *testing.T) {
	source := []byte(`cicada 2
instrument glass { voice poly { out=sine(pitch)*env(gate,80ms)*pressure } }
track keys glass {}
pattern phrase performance length=3840ticks {
 gesture-group touch { gesture at=0ticks bend=0ct pressure=0.2 timbre=0.5; gesture at=2000ticks bend=25ct pressure=0.8 timbre=0.7; }
 note 60 at=17ticks length=703ticks velocity=103 group=touch;
 note 64 at=19ticks length=1301ticks velocity=87;
}
scene main { keys=phrase }
song { main*2 }
`)
	entity := edit.EntityID("pattern:phrase")
	result, err := applyM5(t, source, &edit.SetPerformedNote{Entity: entity, Index: 0, Tick: 31, EndTick: 900, Pitch: 62, Velocity: 101})
	if err != nil || result.Plan.Patterns[0].Performance.Notes[0].Group != 1 || !strings.Contains(string(result.Source), "group=touch") {
		t.Fatal("moving a note lost its shared lane", err)
	}
	result, err = applyM5(t, result.Source, &edit.BindPerformedGroup{Entity: entity, NoteIndex: 1, Group: 1})
	if err != nil || result.Plan.Patterns[0].Performance.Notes[1].Group != 1 {
		t.Fatal(err)
	}
	result, err = applyM5(t, result.Source, &edit.SetSharedExpression{Entity: entity, GroupIndex: 0, Index: 1, Tick: 2200, PitchCents: 212.125, Pressure: .8, Timbre: .9})
	if err != nil || result.Plan.Patterns[0].Performance.Groups[0].Points[1].Tick != 2200 {
		t.Fatal(err)
	}
	for _, in := range []edit.Intent{
		&edit.ResizePerformance{Entity: entity, LengthTicks: 1500},
		&edit.SetPerformanceGroup{Entity: entity, Index: 0, Remove: true},
		&edit.SetSharedExpression{Entity: entity, GroupIndex: 0, Index: 1, Tick: 0, Timbre: .5},
		&edit.SetSharedExpression{Entity: entity, GroupIndex: 0, Index: 1, Tick: 3840, Timbre: .5},
		&edit.BindPerformedGroup{Entity: entity, NoteIndex: 1, Group: 2},
	} {
		if _, err := applyM5(t, result.Source, in); err == nil {
			t.Fatal("invalid or destructive shared edit succeeded", in)
		}
	}
	renamed, err := applyM5(t, result.Source, &edit.SetPerformanceGroup{Entity: entity, Index: 0, Name: "breath"})
	if err != nil || strings.Count(string(renamed.Source), "group=breath") != 2 {
		t.Fatal("rename did not retain bindings", err)
	}
}
