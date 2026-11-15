package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/gosx"
)

func TestMixerFaderSavesTheLevelOncePerGesture(t *testing.T) {
	level := mixerField{Path: "keys.level", Value: -6.5, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6, Unit: "dB", DisplayStep: 0.5}}
	s := &studioApp{}
	html := gosx.RenderHTML(s.mixerFader(workspace{Edition: 2}, "token", "keys", level))
	for _, want := range []string{`type="range"`, `name="value"`, `min="-60"`, `max="6"`, `step="0.5"`, `value="-6.5"`, `data-commit="settle"`, `aria-label="keys level fader"`, `value="keys.level"`, `value="number"`, `action="/__actions/mixer"`, `-6.5<small>dB</small>`} {
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

// A bus level takes only the values the service lists. The Set form posts them
// as text, and the select never shows a choice that is not saved.
func TestChoiceLevelPostsTextAndShowsOnlyTheSavedChoice(t *testing.T) {
	s := &studioApp{}
	sfx := mixerField{Path: "sfx.level", Value: 0.0, Choices: []string{"off"}, Supported: true, Descriptor: paramdefs.Descriptor{Min: -60, Max: 6, Unit: "dB"}}
	html := gosx.RenderHTML(s.mixerField(workspace{Edition: 2}, "token", "level", sfx))
	for _, want := range []string{`name="valueType" value="text"`, `<select name="value" required><option value="" selected disabled>0dB</option><option value="off">off</option></select>`} {
		if !strings.Contains(html, want) {
			t.Errorf("unsaved choice: missing %s in %s", want, html)
		}
	}
	sfx.SourceValue = "off"
	if html := gosx.RenderHTML(s.mixerField(workspace{Edition: 2}, "token", "level", sfx)); !strings.Contains(html, `<select name="value"><option value="off" selected>off</option></select>`) {
		t.Errorf("saved choice: %s", html)
	}
	// Its fader shows the level and cannot move: the service refuses other values.
	fader := gosx.RenderHTML(s.mixerFader(workspace{Edition: 2}, "token", "sfx", sfx))
	if !strings.Contains(fader, "disabled") || strings.Contains(fader, "<form") || strings.Contains(fader, "data-commit") || !strings.Contains(fader, `0.0<small>dB</small>`) {
		t.Errorf("fixed fader: %s", fader)
	}
}
