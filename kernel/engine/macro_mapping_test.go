package engine

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
)

func TestMappedMacrosFollowSmoothedValues(t *testing.T) {
	cfg := modConfig(t)
	cfg.Modulators, cfg.Routes = nil, nil
	cfg.MacroMappings = []MacroMapping{{Macro: 0, Track: 0, Param: uint16(kernel.ParamAcidCutoff), Min: 200, Max: 4000, Exponential: true}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defineMacro(t, e, 0, 0)
	pushMacroCommand(t, e, cmd.Command{Op: cmd.OpPlay, Track: 255})
	renderMacroFrames(t, e, 1, 1)
	if got := e.voices[0].acidTarget.Cutoff; got != 200 {
		t.Fatalf("initial cutoff = %g", got)
	}
	setMacro(t, e, 0, 1, 256)
	renderMacroFrames(t, e, 128, 128)
	if got := e.voices[0].acidTarget.Cutoff; math.Abs(got-200*math.Pow(20, 127.0/256)) > 3 {
		t.Fatalf("mid-ramp exponential cutoff = %g", got)
	}
	renderMacroFrames(t, e, 256, 128)
	if got := e.voices[0].acidTarget.Cutoff; got != 4000 {
		t.Fatalf("final cutoff = %g", got)
	}
}
