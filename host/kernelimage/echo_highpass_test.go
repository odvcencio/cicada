package kernelimage

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
)

// A portable image cannot carry the echo high-pass: the core kernel's
// parameter table does not reach its row. The encoder refuses a delay that
// sets it, and a scene or automation lane that moves it, as it refuses a
// tape echo. A delay without it still encodes.
func TestEncodeRefusesTheEchoHighpass(t *testing.T) {
	delay := fx.DefaultDelayParams()
	base := engine.Config{SampleRate: 48000, BPMMilli: 120000, Tracks: 1, DelayA: &delay}
	if usesEchoHighpass(&base) {
		t.Fatal("a delay without highpass counts as using it")
	}
	set := delay
	set.HighpassHz = 300
	withParam := base
	withParam.DelayA = &set
	moved := base
	moved.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 0xff, Index: uint16(kernel.ParamFxDelayHighpass), Tick: 0}}
	scene := base
	scene.Scenes = []engine.Scene{{Settings: []engine.SceneSetting{{Track: 0xff, ID: kernel.ParamFxDelayHighpass, Value: 400}}}}
	for name, cfg := range map[string]engine.Config{"highpass = 300Hz": withParam, "automate": moved, "scene": scene} {
		if _, err := Encode(cfg); err == nil || !strings.Contains(err.Error(), "portable images have no echo highpass") {
			t.Errorf("%s: want the echo highpass refusal, got %v", name, err)
		}
	}
}
