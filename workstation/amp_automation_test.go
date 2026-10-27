package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"m31labs.dev/gosx/server"
)

func TestAmpAutomationLinksFollowTrackCatalogAndSharedInsert(t *testing.T) {
	p := &project.Project{Edition: 2, Tracks: []project.Track{
		{ID: "guitar", Kind: "audio", Mixer: project.Mixer{Inserts: []string{"eq1", "shared_amp"}}},
		{ID: "harmony", Kind: "audio", Mixer: project.Mixer{Inserts: []string{"shared_amp"}}},
	}, Effects: []project.Effect{{ID: "eq1", Kind: "eq", Params: map[string]project.Value{}}, {ID: "shared_amp", Kind: "ampcab", Params: map[string]project.Value{}}}}
	ctx := &server.Context{Request: httptest.NewRequest("GET", "http://localhost/studio?tab=arrange&vocalTrack=guitar", nil)}
	html := gosx.RenderHTML(ampAutomationLinks(ctx, p, "guitar"))
	for _, want := range []string{"Automate Drive", "Automate Tone", "Automate Output", "curvePath=guitar.amp_drive", "curvePath=guitar.amp_tone", "curvePath=guitar.amp_output", "dock=automation", "Input monitoring uses separate settings"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
	if strings.Contains(html, "cabinet") || strings.Contains(html, "bypass") {
		t.Fatal("discrete amp controls offered as curves")
	}
	shared := gosx.RenderHTML(ampAutomationLinks(ctx, p, "harmony"))
	if !strings.Contains(shared, "curvePath=harmony.amp_drive") {
		t.Fatalf("shared amp lost independent track link: %s", shared)
	}
	p.Effects[1].Params["bypass"] = project.Value{Unit: "enum", Text: "on"}
	if got := gosx.RenderHTML(ampAutomationLinks(ctx, p, "guitar")); !strings.Contains(got, "playback amp is bypassed") {
		t.Fatalf("bypassed amp was not identified: %s", got)
	}
	p.Tracks = append(p.Tracks, project.Track{ID: "synth", Kind: "acid", Mixer: project.Mixer{Inserts: []string{"shared_amp"}}})
	if got := gosx.RenderHTML(ampAutomationLinks(ctx, p, "synth")); !strings.Contains(got, "curvePath=synth.amp_drive") {
		t.Fatalf("synth amp missing: %s", got)
	}
	p.Tracks[0].Mixer.Inserts = append(p.Tracks[0].Mixer.Inserts, "second_amp")
	p.Effects = append(p.Effects, project.Effect{ID: "second_amp", Kind: "ampcab", Params: map[string]project.Value{}})
	if got := gosx.RenderHTML(ampAutomationLinks(ctx, p, "guitar")); got != "" {
		t.Fatalf("ambiguous amp exposed automation: %s", got)
	}
}
