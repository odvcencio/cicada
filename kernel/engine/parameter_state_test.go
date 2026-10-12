package engine

import (
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
)

type parameterTargetState struct {
	sceneParameterState
	muted, soloed [16]bool
}

func parameterTargets(e *Engine) parameterTargetState {
	state := parameterTargetState{sceneParameterState: sceneParameterState{layerMask: e.layerAuthored}}
	for i := 0; i < e.tracks; i++ {
		v, p := &e.voices[i], &state.tracks[i]
		state.muted[i], state.soloed[i] = v.muted, v.soloed
		p.mix, p.gainDB, p.pan = v.targetMix, v.gainDB, v.pan
		p.sendA, p.sendB, p.sourceOff = v.targetSendA, v.targetSendB, v.sourceOff
		p.acid, p.drums, p.pianoSustain = v.acidTarget, v.drumTargets, v.pianoSustain
		if v.guitar != nil {
			p.guitar = v.guitar.Params()
		}
		if v.insert != nil {
			p.drive = v.insert.Params()
		}
	}
	state.delay, state.reverb, state.compressor = e.delayA.Params(), e.reverbB.Params(), e.compMusic.Params()
	return state
}

func TestCommittedBatchReconstructsEveryParameterTarget(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 4, 32
	cfg.Track[2].Kind, cfg.Track[2].Guitar = VoiceGuitar, guitar.DefaultParams()
	cfg.Track[3].Kind = VoicePiano
	cfg.Track[2].Experimental, cfg.Track[3].Experimental = true, true
	delay, reverb, comp, drive := fx.DefaultDelayParams(), fx.DefaultReverbParams(), fx.DefaultCompParams(), fx.DefaultDriveParams()
	cfg.DelayA, cfg.ReverbB, cfg.CompMusic = &delay, &reverb, &comp
	cfg.Track[0].InsertDrive = &drive
	kit := new([drum.LaneCount]KitLaneBinding)
	for lane := range kit {
		kit[lane] = KitLaneBinding{Kind: KitLaneBuiltin, Recipe: drum.Lane(lane)}
	}
	kit[drum.BD].Recipe = drum.SD
	cfg.Track[1].Kit = kit
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: 0, ID: kernel.ParamMixGain, Value: -9}, {Track: 0xff, ID: kernel.ParamFxDelayFeedback, Value: .7}}}, {}}
	cfg.Song, cfg.LoopSong = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}, true
	patched, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var commands []cmd.Command
	for _, spec := range kernel.Params {
		if !spec.Live {
			continue
		}
		for track := 0; track < 4; track++ {
			index := uint8(track)
			if spec.Scope == "global" {
				if track != 0 {
					continue
				}
				index = 0xff
			} else if !strings.HasPrefix(spec.Name, "mix.") && !(track == 0 && strings.HasPrefix(spec.Name, "acid.")) && !(track == 1 && strings.HasPrefix(spec.Name, "drum.")) && !(track == 2 && strings.HasPrefix(spec.Name, "guitar.")) && !(track == 3 && strings.HasPrefix(spec.Name, "piano.")) {
				continue
			}
			value := spec.Default
			if spec.Curve != "toggle" {
				value = min(spec.Max, value+max(spec.DisplayStep, .01))
			}
			validator := patched.PreviewParamValidator(track)
			if track == 1 {
				if effective, ok := validator.CommittedValue(spec.ID); ok {
					value = float32(effective)
				}
			}
			if validator.Validate(spec.ID, value) != nil {
				continue
			}
			commands = append(commands, cmd.Command{Op: cmd.OpSetParam, Track: index, Index: uint16(spec.ID), Arg0: math.Float32bits(value)})
		}
	}
	for _, c := range commands {
		fresh.setParamImmediate(c)
	}
	if fresh.faulted {
		t.Fatal("reference source preparation faulted")
	}
	// Capture the updated authored defaults cold, as a newly compiled source does.
	fresh.captureSceneDefaults()
	if !patched.PushCommittedBatch(commands) {
		t.Fatal("saved defaults rejected")
	}
	var left, right [128]float32
	for _, e := range []*Engine{patched, fresh} {
		e.meterRate = 0
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		e.Render(left[:], right[:])
	}
	for _, bar := range []uint32{2, 0, 1, 2, 3, 0} {
		for _, e := range []*Engine{patched, fresh} {
			if !e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: bar, Arg1: 1}) {
				t.Fatal("seek rejected")
			}
			e.Render(left[:], right[:])
		}
		if patched.faulted || fresh.faulted {
			t.Fatal("seek faulted")
		}
		if got, want := parameterTargets(patched), parameterTargets(fresh); got != want {
			t.Fatalf("bar %d: saved parameter reconstruction differs from fresh source", bar)
		}
	}
	t.Logf("%d committed controls across four voice families and effects; six forward/backward/loop seeks compare every target", len(commands))
}

func TestCommandCapacityIncludesFuturePendingCommands(t *testing.T) {
	e, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	var commands [CommandCapacity]cmd.Command
	for i := range commands {
		commands[i] = cmd.Command{Op: cmd.OpSetParam, Track: 0, Index: uint16(kernel.ParamMixGain), Arg0: math.Float32bits(-6), Tick: 100_000}
	}
	if !e.PushBatch(commands[:]) {
		t.Fatal("empty queue rejected capacity batch")
	}
	var left, right [128]float32
	e.Render(left[:], right[:])
	if e.faulted || e.AvailableCommands() != 0 {
		t.Fatal("future commands were not retained")
	}
	if e.Push(commands[0]) || e.PushCommittedBatch(commands[:1]) {
		t.Fatal("publication ignored pending capacity")
	}
	e.Render(left[:], right[:])
	if e.faulted {
		t.Fatal("rejected pressure faulted engine")
	}
}
