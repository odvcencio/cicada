package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// An automation lane may name a send by its effect, as a track's send line
// does: keys.send.echo is the tape echo's return, keys.send.delay. A send to
// an effect that is not a return still gets a hint naming the send paths.
func TestAutomationSendPathNamedByEffectGetsAHint(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx echo tape { time = 1/8 }\nfx grit drive {}\ntrack keys acid { send echo = 0.2 insert = grit }\npattern p acid { 1 . 3 . }\nscene s { keys = p }\nsong { s }\nautomate keys.send.echo {\n  @1.1.1 0.1\n}\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("automating keys.send.echo was refused: %+v", ds)
	}
	resolved, err := ResolveParameterPath(p, "keys.send.echo")
	if err != nil || resolved.Descriptor.Path != "send.delay" {
		t.Fatalf("keys.send.echo resolves to %+v, %v; want the send.delay control", resolved, err)
	}
	score, _ = notation.Parse([]byte(strings.Replace(source, "keys.send.echo", "keys.send.grit", 1)))
	if p, ds = FromScore(score); p != nil {
		t.Fatal("automating keys.send.grit, a drive, was accepted")
	}
	for _, d := range ds {
		if strings.Contains(d.Message, "only the delay and reverb returns have track send paths") {
			return
		}
	}
	t.Fatalf("no hint naming the send paths: %+v", ds)
}

// A route to an authored instrument's own parameter moves it live: the track
// gets a binding for that parameter's slot and graph node, and no other.
func TestRouteToAnInstrumentParamBindsItsSlot(t *testing.T) {
	source := "cicada 2\ntempo 120\ninstrument tone {\n  param drive = 0.5\n  param cutoff = 400Hz\n  voice mono { out = lowpass(saw(pitch) * drive, cutoff) * env(gate, 200ms) }\n}\ntrack lead tone { cutoff = 500Hz }\ntrack pad tone {}\nlfo wobble { wave = sine rate = 1bars }\nmod wobble -> lead.cutoff depth = 100Hz\npattern p notes { c4 . . . }\nscene s { lead = p pad = p }\nsong { s }\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	live := cfg.LiveParams
	if len(live) != 1 || live[0].Track != 0 || live[0].Slot != 1 || cfg.Track[0].Graph.Nodes[live[0].Node].Value != 500 {
		t.Fatalf("bindings %+v, want only lead's slot 1 (cutoff) on the node holding 500 Hz; pad, which nothing moves, gets none", live)
	}
}
