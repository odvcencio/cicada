package edit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
)

type previewPlacement struct {
	Entity EntityID `json:"entity"`
}

func (previewPlacement) Kind() string { return "previewplacement" }

func (in *previewPlacement) Preview(plan *Plan, _ Intent) ([]PreviewOp, error) {
	entity, err := Resolve(plan, in.Entity)
	if err != nil {
		return nil, err
	}
	placement := plan.Placements[entity.Index]
	return []PreviewOp{{Kind: "placement", Index: entity.Index, AtTick: placement.AtTick, LengthTicks: placement.LengthTicks}}, nil
}

func previewPlan() *Plan {
	return &Plan{
		Tracks:     []Track{{ID: "bass"}},
		Placements: []Placement{{ID: "intro"}, {ID: "return", AtTick: 3840, LengthTicks: 7680}},
		ResolveParam: func(path string) (Param, error) {
			if path != "bass.level" {
				return Param{}, fmt.Errorf("unknown parameter %s", path)
			}
			descriptor, _ := paramdefs.Lookup("mix.gain")
			return Param{Path: path, Descriptor: descriptor}, nil
		},
	}
}

func TestPreviewOpsResolveEntitiesAfterRecompile(t *testing.T) {
	Register("previewplacement", func() Intent { return &previewPlacement{} })
	t.Cleanup(func() { delete(registry, "previewplacement") })
	plan := previewPlan()
	intents := []Intent{&SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)}, &previewPlacement{Entity: "placement:return"}}
	want := []PreviewOp{{Kind: "setparam", Track: 0, Param: "level", Value: -3}, {Kind: "placement", Index: 1, AtTick: 3840, LengthTicks: 7680}}
	ops, err := PreviewOps(plan, intents)
	if err != nil || !reflect.DeepEqual(ops, want) {
		t.Fatalf("ops=%+v, err=%v; want %+v", ops, err, want)
	}
	plan.Tracks = append([]Track{{ID: "lead"}}, plan.Tracks...)
	plan.Placements = append([]Placement{{ID: "lead-in"}}, plan.Placements...)
	want[0].Track, want[1].Index = 1, 2
	ops, err = PreviewOps(plan, intents)
	if err != nil || !reflect.DeepEqual(ops, want) {
		t.Fatalf("recompiled ops=%+v, err=%v; want %+v", ops, err, want)
	}
}

func TestPreviewOpsRefuseUnknownEntitiesWithoutPartialOps(t *testing.T) {
	for _, intent := range []Intent{&previewPlacement{Entity: "placement:missing"}, &SetParam{Entity: "param:missing.level", Value: json.RawMessage(`-3`)}, &SetParam{Entity: "track:bass", Value: json.RawMessage(`-3`)}} {
		ops, err := PreviewOps(previewPlan(), []Intent{&SetParam{Entity: "param:bass.level", Value: json.RawMessage(`-3`)}, intent})
		if err == nil || ops != nil {
			t.Fatalf("%+v: ops=%+v, err=%v", intent, ops, err)
		}
	}
	if _, err := PreviewOps(nil, []Intent{&previewPlacement{Entity: "placement:return"}}); err == nil {
		t.Fatal("preview accepted a nil plan")
	}
}

func TestPreviewOpsNumericValuesAndOptionalPreviewers(t *testing.T) {
	plan := previewPlan()
	for _, value := range []json.RawMessage{json.RawMessage(`"-3dB"`), json.RawMessage(`-3`)} {
		ops, err := PreviewOps(plan, []Intent{&SetParam{Entity: "param:bass.level", Value: value}})
		if err != nil || len(ops) != 1 || ops[0].Value != -3 {
			t.Fatalf("value=%s: %+v, %v", value, ops, err)
		}
	}
	param, _ := plan.ResolveParam("bass.level")
	ops, err := PreviewOps(plan, []Intent{&SetParam{Entity: "param:bass.level"}, &ReplaceText{Source: "unused"}})
	if err != nil || len(ops) != 1 || ops[0].Value != param.Descriptor.Default {
		t.Fatalf("reset: %+v, %v", ops, err)
	}
	for _, value := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`"bad"`), json.RawMessage(`1000`)} {
		if _, err := PreviewOps(plan, []Intent{&SetParam{Entity: "param:bass.level", Value: value}}); err == nil {
			t.Fatalf("invalid preview value %s accepted", value)
		}
	}
}

func TestPreviewOpsLeaveNonnumericAndGlobalParametersInDiff(t *testing.T) {
	plan := previewPlan()
	plan.ResolveParam = func(path string) (Param, error) {
		id := "mix.mute"
		if path == "master.level" {
			id = "mix.master.level"
		}
		descriptor, ok := paramdefs.Lookup(id)
		if !ok {
			t.Fatalf("missing descriptor %s", id)
		}
		return Param{Path: path, Descriptor: descriptor}, nil
	}
	for _, intent := range []Intent{&SetParam{Entity: "param:master.level", Value: json.RawMessage(`-3`)}, &SetParam{Entity: "param:bass.mute", Value: json.RawMessage(`true`)}, &SetParam{Entity: "param:bass.mute"}} {
		ops, err := PreviewOps(plan, []Intent{intent})
		if err != nil || len(ops) != 0 {
			t.Fatalf("nonnumeric preview: %+v, %v", ops, err)
		}
	}
}
