package kernelimage

import (
	"encoding/binary"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
)

func modulatedConfig() engine.Config {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, Seed: 4242, LoopSong: true}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Patterns = []engine.PatternBank{{}}
	cfg.Modulators = []engine.ModulatorSpec{
		{Kind: engine.ModKindLFO, Wave: engine.ModWaveTriangle, RateTicks: 7680, PhaseQ32: 1 << 30, Continue: true},
		{Kind: engine.ModKindSway, Octaves: 3, RateMicroHz: 70_000},
		{Kind: engine.ModKindEnvelope, Source: 0, Attack: 1 << 30, Release: 1 << 24},
		{Kind: engine.ModKindContour, RateTicks: 3840, Points: []engine.ContourPoint{{At: 0, Value: -1 << 30}, {At: 1 << 31, Value: 1 << 29, Shape: engine.ModShapeSmooth}, {At: 1 << 32, Value: -1 << 30}}},
	}
	cfg.Routes = []engine.RouteSpec{
		{Source: 0, Track: 0, Param: uint16(kernel.ParamMixPan), Depth: .25},
		{Source: 1, Track: 0, Param: uint16(kernel.ParamAcidCutoff), Multiply: true, Depth: .5},
	}
	return cfg
}

func TestModulatorTableRoundTripsWithoutANewVersion(t *testing.T) {
	cfg := modulatedConfig()
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	plainCfg := cfg
	plainCfg.Modulators, plainCfg.Routes = nil, nil
	plainData, err := Encode(plainCfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:]) != binary.LittleEndian.Uint16(plainData[4:]) {
		t.Fatal("modulators changed the image version")
	}
	if flags := binary.LittleEndian.Uint32(data[8:]); flags != 1|ModulatorFlag {
		t.Fatalf("flags %#x", flags)
	}
	restored, err := Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Modulators, restored.Modulators) || !reflect.DeepEqual(cfg.Routes, restored.Routes) {
		t.Fatalf("table changed:\n%+v\n%+v", cfg.Modulators, restored.Modulators)
	}
	again, err := Encode(restored)
	if err != nil || string(again) != string(data) {
		t.Fatal("re-encoding changed the image")
	}
	// The table is the last thing in the image; any cut or extra byte fails.
	if _, err := Decode(data[:len(data)-1], 48000, 128); err == nil {
		t.Error("accepted a truncated table")
	}
	if _, err := Decode(append(append([]byte(nil), data...), 0), 48000, 128); err == nil {
		t.Error("accepted trailing bytes")
	}
	// An image without modulators keeps the old flags and has no table.
	plain := cfg
	plain.Modulators, plain.Routes = nil, nil
	data, err = Encode(plain)
	if err != nil {
		t.Fatal(err)
	}
	if flags := binary.LittleEndian.Uint32(data[8:]); flags != 1 {
		t.Fatalf("plain flags %#x", flags)
	}
}

func TestModulatorImageRefusesMalformedTables(t *testing.T) {
	data, err := Encode(modulatedConfig())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]byte){
		"unknown flag bit":         func(b []byte) { b[8] |= 1 << 2 },
		"route to an unlisted row": func(b []byte) { binary.LittleEndian.PutUint16(b[len(b)-7:], uint16(kernel.ParamMixMute)) },
		"bad route mode":           func(b []byte) { b[len(b)-5] = 2 },
	} {
		bad := append([]byte(nil), data...)
		mutate(bad)
		if _, err := Decode(bad, 48000, 128); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
