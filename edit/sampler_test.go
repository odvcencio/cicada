package edit

import (
	"bytes"
	"strings"
	"testing"
)

type fixedPlan struct{ plan *Plan }

func (c fixedPlan) Compile(source []byte, _ map[string][]byte) (*Plan, error) {
	if c.plan == nil {
		return &Plan{Revision: Revision(source)}, nil
	}
	return c.plan, nil
}

// An imported instrument is resolved by the project compile, so a score with
// imports the standalone parser cannot resolve still gains its audio track.
func TestPlaceSamplerPrintAddsTrackToScoreWithUnresolvedImports(t *testing.T) {
	source := []byte("cicada 2\nimport \"std/keys\"\ntrack lead keys.tine_ep {}\npattern head { 1 . 3 . }\narrange { place original lead head { at = @1.1.1 length = 4bars } }\n")
	plan := &Plan{Edition: 2, HasArrange: true, TempoMilli: 120000, Tracks: []Track{{ID: "lead", Kind: "keys"}}}
	intent := &PlaceSamplerPrint{Name: "print", Track: "sampler", At: "@5.1.1", Asset: PrintAsset{Path: "sampler/audio/x.wav", SHA256: strings.Repeat("ab", 32), Frames: 48000, RateHz: 48000}}
	result, err := Apply(source, Envelope{Version: EnvelopeVersion, Intents: []Intent{intent}}, Options{Compiler: fixedPlan{plan}})
	if err != nil || !bytes.HasPrefix(result.Source, source[:len(source)-len("arrange { place original lead head { at = @1.1.1 length = 4bars } }\n")]) || !bytes.Contains(result.Source, []byte("track sampler audio {}")) {
		t.Fatalf("imported score cannot acquire an audio track: %v\n%s", err, result.Source)
	}
	if !bytes.Contains(result.Source, []byte("place print sampler print-clip { at = @5.1.1 length = 1920ticks }")) {
		t.Fatalf("placement missing:\n%s", result.Source)
	}
	// A second print onto the track that now exists adds no second track.
	plan.Tracks = append(plan.Tracks, Track{ID: "sampler", Kind: "audio"})
	intent.Name = "again"
	again, err := Apply(result.Source, Envelope{Version: EnvelopeVersion, Intents: []Intent{intent}}, Options{Compiler: fixedPlan{plan}})
	if err != nil || bytes.Count(again.Source, []byte("track sampler audio {}")) != 1 {
		t.Fatalf("duplicate track: %v", err)
	}
	// A local audio track declaration is refused when the plan lacks it.
	plan.Tracks = plan.Tracks[:1]
	if _, err := Apply(result.Source, Envelope{Version: EnvelopeVersion, Intents: []Intent{intent}}, Options{Compiler: fixedPlan{plan}}); err == nil || err.Error() != "track sampler already exists" {
		t.Fatalf("duplicate local audio track accepted: %v", err)
	}
}
