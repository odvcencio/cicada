package kernelimage

import (
	"bytes"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/chorus"
)

func TestChorusControlRequiresUniqueTrackStage(t *testing.T) {
	cfg := modulatedConfig()
	cfg.Tracks, cfg.MaxVoices = 2, 2
	cfg.Track[1].Kind = engine.VoiceAcid
	cfg.Patterns = append(cfg.Patterns, engine.PatternBank{})
	params := chorus.DefaultParams()
	stage := engine.TrackEffectConfig{Kind: engine.TrackEffectChorus, Chorus: &params}
	cfg.Track[0].InsertChain = []engine.TrackEffectConfig{stage}
	command := cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamChorusMix), Arg0: math.Float32bits(.3), Tick: 960}
	cfg.Automation = []cmd.Command{command}
	valid, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := cmd.EncodeCommand(command, uint8(cfg.Tracks))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(valid, wire[:]) != 1 {
		t.Fatal("automation record is not unique")
	}
	bad := append([]byte(nil), valid...)
	bad[bytes.Index(bad, wire[:])+1] = 1 // Track 1 has no chorus.
	if _, err := Decode(bad, 48000, 128); err == nil {
		t.Fatal("decoded automation targeting an absent chorus")
	}
	for _, stages := range [][]engine.TrackEffectConfig{nil, {stage, stage}} {
		cfg.Track[0].InsertChain = stages
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("engine accepted invalid chorus automation owner")
		}
		if _, err := Encode(cfg); err == nil {
			t.Fatal("encoded invalid chorus automation owner")
		}
		cfg.Automation = nil
		cfg.Scenes = []engine.Scene{{Settings: []engine.SceneSetting{{Track: 0, ID: kernel.ParamChorusMix, Value: .3}}}}
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("engine accepted invalid chorus scene owner")
		}
		if _, err := Encode(cfg); err == nil {
			t.Fatal("encoded invalid chorus scene owner")
		}
		cfg.Scenes = nil
		cfg.Automation = []cmd.Command{command}
	}
}
