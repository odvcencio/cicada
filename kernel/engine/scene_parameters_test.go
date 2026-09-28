package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

func TestSceneSettingsDistinguishTrackOwners(t *testing.T) {
	cfg := testConfig()
	cfg.Scenes = []Scene{{Settings: []SceneSetting{
		{Track: 0, ID: kernel.ParamMixGain, Value: -6},
		{Track: 1, ID: kernel.ParamMixGain, Value: -12},
	}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}}
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("different owners are not duplicate paths: %v", err)
	}
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	if e.faulted || e.voices[0].gainDB != -6 || e.voices[1].gainDB != -12 {
		t.Fatal("scene did not apply both track settings")
	}
	cfg.Scenes[0].Settings = append(cfg.Scenes[0].Settings, cfg.Scenes[0].Settings[0])
	if _, err := New(cfg); err == nil {
		t.Fatal("repeated path for the same track must be rejected")
	}
}

func TestSceneSeekRestoresAuthoredParametersBeforeReplay(t *testing.T) {
	cfg := testConfig()
	delay, reverb, compressor, drive := fx.DefaultDelayParams(), fx.DefaultReverbParams(), fx.DefaultCompParams(), fx.DefaultDriveParams()
	delay.Feedback = .23
	reverb.Mix = .37
	cfg.DelayA, cfg.ReverbB, cfg.CompMusic = &delay, &reverb, &compressor
	cfg.Track[0].InsertDrive = &drive
	cfg.Track[0].SendA, cfg.Track[0].SendB = .2, .3
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{
		{Track: 0, ID: kernel.ParamAcidCutoff, Value: 1200},
		{Track: 1, ID: kernel.ParamDrumBdTune, Value: 120},
		{Track: 0, ID: kernel.ParamMixGain, Value: -12},
		{Track: 0, ID: kernel.ParamMixSendA, Value: .6},
		{Track: 0xff, ID: kernel.ParamFxDriveGain, Value: 6},
		{Track: 0xff, ID: kernel.ParamFxDelayFeedback, Value: .8},
		{Track: 0xff, ID: kernel.ParamFxDelayTime, Division: fx.Eighth},
		{Track: 0xff, ID: kernel.ParamFxReverbSize, Value: .7},
		{Track: 0xff, ID: kernel.ParamFxCompRatio, Value: 8},
	}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 0, Bars: 1}}
	newEngine := func() *Engine {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		return e
	}
	reused, fresh := newEngine(), newEngine()
	reused.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1, Arg1: 1})
	if reused.faulted || reused.delayA.Params().Feedback != float64(float32(.8)) {
		t.Fatal("later scene did not change its parameters")
	}
	reused.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
	if reused.faulted {
		t.Fatal("backward seek faulted")
	}
	if reused.delayA.Params() != fresh.delayA.Params() || reused.reverbB.Params() != fresh.reverbB.Params() || reused.compMusic.Params() != fresh.compMusic.Params() {
		t.Fatal("seek retained a later scene's effect parameters")
	}
	if reused.voices[0].acid.Params() != fresh.voices[0].acid.Params() || reused.voices[1].drums.Params(drum.BD) != fresh.voices[1].drums.Params(drum.BD) || reused.voices[0].insert.Params() != fresh.voices[0].insert.Params() {
		t.Fatal("seek retained a later scene's voice or insert parameters")
	}
	if reused.voices[0].mix != fresh.voices[0].mix || reused.voices[0].sendA != fresh.voices[0].sendA {
		t.Fatal("seek retained a later scene's mixer parameters")
	}
	// The same notes after a backward seek must render exactly like a fresh
	// engine, including the effective (smoothed) effect parameters.
	for _, e := range []*Engine{reused, fresh} {
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8})
		e.apply(cmd.Command{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 110<<8})
	}
	var aL, aR, bL, bR [128]float32
	for i := 0; i < 12; i++ {
		reused.Render(aL[:], aR[:])
		fresh.Render(bL[:], bR[:])
		if aL != bL || aR != bR {
			t.Fatalf("backward-seek audio differs at block %d", i)
		}
	}
	// A later scene that omits parameters still inherits earlier authored
	// settings when reconstructing a position beyond them.
	reused.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 2, Arg1: uint32(seq.TicksPerStep)})
	if reused.faulted || reused.delayA.Params().Feedback != float64(float32(.8)) || reused.voices[0].acidTarget.Cutoff != 1200 {
		t.Fatal("seek failed to replay earlier scene settings")
	}
	var left, right [1]float32
	allocs := testing.AllocsPerRun(100, func() {
		reused.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
		reused.Render(left[:], right[:])
	})
	if reused.faulted || allocs != 0 {
		t.Fatalf("seek reconstruction fault=%v allocations=%g", reused.faulted, allocs)
	}
}
