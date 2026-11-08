package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// An automation lane that names a send by its effect gets a hint with the
// return-slot path.
func TestAutomationSendPathNamedByEffectGetsAHint(t *testing.T) {
	source := "cicada 2\ntempo 120\nfx echo tape { time = 1/8 }\ntrack keys acid { send echo = 0.2 }\npattern p acid { 1 . 3 . }\nscene s { keys = p }\nsong { s }\nautomate keys.send.echo {\n  @1.1.1 0.1\n}\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p != nil {
		t.Fatal("automating keys.send.echo was accepted")
	}
	for _, d := range ds {
		if strings.Contains(d.Message, "the send to echo is send.delay") {
			return
		}
	}
	t.Fatalf("no hint naming send.delay: %+v", ds)
}

// A route to an authored instrument's own parameter explains that the value is
// fixed at compile time and names the in-graph alternative.
func TestRouteToAnInstrumentParamExplainsTheLimit(t *testing.T) {
	source := "cicada 2\ntempo 120\ninstrument tone {\n  param cutoff = 400Hz\n  voice mono { out = lowpass(saw(pitch), cutoff) * env(gate, 200ms) }\n}\ntrack lead tone { cutoff = 500Hz }\nlfo wobble { wave = sine rate = 1bars }\nmod wobble -> lead.cutoff depth = 100Hz\npattern p notes { c4 . . . }\nscene s { lead = p }\nsong { s }\n"
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", ds)
	}
	_, ds = FromScore(score)
	for _, d := range ds {
		if strings.Contains(d.Message, "cutoff is a parameter of instrument tone") && strings.Contains(d.Message, "mod(<source>)") {
			return
		}
	}
	t.Fatalf("no instrument-parameter hint: %+v", ds)
}
