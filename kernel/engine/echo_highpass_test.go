package engine

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/tape"
)

func TestEchoHighpassLiveSceneAndRestart(t *testing.T) {
	for _, id := range []kernel.ParamID{kernel.ParamFxDelayHighpass, kernel.ParamFxTapeHighpass} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			delay := fx.DefaultDelayParams()
			delay.HighpassHz = 300
			tp := tape.DefaultParams()
			tp.HighpassHz = 300
			cfg := rewindConfig(t, false)
			cfg.DelayA, cfg.TapeA = nil, nil
			cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: 255, Index: uint16(id), Arg0: math.Float32bits(500), Tick: 480}}
			if id == kernel.ParamFxDelayHighpass {
				cfg.DelayA = &delay
			} else {
				cfg.TapeA = &tp
			}
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			value := func() float64 {
				if id == kernel.ParamFxDelayHighpass {
					return e.delayA.Params().HighpassHz
				}
				return e.tapeA.Params().HighpassHz
			}
			e.setGlobalParam(id, 800)
			if got := value(); got != 800 {
				t.Fatalf("live set: %g", got)
			}
			e.applySceneSetting(SceneSetting{Track: 255, ID: id, Value: float32(math.Inf(-1)), Division: fx.FreeDelay}, false)
			if got := value(); got != 0 {
				t.Fatalf("scene off: %g", got)
			}
			if len(e.automationLanes) == 0 {
				t.Fatal("automation lane not registered")
			}
			if !e.restartAutomationLanes() {
				t.Fatal("restart fault")
			}
			if got := value(); got != 300 {
				t.Fatalf("restart: %g", got)
			}
		})
	}
}
