package engine

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

// restoreTracks lays out one track per voice family so every live parameter
// has an owner: 0 acid (also the mixer's track), 1 drums, 2 guitar, 3 piano,
// 4..9 the six keyboard families.
const restoreTracks = 10

var restoreFamilyTrack = map[string]uint8{"acid": 0, "drums": 1, "guitar": 2, "piano": 3, "ep": 4, "clav": 5, "organ": 6, "fm": 7, "analog": 8, "strings": 9}

func restoreBase(t *testing.T) Config {
	installControlKeys(t, 1)
	delay, reverb, comp, drive := fx.DefaultDelayParams(), fx.DefaultReverbParams(), fx.DefaultCompParams(), fx.DefaultDriveParams()
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: restoreTracks, MaxVoices: 32, BPMMilli: 300_000, Seed: 7, DelayA: &delay, ReverbB: &reverb, CompMusic: &comp}
	cfg.Track[0] = TrackConfig{Kind: VoiceAcid, InsertDrive: &drive, SendA: .1, SendB: .1}
	cfg.Track[1].Kind = VoiceDrums
	cfg.Track[2] = TrackConfig{Kind: VoiceGuitar, Experimental: true, Guitar: guitar.DefaultParams()}
	cfg.Track[3].Kind = VoicePiano
	for i, patch := range []uint8{1, 7, 12, 14, 19, 21} {
		spec := &keyboard.Spec{Patch: patch}
		spec.Controls[127] = 1
		switch patch {
		case 1:
			spec.Controls[8], spec.Controls[10] = 4.6, .7 // Tremolo rate and gain have nonzero minimums or defaults.
		case 14:
			spec.Controls[1] = 1 // FM gain has a minimum of 0.001.
		}
		cfg.Track[4+i] = TrackConfig{Kind: VoiceKeys, Keys: spec}
	}
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		cfg.Track[1].Drums[lane] = drum.DefaultParams(lane)
	}
	return cfg
}

// restoreTarget picks the track that owns a live parameter and a value to move
// it to, away from every authored value.
func restoreTarget(d paramdefs.Descriptor) (track uint8, value float32, ok bool) {
	if d.Scope == "global" {
		track = 0xff
	} else {
		for _, voice := range d.Voices {
			if t, found := restoreFamilyTrack[voice]; found {
				track, ok = t, true
				if voice != "instrument" {
					break
				}
			}
		}
		if !ok {
			return 0, 0, false
		}
	}
	v := d.Min + .31*(d.Max-d.Min)
	if d.Curve == "toggle" || d.Type == "integer" {
		v = math.Round(v)
	}
	if d.Curve == "toggle" {
		v = 1
	}
	return track, float32(v), true
}

// engineState collects every value the parameter switch can change, current and
// target, so a restore that misses one shows up as a difference.
func engineState(e *Engine) string {
	type track struct {
		Mix, Target                                 any
		Gain, Pan, SendA, SendB, TSendA, TSendB     float32
		Off, Muted, Soloed                          bool
		MuteGain, MuteTarget, Sustain, PosG, PosT   float32
		Acid, Guitar, Drive, KeyValues, Keys, Drums any
	}
	var all []track
	for i := 0; i < e.tracks; i++ {
		v := &e.voices[i]
		s := track{Mix: v.mix, Target: v.targetMix, Gain: v.gainDB, Pan: v.pan, SendA: v.sendA, SendB: v.sendB, TSendA: v.targetSendA, TSendB: v.targetSendB,
			Off: v.sourceOff, Muted: v.muted, Soloed: v.soloed, MuteGain: v.muteGain, MuteTarget: v.muteTarget, Sustain: v.pianoSustain,
			PosG: e.spatial.tracks[i].gain, PosT: e.spatial.tracks[i].target, KeyValues: v.keysValues}
		s.Acid = v.acidTarget
		if v.acid != nil {
			s.Acid = [2]any{v.acidTarget, v.acid.Params()}
		}
		if v.guitar != nil {
			s.Guitar = v.guitar.Params()
		}
		if v.insert != nil {
			s.Drive = v.insert.Params()
		}
		if f, ok := v.keys.(*controlKeys); ok {
			s.Keys = f.values
		}
		if v.drums != nil {
			var lanes []drum.Params
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				lanes = append(lanes, v.drums.Params(lane))
			}
			s.Drums = [2]any{v.drumTargets, lanes}
		}
		all = append(all, s)
	}
	// Parameters travel as float32, so compare them to the precision they have.
	state := roundNumbers(fmt.Sprintf("%+v", all))
	state += fmt.Sprintf("|solo=%d layers=%v", e.soloCount, e.layerAuthored)
	if e.delayA != nil {
		state += roundNumbers(fmt.Sprintf("|%+v", e.delayA.Params()))
	}
	if e.reverbB != nil {
		state += roundNumbers(fmt.Sprintf("|%+v", e.reverbB.Params()))
	}
	if e.compMusic != nil {
		state += roundNumbers(fmt.Sprintf("|%+v", e.compMusic.Params()))
	}
	return state
}

// Every live parameter, moved by automation or by a scene, returns to the state
// a fresh engine has after a seek to the start, a loop and a replay, in song and
// arrange schedules.
func TestEveryLiveParameterRestoresLikeAFreshEngine(t *testing.T) {
	const twoBarFrames = 76_800 // Two bars at 300 BPM.
	cases := 0
	for _, d := range paramdefs.Registry {
		track, value, ok := restoreTarget(d)
		if !d.Live || !ok || d.Off && false {
			continue
		}
		id, found := kernel.FindParam(d.ID)
		if !found {
			t.Fatalf("%s has no kernel ID", d.ID)
		}
		for _, variant := range []string{"automation", "scene"} {
			if variant == "automation" && !d.Automatable {
				continue
			}
			for _, placement := range []bool{false, true} {
				if variant == "scene" && placement {
					continue // Arrange schedules have no scenes.
				}
				for _, loop := range []bool{false, true} {
					if variant == "scene" && loop {
						continue // Scene settings carry over a loop by design.
					}
					name := fmt.Sprintf("%s/%s/%s/loop=%v", d.ID, variant, scheduleKindName(placement), loop)
					t.Run(name, func(t *testing.T) {
						cfg := restoreBase(t)
						cfg.LoopSong = loop
						movedAfter := 9600
						switch {
						case placement:
							cfg.Patterns = make([]PatternBank, restoreTracks)
							cfg.Patterns[1].Drums = new([16][drum.LaneCount]seq.Pattern)
							cfg.Patterns[1].Drums = new([16][drum.LaneCount]seq.Pattern)
							cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 50}
							cfg.Schedule = []ScheduleEvent{
								{Kind: SchedulePattern, Track: 0, Index: 0, Tick: 0, EndTick: 7680, ID: 1},
								{Kind: SchedulePatternEnd, Track: 0, Tick: 7680, EndTick: 7680, ID: 1},
							}
						case variant == "scene":
							cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{{Track: track, ID: id, Value: value}}}}
							cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}}
							movedAfter = twoBarFrames/2 + 9600
						default:
							cfg.Scenes = []Scene{{}}
							cfg.Song = []SongEntry{{Scene: 0, Bars: 2}}
						}
						if variant == "automation" {
							cfg.Automation = []cmd.Command{{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(value), Tick: 600}}
						}
						newEngine := func() *Engine {
							e, err := New(cfg)
							if err != nil {
								t.Fatal(err)
							}
							e.meterRate = 0
							return e
						}
						var l, r [128]float32
						frames := 0
						render := func(e *Engine, n int) {
							for ; n > 0; n -= len(l) {
								e.Render(l[:], r[:])
								frames += len(l)
							}
						}
						fresh := newEngine()
						fresh.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
						fresh.Render(l[:1], r[:1])
						want := engineState(fresh)
						e := newEngine()
						e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
						render(e, movedAfter)
						if engineState(e) == want {
							t.Fatal("the parameter never moved")
						}
						check := func(moment string) {
							t.Helper()
							e.Render(l[:1], r[:1])
							if got := engineState(e); got != want {
								t.Fatalf("after %s the engine state differs from a fresh engine's:\n got %.600s\nwant %.600s", moment, got, want)
							}
						}
						if loop {
							frames = ((frames + 127) / 128) * 128
							render(e, twoBarFrames-frames)
							check("the loop wraps")
							return
						}
						e.Push(cmd.Command{Op: cmd.OpSeek, Track: 255})
						check("a seek to the start")
						render(e, movedAfter)
						if engineState(e) == want {
							t.Fatal("the lane did not run again after the seek")
						}
						for i := 0; i < 5000 && e.Playing(); i++ {
							e.Render(l[:], r[:])
						}
						e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
						check("a replay after the end")
						var message cmd.Message
						for e.Poll(&message) {
							if message.Kind == cmd.Fault {
								t.Fatalf("fault %d", message.A)
							}
						}
					})
					cases++
				}
			}
		}
	}
	if cases < 150 {
		t.Fatalf("only %d cases", cases)
	}
}

var numberPattern = regexp.MustCompile(`-?[0-9]+\.[0-9]+(?:e[+-]?[0-9]+)?`)

// roundNumbers rewrites every decimal number to seven significant digits.
func roundNumbers(text string) string {
	return numberPattern.ReplaceAllStringFunc(text, func(number string) string {
		value, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return number
		}
		return strconv.FormatFloat(value, 'g', 7, 64)
	})
}
