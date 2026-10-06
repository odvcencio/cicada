package director

import (
	"errors"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
)

func testSurface() Surface {
	return Surface{Version: 1, SampleRate: 48000, Tracks: 3, Land: "bar", PhraseBars: 8, Macros: []Macro{{Name: "intensity", SmoothFrames: 19200}}, States: []State{{Name: "explore"}, {Name: "combat", ID: 1, Scene: 1}}, Stingers: []Stinger{{Name: "pickup", Track: 2, Quantize: "beat", CrossfadeFrames: 480}}, Transitions: []Transition{{From: "explore", To: "combat", Quantize: "phrase", CrossfadeFrames: 9600}}}
}
func TestDirectorCommandsUseLandedStateAndRejectErrors(t *testing.T) {
	s := testSurface()
	var records []cmd.Command
	c, err := New(s, func(v cmd.Command) error { records = append(records, v); return nil })
	if err != nil {
		t.Fatal(err)
	}
	s.States[0].Scene = 99
	if err := c.SetState("explore", 0); err != nil {
		t.Fatal(err)
	}
	if c.State() != "" || records[0].Arg0 != 2<<16 {
		t.Fatal("state changed before landing or manifest mutated")
	}
	c.Handle(cmd.Message{Kind: cmd.StateChanged, A: 0, B: 0})
	tick := int64(1<<54) + 3
	if err := c.SetState("combat", tick); err != nil {
		t.Fatal(err)
	}
	want := cmd.Command{Op: cmd.OpSetState, Track: 255, Index: 1, Arg0: 1 | 21<<16, Arg1: 9600, Tick: tick}
	if records[1] != want {
		t.Fatalf("command: %+v", records[1])
	}
	if err := c.SetMacro("intensity", .75, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.TriggerStinger("pickup", 0); err != nil {
		t.Fatal(err)
	}
	c.Handle(cmd.Message{Kind: cmd.LayerChanged, B: 5})
	if c.LayerMask() != 5 {
		t.Fatal("lost layer observation")
	}
	for _, err := range []error{c.SetState("missing", 0), c.SetState("combat", -1), c.SetMacro("intensity", float32(math.NaN()), 0), c.SetMacro("intensity", 2, 0), c.TriggerStinger("missing", 0)} {
		if err == nil {
			t.Fatal("invalid control accepted")
		}
	}
	expected := errors.New("queue full")
	c.send = func(cmd.Command) error { return expected }
	if err := c.SetState("combat", 0); !errors.Is(err, expected) {
		t.Fatal("sender failure discarded")
	}
}
