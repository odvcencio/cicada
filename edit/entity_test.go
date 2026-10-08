package edit

import (
	"fmt"
	"testing"
)

func TestEntityIDParsesEveryKind(t *testing.T) {
	for _, id := range []string{"project", "track:bass", "pattern:pulse", "step:pulse/3", "step:beat/bd/3", "scene:main", "song:2", "placement:intro", "marker:chorus", "clip:take-1-clip", "param:bass.cutoff", "fx:room", "bus:music", "master", "setting:main/bass.level"} {
		if _, err := ParseEntityID(id); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	for _, bad := range []string{"", "nope:x", "step:pulse", "song:x", "track:"} {
		if _, err := ParseEntityID(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestResolvePlacementIndexFollowsRecompile(t *testing.T) {
	before := &Plan{Revision: "r1", Placements: []Placement{{ID: "intro"}, {ID: "return"}}}
	after := &Plan{Revision: "r2", Placements: []Placement{{ID: "intro"}, {ID: "return"}, {ID: "lead-in"}}}
	id := EntityID("placement:return")
	a, err := Resolve(before, id)
	b, err2 := Resolve(after, id)
	if err != nil || err2 != nil || a.Index != 1 || b.Index != 1 {
		t.Fatalf("%+v %+v %v %v", a, b, err, err2)
	}
	moved := &Plan{Revision: "r3", Placements: []Placement{{ID: "lead-in"}, {ID: "intro"}, {ID: "return"}}}
	if c, err := Resolve(moved, id); err != nil || c.Index != 2 {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := Resolve(after, EntityID("song:0")); err == nil {
		t.Fatal("song index on a plan without a song must fail")
	}
	step, err := Resolve(&Plan{Patterns: []Pattern{{ID: "beat", Kind: "drums", Lanes: map[string][]*Step{"bd": make([]*Step, 4)}}}}, EntityID("step:beat/bd/3"))
	if err != nil || step.Track != "" || step.Lane != "bd" || step.Step != 3 || step.Index != 0 {
		t.Fatalf("%+v %v", step, err)
	}
}

func TestResolveFXAndBusUseDeclaredNames(t *testing.T) {
	plan := &Plan{Names: map[string]bool{"room": true, "music": true}}
	if e, err := Resolve(plan, "fx:room"); err != nil || e.Name != "room" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := Resolve(plan, "bus:nope"); err == nil || err.Error() != `unknown bus "nope"` {
		t.Fatalf("%v", err)
	}
}

func TestResolveParamUsesThePlanResolver(t *testing.T) {
	plan := &Plan{ResolveParam: func(path string) (Param, error) {
		if path == "keys.cutoff" {
			return Param{Path: path}, nil
		}
		return Param{}, fmt.Errorf("unknown parameter path %s", path)
	}}
	if e, err := Resolve(plan, "param:keys.cutoff"); err != nil || e.Name != "keys.cutoff" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := Resolve(plan, "param:keys.nope"); err == nil || err.Error() != "unknown parameter path keys.nope" {
		t.Fatalf("unknown parameter accepted: %v", err)
	}
	// A plan without a resolver cannot vouch for a parameter.
	if _, err := Resolve(&Plan{}, "param:keys.cutoff"); err == nil {
		t.Fatal("param resolved without a resolver")
	}
}
