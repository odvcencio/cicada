package main

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func TestMeterNoticeNamesTheSongMeter(t *testing.T) {
	if _, ok := meterNotice(nil); ok {
		t.Fatal("a song without a meter got a notice")
	}
	if _, ok := meterNotice(&project.Meter{Numerator: 4, Denominator: 4}); ok {
		t.Fatal("a 4/4 song got a notice")
	}
	message, ok := meterNotice(&project.Meter{Numerator: 6, Denominator: 8, Groups: []int{3, 3}})
	if !ok || !strings.Contains(message, "in 6/8") || !strings.Contains(message, "in 4/4") {
		t.Fatalf("notice: %q", message)
	}
}

// In a song whose meter is not 4/4, the Studio gestures that would store music
// somewhere other than where the user put it are off, each with its reason
// (#358 review): the curve editor's drawing, dragging and recording (it places
// points on a 4/4 grid), a Bar snap the browser's 4/4 grid would treat as a
// sixteenth, and Shift+Arrow, which moves by a 4/4 bar. A 4/4 song keeps them.
func TestGesturesThatMoveMusicAreOffInAnotherMeter(t *testing.T) {
	for _, c := range []struct {
		meter string
		off   bool
	}{{"", false}, {"meter 3/4\n", true}} {
		source := "cicada 2\n" + c.meter + "track bass acid {}\npattern riff { c3 . c4 . }\narrange {\n  place verse bass riff { at = @1.1.1 length = 2bars }\n}\nautomate bass.cutoff {\n  @1.1.1 400Hz\n  @2.1.1 800Hz\n}\n"
		score, ds := notation.Parse([]byte(source))
		if score == nil {
			t.Fatal(ds)
		}
		p, ds := project.FromScore(score)
		if p == nil {
			t.Fatal(ds)
		}
		ctx := &server.Context{Request: httptest.NewRequest("GET", "http://localhost/studio", nil)}
		curve := gosx.RenderHTML((&studioApp{}).continuousAutomation(ctx, workspace{Project: p, Revision: "r", SourceRevision: "s"}, "csrf"))
		timeline := gosx.RenderHTML(placementTimeline(p, p.Arrange.Placements, "verse", p.MeterMap().BarStart(2), nil))
		curveOff := strings.Contains(curve, "data-automation-curve-off") && strings.Contains(curve, "Drawing, dragging and recording points are off in this 3/4 song") && !strings.Contains(curve, "CicadaAutomationCurve") && regexp.MustCompile(`data-automation-new[^>]*disabled`).MatchString(curve)
		barOff := strings.Contains(timeline, "Bar (off until Studio draws 3/4)") && !strings.Contains(timeline, "Beat (off")
		shiftOff := strings.Contains(timeline, "data-placement-bar-moves-off") && strings.Contains(timeline, "Shift+Arrow moves by a 4/4 bar, so it is off in this 3/4 song")
		if c.off != curveOff || c.off != barOff || c.off != shiftOff {
			t.Fatalf("meter %q: curve editing off %v, Bar snap off %v, Shift+Arrow off %v; want %v", c.meter, curveOff, barOff, shiftOff, c.off)
		}
		if !c.off && (!strings.Contains(curve, "CicadaAutomationCurve") || strings.Contains(timeline, "(off until")) {
			t.Fatal("a 4/4 song lost a gesture")
		}
	}
}
