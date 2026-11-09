package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/gosx"
)

func TestMixerFaderSubmitsTheLevelOnChange(t *testing.T) {
	level := mixerField{Path: "keys.level", Value: -6.5, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6, Unit: "dB", DisplayStep: 0.5}}
	s := &studioApp{}
	html := gosx.RenderHTML(s.mixerFader(workspace{Edition: 2}, "token", "keys", level))
	for _, want := range []string{`type="range"`, `name="value"`, `min="-60"`, `max="6"`, `step="0.5"`, `value="-6.5"`, `data-gosx-submit-on="change"`, `aria-label="keys level fader"`, `value="keys.level"`, `value="number"`, `action="/__actions/mixer"`, `-6.5<small>dB</small>`} {
		if !strings.Contains(html, want) {
			t.Errorf("fader missing %s in %s", want, html)
		}
	}
	// An "off" level parks the fader at its minimum and says off.
	level.Value = "off"
	if html := gosx.RenderHTML(s.mixerFader(workspace{Edition: 2}, "token", "keys", level)); !strings.Contains(html, `value="-60"`) || !strings.Contains(html, `off<small>`) {
		t.Fatalf("off level: %s", html)
	}
	// Edition 1 mixer edits need an upgrade confirmation, so no fader.
	if html := gosx.RenderHTML(s.mixerFader(workspace{Edition: 1}, "token", "keys", level)); strings.Contains(html, "range") {
		t.Fatalf("edition 1 should keep only the field: %s", html)
	}
}
