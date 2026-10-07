package kernelimage

import (
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

func TestAutomationImageRoundTripAndMalformedControls(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAcidCutoff), Arg0: math.Float32bits(400)}, {Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamAcidCutoff), Arg0: math.Float32bits(2400), Tick: 15360}}
	data, err := Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Automation, restored.Automation) {
		t.Fatal("automation image changed controls")
	}
	if _, err := Decode(data[:len(data)-1], 48000, 128); err == nil {
		t.Fatal("accepted truncated control")
	}
	bad := append([]byte(nil), data...)
	bad[len(bad)-2*cmd.CommandSize] = byte(cmd.OpPlay)
	if _, err := Decode(bad, 48000, 128); err == nil {
		t.Fatal("accepted non-parameter automation")
	}
	count := len(data) - len(cfg.Automation)*cmd.CommandSize - 2
	bad = append([]byte(nil), data...)
	bad[count], bad[count+1] = 0, 0
	if _, err := Decode(bad, 48000, 128); err == nil {
		t.Fatal("accepted empty capability payload")
	}
}
