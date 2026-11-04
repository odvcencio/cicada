package liveplay

import (
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

func TestEffectiveParameterTracksAutomationAndLiveOverride(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.3), Tick: 0}}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	player, err := New(Score{Engine: created, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "bass"}}, Parameters: []ParameterValue{{Track: 0, ID: kernel.ParamMixPan, Value: 0}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	pcm := make([]byte, blockFrames*8)
	if _, err := io.ReadFull(player, pcm); err != nil {
		t.Fatal(err)
	}
	if got, ok := player.EffectiveValue(0, kernel.ParamMixPan); !ok || math.Abs(float64(got-.3)) > 1e-6 {
		t.Fatalf("automation target %g, %v", got, ok)
	}
	if err := player.SetParam(0, kernel.ParamMixPan, -.5); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(player, pcm); err != nil {
		t.Fatal(err)
	}
	if got, ok := player.EffectiveValue(0, kernel.ParamMixPan); !ok || math.Abs(float64(got+.5)) > 1e-6 {
		t.Fatalf("live target %g, %v", got, ok)
	}
}
