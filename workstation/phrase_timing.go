package main

import (
	"m31labs.dev/cicada/workstation/control"
	"m31labs.dev/gosx"
	"strconv"
)

func phraseTiming(value control.VocalEdits, active bool) gosx.Node {
	var fields []gosx.Node
	for _, f := range []struct {
		name, label     string
		value, min, max float64
	}{{"offsetMS", "Advance phrase (ms)", value.OffsetMS, -500, 500}, {"fadeMS", "Edge fade (ms)", value.FadeMS, 0, 100}, {"crossfadeMS", "Join crossfade (ms)", value.CrossfadeMS, 0, 100}} {
		fields = append(fields, field(f.label, gosx.El("input", gosx.Attrs(gosx.Attr("type", "number"), gosx.Attr("name", f.name), gosx.Attr("aria-label", f.label), gosx.Attr("value", strconv.FormatFloat(f.value, 'f', -1, 64)), gosx.Attr("min", f.min), gosx.Attr("max", f.max), gosx.Attr("step", "0.1"), gosx.Attr("data-vocal-level-control", ""), gosx.Attr("disabled", active)))))
	}
	for _, item := range []struct{ label, value string }{{"Earlier 5 ms", "5"}, {"Later 5 ms", "-5"}} {
		fields = append(fields, gosx.El("button", gosx.Attrs(gosx.Attr("type", "button"), gosx.Attr("data-vocal-nudge", item.value), gosx.Attr("data-vocal-level-control", ""), gosx.Attr("disabled", active)), gosx.Text(item.label)))
	}
	return gosx.El("details", gosx.Attrs(gosx.Attr("class", "vocal-timing")), gosx.El("summary", gosx.Text("Timing and transitions")), gosx.El("div", gosx.Attrs(gosx.Attr("class", "vocal-phrase-controls")), gosx.Fragment(fields...), gosx.El("p", gosx.Attrs(gosx.Attr("class", "muted")), gosx.Text("Positive timing advances the voice; negative timing delays it. Audition before saving. Crossfades use available audio around neighboring takes; raw recordings stay unchanged."))))
}
