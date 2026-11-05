package kernelimage

import (
	"bytes"
	"encoding/binary"
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

func TestChorusMacroAndRouteRequireUniqueTrackStage(t *testing.T) {
	cfg := modulatedConfig()
	cfg.Tracks, cfg.MaxVoices = 2, 2
	cfg.Track[1].Kind = engine.VoiceAcid
	cfg.Patterns = append(cfg.Patterns, engine.PatternBank{})
	params := chorus.DefaultParams()
	stage := engine.TrackEffectConfig{Kind: engine.TrackEffectChorus, Chorus: &params}
	cfg.Track[0].InsertChain = []engine.TrackEffectConfig{stage}
	mapping := engine.MacroMapping{Macro: 7, Track: 0, Param: uint16(kernel.ParamChorusMix), Min: .123, Max: .987}
	cfg.MacroMappings = []engine.MacroMapping{mapping}
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	player, err := engine.New(decoded)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	var wire [13]byte
	wire[0], wire[1] = mapping.Macro, mapping.Track
	binary.LittleEndian.PutUint16(wire[2:], mapping.Param)
	binary.LittleEndian.PutUint32(wire[4:], math.Float32bits(mapping.Min))
	binary.LittleEndian.PutUint32(wire[8:], math.Float32bits(mapping.Max))
	if bytes.Count(data, wire[:]) != 1 {
		t.Fatal("macro record is not unique")
	}
	bad := append([]byte(nil), data...)
	bad[bytes.Index(bad, wire[:])+1] = 1
	if _, err := Decode(bad, 48000, 128); err == nil {
		t.Fatal("decoded macro targeting an absent chorus")
	}
	for _, stages := range [][]engine.TrackEffectConfig{nil, {stage, stage}} {
		cfg.Track[0].InsertChain = stages
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("engine accepted invalid chorus macro owner")
		}
		if _, err := Encode(cfg); err == nil {
			t.Fatal("encoded invalid chorus macro owner")
		}
		cfg.MacroMappings = nil
		cfg.Routes = []engine.RouteSpec{{Source: 0, Track: 0, Param: uint16(kernel.ParamChorusMix), Depth: .2}}
		if _, err := engine.New(cfg); err == nil {
			t.Fatal("engine accepted invalid chorus route owner")
		}
		if _, err := Encode(cfg); err == nil {
			t.Fatal("encoded invalid chorus route owner")
		}
		cfg.Routes = nil
		cfg.MacroMappings = []engine.MacroMapping{mapping}
	}
}
