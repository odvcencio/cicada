package kernelimage_test

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

func TestGridChainAutomationAndExpressionImageRoundTrip(t *testing.T) {
	if kernelimage.GridCapability&(kernelimage.PMCapability|kernelimage.KeysCapability|kernelimage.PackCapability) != 0 || kernelimage.ChainCapability&(kernelimage.ModalCapability|kernelimage.KeysCapability|kernelimage.PackCapability) != 0 || kernelimage.AutomationCapability&(kernelimage.DDSPCapability|kernelimage.KeysCapability|kernelimage.PackCapability) != 0 {
		t.Fatal("project extensions share a voice capability bit")
	}
	cfg := firstAcidConfig(t)
	cfg.Schedule = engine.LowerSong(cfg.Song)
	cfg.Song = []engine.SongEntry{}
	cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixPan), Arg0: math.Float32bits(.25)}}
	for track := range cfg.Patterns {
		cfg.Patterns[track].Chain = []uint8{0}
		for slot := range cfg.Patterns[track].Slots {
			cfg.Patterns[track].Slots[slot].StepTicks = 320
			if cfg.Patterns[track].Drums != nil {
				for lane := range cfg.Patterns[track].Drums[slot] {
					cfg.Patterns[track].Drums[slot][lane].StepTicks = 320
				}
			}
		}
	}
	cfg.Patterns[0].Slots[0].Expression = new([64]seq.Expression)
	cfg.Patterns[0].Slots[0].Expression[0] = seq.Expression{Set: true, PitchCents: 200}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bits := binary.LittleEndian.Uint16(data[30:32]); bits&(kernelimage.GridCapability|kernelimage.ChainCapability|kernelimage.AutomationCapability|kernelimage.ExpressionCapability) != kernelimage.GridCapability|kernelimage.ChainCapability|kernelimage.AutomationCapability|kernelimage.ExpressionCapability {
		t.Fatalf("missing grid/expression capabilities: %#x", bits)
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("grid/expression image roundtrip: %v", err)
	}
	bits := binary.LittleEndian.Uint16(data[30:32])
	binary.LittleEndian.PutUint16(data[30:32], bits&^kernelimage.GridCapability)
	if _, err := kernelimage.Decode(data, 48000, 128); err == nil {
		t.Fatal("grid image accepted without its capability")
	}
}
