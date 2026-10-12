package main

import (
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func studioPreviewScore(t *testing.T, gain float64) liveplay.Score {
	t.Helper()
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Len, program.Output = 2, 1
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: 256, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000}
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = gain, true
	cfg.Track[0].Pan, cfg.Track[0].BusSFX = -1, true
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return liveplay.Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: "preview",
		Tracks: []liveplay.TrackSlots{{ID: "bass"}}, HasSFX: true,
		Parameters: []liveplay.ParameterValue{{Track: 0, ID: kernel.ParamMixGain, Value: float32(gain)}},
	}
}

func renderStudioPreview(t *testing.T, p *liveplay.Player, frames int64) {
	t.Helper()
	if _, err := io.CopyN(io.Discard, p, frames*8); err != nil {
		t.Fatal(err)
	}
}

func assertStudioPreviewGain(t *testing.T, p *liveplay.Player, gain float64) {
	t.Helper()
	select {
	case frame := <-p.Meters():
		want := math.Pow(10, gain/20)
		if math.Abs(float64(frame.Tracks[0].Peak)-want) > .01 {
			t.Fatalf("measured playback %g, want %g (%g dB)", frame.Tracks[0].Peak, want, gain)
		}
	default:
		t.Fatal("no playback meter")
	}
}

func TestPreviewCancelDoesNotMaskLaterSavedScore(t *testing.T) {
	for _, timing := range []string{"offer-after-cancel", "offer-before-cancel", "cancel-at-activation", "activate-before-cancel"} {
		t.Run(timing, func(t *testing.T) {
			p, err := liveplay.New(studioPreviewScore(t, -6), 48_000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			s := &studio{transport: &studioTransport{stream: p}}
			session := make(livePreviewSession)
			if err := s.handleLiveMessage([]byte(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`), session); err != nil {
				t.Fatal(err)
			}
			renderStudioPreview(t, p, 24_064)
			assertStudioPreviewGain(t, p, 0)
			offer := func() {
				if err := p.Offer(studioPreviewScore(t, -3)); err != nil {
					t.Fatal(err)
				}
			}
			if timing != "offer-after-cancel" {
				offer()
			}
			if timing == "activate-before-cancel" {
				// At 48 kHz/120 BPM a bar is 96,000 frames.
				renderStudioPreview(t, p, 120_064)
				assertStudioPreviewGain(t, p, 0)
			}
			if timing == "cancel-at-activation" {
				// Stop exactly before the boundary that activates the offer.
				renderStudioPreview(t, p, 96_000-24_064)
			}
			if err := s.handleLiveMessage([]byte(`{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":false}`), session); err != nil {
				t.Fatal(err)
			}
			if timing == "offer-after-cancel" {
				// Retire the preview before a later score is even offered.
				renderStudioPreview(t, p, 256)
				offer()
			}
			renderStudioPreview(t, p, 120_064)
			if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || value != -3 {
				t.Fatalf("saved score did not activate: %g, %v", value, ok)
			}
			assertStudioPreviewGain(t, p, -3)
			if value, active := p.OverrideValue(0, kernel.ParamMixGain); active {
				t.Fatalf("cancel kept override %g active", value)
			}
		})
	}
}

func TestPreviewCancelFromOlderClientPreservesNewerGesture(t *testing.T) {
	p, err := liveplay.New(studioPreviewScore(t, -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s := &studio{transport: &studioTransport{stream: p}}
	a, b := make(livePreviewSession), make(livePreviewSession)
	if err := s.handleLiveMessage([]byte(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":0}`), a); err != nil {
		t.Fatal(err)
	}
	renderStudioPreview(t, p, 24_064)
	if err := s.handleLiveMessage([]byte(`{"type":"preview-set","entity":"track:bass","param":"mix.gain","value":-9}`), b); err != nil {
		t.Fatal(err)
	}
	renderStudioPreview(t, p, 24_064)
	if err := s.handleLiveMessage([]byte(`{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":false}`), a); err != nil {
		t.Fatal(err)
	}
	renderStudioPreview(t, p, 24_064)
	assertStudioPreviewGain(t, p, -9)
	if value, active := p.OverrideValue(0, kernel.ParamMixGain); !active || value != -9 {
		t.Fatalf("older client canceled newer gesture: %g, %v", value, active)
	}
	if err := s.handleLiveMessage([]byte(`{"type":"preview-end","entity":"track:bass","param":"mix.gain","commit":false}`), b); err != nil {
		t.Fatal(err)
	}
	renderStudioPreview(t, p, 24_064)
	assertStudioPreviewGain(t, p, -6)
	if _, active := p.OverrideValue(0, kernel.ParamMixGain); active {
		t.Fatal("newer client's cancellation did not retire its override")
	}
}
