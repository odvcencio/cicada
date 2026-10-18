package engine

import (
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/piano"
)

func packedDrum(t *testing.T, lane drum.Lane) uint32 {
	t.Helper()
	v, err := seq.PackStep(seq.Step{Note: uint8(lane), Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDrumStepPatchLandsAtNextStepAndIsBlockInvariant(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices, cfg.MaxBlock = 2, 12, 256
	build := func() *Engine {
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		setup := []cmd.Command{
			{Op: cmd.OpSetPatternLen, Track: 1, Index: 4},          // slot 0: four steps, empty
			{Op: cmd.OpSetPatternLen, Track: 1, Index: 4, Arg1: 1}, // slot 1: four steps
			{Op: cmd.OpSetStep, Track: 1, Index: 0, Arg0: packedDrum(t, drum.BD), Arg1: 1},
			{Op: cmd.OpSelectPattern, Track: 1},
			{Op: cmd.OpPlay, Track: 0xff},
		}
		if !e.PushBatch(setup) {
			t.Fatal("setup rejected")
		}
		return e
	}
	a, b := build(), build()
	var aL, aR, bL, bR [256]float32
	var hits []cmd.Message
	for block := 0; block < 1600; block++ {
		if block == 100 {
			next := (a.transport.Tick()/seq.TicksPerStep + 1) * seq.TicksPerStep
			hit := cmd.Command{Op: cmd.OpSetStep, Track: 1, Index: 2, Arg0: packedDrum(t, drum.SD), Tick: next}
			if !a.Push(hit) || !b.Push(hit) {
				t.Fatal("drum step rejected")
			}
		}
		if block == 600 {
			launch := cmd.Command{Op: cmd.OpSelectPattern, Track: 1, Index: 1, Arg0: 2}
			if !a.Push(launch) || !b.Push(launch) {
				t.Fatal("launch rejected")
			}
		}
		a.Render(aL[:], aR[:])
		b.Render(bL[:128], bR[:128])
		b.Render(bL[128:], bR[128:])
		if aL != bL || aR != bR {
			t.Fatalf("drum audio depends on block size at block %d", block)
		}
		var am, bm cmd.Message
		for {
			aok, bok := a.Poll(&am), b.Poll(&bm)
			if aok != bok || aok && am != bm {
				t.Fatalf("messages differ at block %d: %+v / %+v", block, am, bm)
			}
			if !aok {
				break
			}
			if am.Kind == cmd.Fault {
				t.Fatalf("fault %d", am.A)
			}
			if am.Kind == cmd.NoteOn && am.Track == 1 && am.A == uint16(drum.SD) {
				hits = append(hits, am)
			}
		}
	}
	if len(hits) == 0 || hits[0].Tick%(4*seq.TicksPerStep) != 2*seq.TicksPerStep || hits[0].Tick < 1_024 {
		t.Fatalf("snare did not land on step 2 after the edit: %+v", hits)
	}
	if last := hits[len(hits)-1]; last.Tick >= 7_680 {
		t.Fatalf("snare kept sounding after the launch replaced slot 0: %+v", last)
	}
}

type liveEditKind struct {
	name   string
	config func(t *testing.T) Config
	chord  bool
}

func liveEditPitched(note uint8) uint32 { return packedNoteValue(note) }

// liveEditBase builds one track with two eight-step slots. Slot 0 sounds on
// steps 0 and 4; slot 1 sounds on steps 1 and 5. The song is three one-bar
// scenes; scene 1 changes the tempo, so the loop end lies after a tempo change.
func liveEditBase(track TrackConfig, drums bool) Config {
	cfg := Config{SampleRate: 48_000, MaxBlock: 256, Tracks: 1, MaxVoices: 12, BPMMilli: 120_000, Seed: 4242, LoopSong: true}
	cfg.Track[0] = track
	cfg.Patterns = []PatternBank{{}}
	if drums {
		cfg.Patterns[0].Drums = new([16][drum.LaneCount]seq.Pattern)
	}
	for slot, steps := range [2][2]int{{0, 4}, {1, 5}} {
		pattern := seq.Pattern{Len: 8, StepTicks: 480, GatePercent: 55, Seed: cfg.Seed}
		cfg.Patterns[0].Slots[slot] = pattern
		if drums {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				cfg.Patterns[0].Drums[slot][lane] = pattern
			}
			for _, step := range steps {
				cfg.Patterns[0].Drums[slot][drum.BD].Steps[step] = liveEditDrum(drum.BD)
			}
			continue
		}
		for _, step := range steps {
			cfg.Patterns[0].Slots[slot].Steps[step] = liveEditPitched(60 + uint8(step))
		}
	}
	cfg.Scenes = []Scene{
		{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot, Slot: 0}}},
		{Settings: []SceneSetting{{Track: 0xff, ID: kernel.ParamGlobalTempo, Value: 90}}},
		{},
	}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 2, Bars: 1}}
	return cfg
}

func liveEditDrum(lane drum.Lane) uint32 {
	v, err := seq.PackStep(seq.Step{Note: uint8(lane), Gate: true, Ratchet: 1, Probability: 100, Velocity: 100})
	if err != nil {
		panic(err)
	}
	return v
}

func liveEditKinds() []liveEditKind {
	graphProgram := graph.Program{Len: 6, Output: 5, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}, {Op: graph.Sine, A: 0}, {Op: graph.Gate}, {Op: graph.Constant, Value: 100}, {Op: graph.Envelope, A: 2, B: 3}, {Op: graph.Multiply, A: 1, B: 4}}}
	return []liveEditKind{
		{name: "acid", config: func(*testing.T) Config { return liveEditBase(TrackConfig{Kind: VoiceAcid}, false) }},
		{name: "graph", config: func(*testing.T) Config {
			return liveEditBase(TrackConfig{Kind: VoiceGraph, Graph: graphProgram}, false)
		}},
		{name: "graph-poly", chord: true, config: func(*testing.T) Config {
			return liveEditBase(TrackConfig{Kind: VoiceGraph, Polyphony: 4, Graph: graphProgram}, false)
		}},
		{name: "piano", chord: true, config: func(*testing.T) Config {
			cfg := liveEditBase(TrackConfig{Kind: VoicePiano}, false)
			cfg.MaxVoices = piano.MaxVoices
			return cfg
		}},
		{name: "keys", chord: true, config: func(t *testing.T) Config {
			if !keysEnabled {
				t.Skip("keyboard voices are compiled out of the core build")
			}
			installFakeKeys(t)
			cfg := liveEditBase(TrackConfig{Kind: VoiceKeys, Keys: &keyboard.Spec{Patch: 1}}, false)
			cfg.Track[0].Keys.Controls[127] = 8
			cfg.MaxVoices = 8
			return cfg
		}},
		{name: "sample", config: func(*testing.T) Config {
			cfg := liveEditBase(TrackConfig{Kind: VoiceSample, Sample: &SamplerConfig{RootKey: 60, Voices: 2, Loop: true}}, false)
			cfg.Assets = []AudioAsset{{SampleRate: 48_000, Left: []float32{0.1, 0.05, -0.1, -0.05}}}
			return cfg
		}},
		{name: "modal", config: func(*testing.T) Config { return liveEditBase(TrackConfig{Kind: VoiceModal}, false) }},
		{name: "drums", config: func(*testing.T) Config { return liveEditBase(TrackConfig{Kind: VoiceDrums}, true) }},
	}
}

type liveEditRun struct {
	left, right []float32
	noteOns     []cmd.Message
	switched    []cmd.Message
}

// renderLiveEdit renders the same commands with the given block partition.
// Edits and launches go in at the same frame whatever the partition.
func renderLiveEdit(t *testing.T, cfg Config, split []int, edit []cmd.Command, editFrame, launchFrame, frames int) liveEditRun {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	if !e.PushBatch([]cmd.Command{{Op: cmd.OpSelectPattern, Track: 0, Index: 0}, {Op: cmd.OpPlay, Track: 0xff}}) {
		t.Fatal("setup rejected")
	}
	var run liveEditRun
	l, r := make([]float32, 256), make([]float32, 256)
	var m cmd.Message
	drain := func() {
		for e.Poll(&m) {
			switch m.Kind {
			case cmd.Fault:
				t.Fatalf("fault %d", m.A)
			case cmd.NoteOn:
				run.noteOns = append(run.noteOns, m)
			case cmd.Switched:
				run.switched = append(run.switched, m)
			}
		}
	}
	pushAt := func(frame int, done *bool, push func()) {
		if !*done && frame >= 0 {
			*done = true
			push()
		}
	}
	var edited, launched bool
	for done, i := 0, 0; done < frames; i++ {
		n := min(split[i%len(split)], frames-done)
		if done >= editFrame && len(edit) > 0 {
			pushAt(0, &edited, func() {
				next := (e.transport.Tick()/seq.TicksPerStep + 1) * seq.TicksPerStep
				batch := append([]cmd.Command(nil), edit...)
				for j := range batch {
					batch[j].Tick = next
				}
				if !e.PushBatch(batch) {
					t.Fatal("edit rejected")
				}
			})
		}
		if done >= launchFrame {
			pushAt(0, &launched, func() {
				if !e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0, Index: 1, Arg0: 2}) {
					t.Fatal("launch rejected")
				}
			})
		}
		e.Render(l[:n], r[:n])
		run.left, run.right = append(run.left, l[:n]...), append(run.right, r[:n]...)
		drain()
		done += n
	}
	return run
}

// Every pattern voice kind takes a step patch at the next step boundary, keeps
// it across the song loop end (after a scene tempo change) and a bar-quantized
// launch, and renders identically for different block partitions.
func TestStepPatchLandsAtNextStepForEveryKindAcrossLoopAndLaunch(t *testing.T) {
	const editFrame, launchFrame, frames = 36_864, 400_384, 614_400 // loop end near frame 352,000
	for _, kind := range liveEditKinds() {
		t.Run(kind.name, func(t *testing.T) {
			cfg := kind.config(t)
			drums := kind.name == "drums"
			var edit []cmd.Command
			if drums {
				edit = []cmd.Command{{Op: cmd.OpSetStep, Track: 0, Index: 2, Arg0: liveEditDrum(drum.SD)}}
			} else {
				edit = []cmd.Command{{Op: cmd.OpSetStep, Track: 0, Index: 2, Arg0: liveEditPitched(67)}}
				if kind.chord {
					chord, err := cmd.ChordStepCommand(0, 0, 2, [4]uint8{67, 71, 74}, 3)
					if err != nil {
						t.Fatal(err)
					}
					edit = append(edit, chord)
				}
			}
			whole := renderLiveEdit(t, cfg, []int{256}, edit, editFrame, launchFrame, frames)
			split := renderLiveEdit(t, cfg, []int{100, 156}, edit, editFrame, launchFrame, frames)
			base := renderLiveEdit(t, cfg, []int{256}, nil, editFrame, launchFrame, frames)
			for i := range whole.left {
				if whole.left[i] != split.left[i] || whole.right[i] != split.right[i] {
					t.Fatalf("audio depends on block partition at frame %d", i)
				}
			}
			if len(whole.noteOns) != len(split.noteOns) {
				t.Fatalf("note count depends on block partition: %d / %d", len(whole.noteOns), len(split.noteOns))
			}
			for i := range whole.noteOns {
				if whole.noteOns[i] != split.noteOns[i] {
					t.Fatalf("note %d differs by partition: %+v / %+v", i, whole.noteOns[i], split.noteOns[i])
				}
			}
			lane := uint16(67)
			if drums {
				lane = uint16(drum.SD)
			}
			var firstEdited, launchTick int64 = -1, 0
			for _, s := range whole.switched {
				if s.A == 1 {
					launchTick = s.Tick
				}
			}
			if launchTick == 0 || launchTick%seq.TicksPerBar != 0 {
				t.Fatalf("launch did not land on a bar: %+v", whole.switched)
			}
			editedHits, loopHits := 0, 0
			for _, n := range whole.noteOns {
				if n.A != lane {
					continue
				}
				if firstEdited < 0 {
					firstEdited = n.Tick
				}
				editedHits++
				if n.Tick >= 3*seq.TicksPerBar {
					loopHits++
				}
				if n.Tick >= launchTick {
					t.Fatalf("edited step kept sounding after slot 1 replaced slot 0: %+v", n)
				}
			}
			for _, n := range base.noteOns {
				if n.A == lane {
					t.Fatalf("baseline already sounds the edited note: %+v", n)
				}
			}
			if firstEdited < 0 || firstEdited%(8*480) != 2*480 || firstEdited < 1_680 {
				t.Fatalf("edit did not land on step 2 after the push: first %d", firstEdited)
			}
			if loopHits == 0 {
				t.Fatalf("edit was lost at the song loop end (%d hits)", editedHits)
			}
		})
	}
}

func TestSceneBindingCommandChangesNextLaunch(t *testing.T) {
	cfg := testConfig()
	cfg.Tracks, cfg.MaxVoices = 1, 1
	cfg.Patterns = []PatternBank{{}}
	for slot := range 4 {
		cfg.Patterns[0].Slots[slot] = seq.Pattern{Len: 4, GatePercent: 55, Seed: 1}
	}
	cfg.Scenes = []Scene{{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot, Slot: 0}}}}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	bind := cmd.Command{Op: cmd.OpSetSceneBinding, Track: 0xff, Index: 0, Arg0: 0 | uint32(SceneSlot)<<8 | 3<<16}
	if !e.PushBatch([]cmd.Command{bind, {Op: cmd.OpLaunchScene, Track: 0xff}, {Op: cmd.OpPlay, Track: 0xff}}) {
		t.Fatal("rejected")
	}
	var l, r [128]float32
	e.Render(l[:], r[:])
	if e.patterns[0].active != 3 {
		t.Fatalf("binding not applied: active slot %d", e.patterns[0].active)
	}
	faultOf := func(c cmd.Command) uint16 {
		t.Helper()
		if !e.Push(c) {
			t.Fatal("validation must accept the wire form; the engine faults")
		}
		e.Render(l[:], r[:])
		var m cmd.Message
		code := uint16(0)
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				code = m.A
			}
		}
		return code
	}
	if code := faultOf(cmd.Command{Op: cmd.OpSetSceneBinding, Track: 0xff, Index: 9, Arg0: 0}); code != FaultSceneBinding {
		t.Fatalf("out-of-range scene faulted with %d", code)
	}
}

func TestSceneBindingFaultsOnUnusableTargets(t *testing.T) {
	newEngine := func() *Engine {
		cfg := testConfig()
		cfg.Tracks, cfg.MaxVoices = 1, 1
		cfg.Patterns = []PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 4, GatePercent: 55, Seed: 1}
		cfg.Scenes = []Scene{{}}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for name, c := range map[string]cmd.Command{
		"track beyond the session": {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg0: 1 | uint32(SceneOff)<<8},
		"clip on a pattern track":  {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg0: uint32(SceneClip) << 8},
	} {
		e := newEngine()
		if !e.Push(c) {
			t.Fatalf("%s: wire form rejected", name)
		}
		var l, r [128]float32
		e.Render(l[:], r[:])
		var m cmd.Message
		code := uint16(0)
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				code = m.A
			}
		}
		if code != FaultSceneBinding {
			t.Fatalf("%s: fault %d", name, code)
		}
	}
	for name, c := range map[string]cmd.Command{
		"track beyond build": {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg0: uint32(MaxTracks)},
		"mode 4":             {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg0: 4 << 8},
		"slot 16":            {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg0: 16 << 16},
		"reserved byte":      {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg0: 1 << 24},
		"clip 65536":         {Op: cmd.OpSetSceneBinding, Track: 0xff, Arg1: 65536},
		"track-addressed":    {Op: cmd.OpSetSceneBinding, Track: 0},
		"reserved retire":    {Op: cmd.OpRetire, Track: 0xff},
		"reserved loop":      {Op: cmd.OpSetLoop, Track: 0xff},
		"unknown after bind": {Op: cmd.OpSetSceneBinding + 1, Track: 0xff},
	} {
		if err := c.Validate(1); err == nil {
			t.Fatalf("%s: validation accepted %+v", name, c)
		}
	}
}

func TestStereoMeterQuantitiesFollowPan(t *testing.T) {
	build := func() *Engine {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000}
		cfg.Track[0] = TrackConfig{Kind: VoiceGraph, Graph: sineProgram(375, 1), GainDB: 0, GainSet: true, Pan: -1}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	collect := func(e *Engine) (values map[uint16]float32, quantities int) {
		values = map[uint16]float32{}
		var left, right [128]float32
		var m cmd.Message
		for range 8 {
			e.Render(left[:], right[:])
			for e.Poll(&m) {
				if m.Kind == cmd.Meter && m.Track == 0 {
					values[m.A] = math.Float32frombits(m.B)
					quantities++
				}
			}
		}
		return values, quantities
	}
	mono := build()
	if !mono.PushBatch([]cmd.Command{{Op: cmd.OpMeterRate, Track: 0xff, Arg0: 1}, {Op: cmd.OpNoteOn, Track: 0, Arg0: 69 | 127<<8}}) {
		t.Fatal("setup rejected")
	}
	monoValues, _ := collect(mono)
	if _, ok := monoValues[2]; ok {
		t.Fatalf("stereo quantities appeared without Arg1: %v", monoValues)
	}
	stereo := build()
	if !stereo.PushBatch([]cmd.Command{{Op: cmd.OpMeterRate, Track: 0xff, Arg0: 1, Arg1: 1}, {Op: cmd.OpNoteOn, Track: 0, Arg0: 69 | 127<<8}}) {
		t.Fatal("setup rejected")
	}
	v, _ := collect(stereo)
	if v[2] <= .5 || v[3] != 0 || v[4] <= 0 || v[5] != 0 {
		t.Fatalf("stereo meters ignore the hard-left pan: %v", v)
	}
	if v[0] != monoValues[0] || v[1] != monoValues[1] {
		t.Fatalf("mono quantities changed: %v / %v", v, monoValues)
	}
	if v[0] != v[2] {
		t.Fatalf("mono peak %g must be the larger channel %g", v[0], v[2])
	}
}

// Every track of the build, up to the native 64, gets all six meter messages. A
// ring too full to hold the whole emission sends the mono pair for every track
// instead, and the master message that closes the frame survives either way.
func TestStereoMeterEmissionFitsEveryTrackOrFallsBackWhole(t *testing.T) {
	build := func(tracks int) *Engine {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: tracks, MaxVoices: tracks, BPMMilli: 120_000}
		for i := range tracks {
			cfg.Track[i] = TrackConfig{Kind: VoiceGraph, Graph: sineProgram(375, 1), GainDB: 0, GainSet: true, Pan: -1}
		}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Push(cmd.Command{Op: cmd.OpMeterRate, Track: 0xff, Arg0: 1, Arg1: 1}) {
			t.Fatal("meter rate rejected")
		}
		return e
	}
	collect := func(e *Engine, tracks int) (perTrack map[uint8]int, peaks [2]float32, masterRMS bool) {
		var left, right [128]float32
		e.Render(left[:], right[:])
		perTrack = map[uint8]int{}
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault %d", m.A)
			}
			if m.Kind == cmd.Meter && int(m.Track) < tracks {
				perTrack[m.Track]++
				if int(m.Track) == tracks-1 && (m.A == 2 || m.A == 3) {
					peaks[m.A-2] = math.Float32frombits(m.B)
				}
			}
			masterRMS = masterRMS || m.Kind == cmd.Meter && m.Track == 0xff && m.A == 1
		}
		return
	}
	for _, tracks := range []int{16, MaxTracks} {
		perTrack, peaks, masterRMS := collect(build(tracks), tracks)
		for track := range uint8(tracks) {
			if perTrack[track] != 6 {
				t.Fatalf("%d tracks: track %d produced %d meter messages, want 6", tracks, track, perTrack[track])
			}
		}
		if !masterRMS {
			t.Fatalf("%d tracks: the master RMS message that closes a meter frame was dropped", tracks)
		}
		// The last track is hard left: separation survives at the full track count.
		if tracks == MaxTracks && (peaks[0] <= 0 || peaks[1] != 0) {
			t.Fatalf("%d tracks: last track peaks L=%g R=%g, want a hard-left signal", tracks, peaks[0], peaks[1])
		}
	}
	// A ring already holding more than the emission leaves room for falls back whole.
	full := build(MaxTracks)
	// Leave room for the mono emission but less than the stereo reservation, whatever
	// the build's track limit: six messages per track plus sixteen.
	for i := 0; i < len(full.messages)-(MaxTracks*6+15); i++ {
		full.emit(cmd.Message{Kind: cmd.Switched, A: uint16(i)})
	}
	perTrack, _, masterRMS := collect(full, MaxTracks)
	for track := range uint8(MaxTracks) {
		if perTrack[track] != 2 {
			t.Fatalf("crowded ring: track %d produced %d meter messages, want the mono pair", track, perTrack[track])
		}
	}
	if !masterRMS {
		t.Fatal("crowded ring: the master message was dropped")
	}
}

func TestPatternEditsValidChecksVoiceSlotAndPendingState(t *testing.T) {
	build := func(kind VoiceKind) *Engine {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 12, BPMMilli: 120_000, Seed: 4242}
		cfg.Track[0].Kind = kind
		cfg.Patterns = []PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
		if kind == VoiceDrums {
			cfg.Patterns[0].Drums = new([16][drum.LaneCount]seq.Pattern)
			for lane := range cfg.Patterns[0].Drums[0] {
				cfg.Patterns[0].Drums[0][lane] = cfg.Patterns[0].Slots[0]
			}
		}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	step := func(index uint16, note uint8) cmd.Command {
		return cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: index, Arg0: packedNoteValue(note)}
	}
	chord, err := cmd.ChordStepCommand(0, 0, 1, [4]uint8{60, 64, 67, 0}, 3)
	if err != nil {
		t.Fatal(err)
	}
	acid := build(VoiceAcid)
	if !acid.PatternEditsValid([]cmd.Command{step(1, 60)}) {
		t.Fatal("a plain step on an acid track was refused")
	}
	if acid.PatternEditsValid([]cmd.Command{step(1, 60), chord}) {
		t.Fatal("a chord on an acid track must be refused (fault 39)")
	}
	if !acid.PatternEditsValid([]cmd.Command{{Op: cmd.OpSetParam, Track: 0}}) {
		t.Fatal("a batch without pattern edits is valid")
	}
	kit := build(VoiceDrums)
	if !kit.PatternEditsValid([]cmd.Command{{Op: cmd.OpSetStep, Track: 0, Index: 2, Arg0: liveEditDrum(drum.SD)}}) {
		t.Fatal("a drum step on a drum track was refused")
	}
	if kit.PatternEditsValid([]cmd.Command{{Op: cmd.OpSetStep, Track: 0, Index: 2, Arg0: packedNoteValue(60)}}) {
		t.Fatal("a pitched step names no drum lane and must be refused")
	}
	// An edit still waiting in the pending queue counts as applied.
	pending := build(VoiceAcid)
	pending.cfgSlotTranspose(t)
	later := []cmd.Command{step(3, 110)}
	if !build(VoiceAcid).PatternEditsValid(later) {
		t.Fatal("note 110 is valid without a transpose")
	}
	if pending.PatternEditsValid(later) {
		t.Fatal("note 110 is out of range after the queued +24 transpose")
	}
	if allocs := testing.AllocsPerRun(100, func() { acid.PatternEditsValid([]cmd.Command{step(1, 60)}) }); allocs != 0 {
		t.Fatalf("dry run allocated %g", allocs)
	}
}

// cfgSlotTranspose queues a future +24 transpose of slot 0 without applying it.
func (e *Engine) cfgSlotTranspose(t *testing.T) {
	t.Helper()
	meta := cmd.Command{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: uint32(uint16(24)) << 16, Arg1: 0, Tick: 1_000_000}
	if !e.Push(meta) {
		t.Fatal("meta rejected")
	}
	var l, r [128]float32
	e.Render(l[:], r[:]) // drains the ring into the pending queue
}

// A Tick=0 edit waits for the next bar unless it carries PatternEditNow.
func TestPatternEditNowAppliesAtOnceWhilePlaying(t *testing.T) {
	soundsAt := func(pad uint32) bool {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000, Seed: 1}
		cfg.Track[0].Kind = VoiceAcid
		cfg.Patterns = []PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: 1}
		cfg.Scenes = []Scene{{Track: [MaxTracks]SceneBinding{{Mode: SceneSlot, Slot: 0}}}}
		cfg.Song = []SongEntry{{Scene: 0, Bars: 8}}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		var l, r [128]float32
		var m cmd.Message
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("rejected")
		}
		e.Render(l[:], r[:]) // playing before the edit arrives
		if !e.Push(cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 8, Arg0: packedNoteValue(52), Pad: pad}) {
			t.Fatal("rejected")
		}
		for range 700 { // 89,600 frames: step 8 sounds at frame 48,000; the next bar is at 96,000
			e.Render(l[:], r[:])
			for e.Poll(&m) {
				if m.Kind == cmd.NoteOn {
					return true
				}
			}
		}
		return false
	}
	if !soundsAt(cmd.PatternEditNow) {
		t.Fatal("the flagged edit did not apply at once")
	}
	if soundsAt(0) {
		t.Fatal("an unflagged Tick=0 edit must wait for the next bar")
	}
}

// Queued edits run in arrival order, an immediate edit included, so a queued
// transpose is judged before a later note whatever their ticks. The unread
// command ring counts too.
func TestPatternEditsValidFollowsExecutionOrderAcrossPendingAndRing(t *testing.T) {
	cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000, Seed: 1}
	cfg.Track[0].Kind = VoiceAcid
	cfg.Patterns = []PatternBank{{}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: 1}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.cfgSlotTranspose(t) // +24 waits in pending for a far tick
	note := func(n uint8, tick int64, pad uint32) []cmd.Command {
		return []cmd.Command{{Op: cmd.OpSetStep, Track: 0, Index: 3, Arg0: packedNoteValue(n), Tick: tick, Pad: pad}}
	}
	if e.PatternEditsValid(note(110, 2_000_000, 0)) {
		t.Fatal("a note stamped after the transpose lands out of range")
	}
	if e.PatternEditsValid(note(110, 0, cmd.PatternEditNow)) {
		t.Fatal("an immediate note runs first, then the queued transpose pushes it out of range")
	}
	if !e.PatternEditsValid(note(60, 0, cmd.PatternEditNow)) {
		t.Fatal("a low note stays valid under the transpose")
	}
	// An earlier batch still unread in the ring counts as well.
	if !e.Push(cmd.Command{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: uint32(uint16(24)) << 16, Tick: 1_500_000}) {
		t.Fatal("push rejected")
	}
	fresh, _ := New(cfg)
	if !fresh.Push(cmd.Command{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: uint32(uint16(24)) << 16, Tick: 1_500_000}) {
		t.Fatal("push rejected")
	}
	if fresh.PatternEditsValid(note(110, 2_000_000, 0)) {
		t.Fatal("a transpose waiting unread in the ring must be applied first")
	}
	if fresh.PatternEditsValid(note(110, 1_000_000, 0)) {
		t.Fatal("a note stamped before the transpose still meets it later, and the transpose would fault")
	}
	if !fresh.PatternEditsValid(note(60, 1_000_000, 0)) {
		t.Fatal("a low note is valid before and after the transpose")
	}
}

// Every JavaScript reader of the drained message block sizes its view from the
// ring capacity. A smaller view leaves messages past it unread, or rejected.
func TestMessageBlockReadersMatchTheMessageCapacity(t *testing.T) {
	want := fmt.Sprintf("MESSAGE_BYTES = %d * 16", MessageCapacity)
	for _, file := range []string{
		"host/web/processor.js", "host/wam2/processor.js", "cmd/cicada/browser_cpu_node.js",
		"tools/audio/kernel-cpu-compare.mjs", "tools/audio/kit-cpu.cjs",
	} {
		source, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Skipf("%s not available: %v", file, err)
		}
		if !strings.Contains(string(source), want) {
			t.Errorf("%s must declare %q to match engine.MessageCapacity", file, want)
		}
	}
}

// Pattern edits run in arrival order whatever their ticks, and a seek first
// lands the edits that arrived before it. So a batch is valid exactly when the
// queue replayed in arrival order is, whenever seeks are published.
func TestPatternEditsRunInArrivalOrderAcrossSeeks(t *testing.T) {
	build := func() *Engine {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: piano.MaxVoices, BPMMilli: 120_000, Seed: 1}
		cfg.Track[0].Kind = VoicePiano
		cfg.Patterns = []PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: 1}
		cfg.Patterns[0].Slots[0].Steps[3] = packedNoteValue(100) // piano range ends at 108
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		var l, r [128]float32
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("play rejected")
		}
		for e.transport.Tick() < 5_000 {
			e.Render(l[:], r[:])
		}
		return e
	}
	note := func(n uint8, tick int64) cmd.Command {
		return cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 3, Arg0: packedNoteValue(n), Tick: tick}
	}
	transpose := func(semitones int8, tick int64) cmd.Command {
		return cmd.Command{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: uint32(uint16(int16(semitones))) << 16, Tick: tick}
	}
	seek := func(bar, tick uint32) cmd.Command {
		return cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: bar, Arg1: tick}
	}
	push := func(e *Engine, cs ...cmd.Command) {
		t.Helper()
		if !e.PushBatch(cs) {
			t.Fatal("push rejected")
		}
	}
	run := func(e *Engine, ticks int64) {
		t.Helper()
		var l, r [128]float32
		var m cmd.Message
		for end := e.transport.Tick() + ticks; e.transport.Tick() < end; {
			e.Render(l[:], r[:])
			for e.Poll(&m) {
				if m.Kind == cmd.Fault {
					t.Fatalf("fault %d at tick %d", m.A, e.transport.Tick())
				}
			}
		}
	}
	state := func(e *Engine) (note uint32, semitones int8) {
		slot := &e.patterns[0].slots[0]
		return slot.Steps[3] & 0x7f, slot.Transpose
	}
	// A reported case: A (note 60, then +24) waits for tick 5,040; a backward seek and
	// B (note 105, transpose 0) wait for tick 240; before either runs, a seek to
	// 7,680. Run by tick B would land first. In arrival order B's note 105 meets the
	// +24 from A, so B is refused; a valid B is accepted and everything runs clean.
	e := build()
	push(e, note(60, 5_040), transpose(24, 5_040), seek(0, 200))
	if e.PatternEditsValid([]cmd.Command{note(105, 240), transpose(0, 240)}) {
		t.Fatal("B's note 105 runs under A's +24 and must be refused")
	}
	valid := []cmd.Command{note(70, 240), transpose(0, 240)}
	if !e.PatternEditsValid(valid) {
		t.Fatal("a valid B was refused")
	}
	push(e, valid...)
	push(e, seek(2, 0))
	run(e, 400)
	if n, tr := state(e); n != 70 || tr != 0 {
		t.Fatalf("edits ran out of arrival order: note %d, transpose %d; want 70 and 0", n, tr)
	}
	// Backward jump, an edit, a forward jump: the dependent batch is judged after them.
	e = build()
	push(e, seek(0, 100), note(60, 150), seek(2, 0))
	if !e.PatternEditsValid([]cmd.Command{transpose(24, 0)}) {
		t.Fatal("+24 over the note 60 that arrived first is valid")
	}
	push(e, transpose(24, 160))
	if e.PatternEditsValid([]cmd.Command{note(105, 0)}) {
		t.Fatal("note 105 meets the queued +24 and must be refused")
	}
	run(e, 400)
	if n, tr := state(e); n != 60 || tr != 24 {
		t.Fatalf("after the jumps: note %d, transpose %d; want 60 and 24", n, tr)
	}
	// A seek lands the edits ahead of it at once. The step boundary they were
	// stamped for (tick 5,040) is on a timeline that stops here.
	e = build()
	push(e, note(60, 5_040), seek(0, 200))
	var block, spare [128]float32
	e.Render(block[:], spare[:])
	if n, _ := state(e); n != 60 || e.transport.Tick() > 1_000 {
		t.Fatalf("the edit before the seek did not land at the seek: note %d, tick %d", n, e.transport.Tick())
	}
	// An edit that arrives later but is stamped earlier waits behind the first: the
	// +24 (tick 5,100) must not run ahead of the note 60 (tick 6,000).
	e = build()
	push(e, note(60, 6_000), transpose(24, 5_100))
	run(e, 500)
	if n, tr := state(e); n != 100 || tr != 0 {
		t.Fatalf("an edit ran ahead of one that arrived before it: note %d, transpose %d", n, tr)
	}
	run(e, 600)
	if n, tr := state(e); n != 60 || tr != 24 {
		t.Fatalf("both edits should have run by now: note %d, transpose %d", n, tr)
	}
}

// A seek applies the edits that arrived before it exactly once and never touches
// the ones that arrived after, including seeks started from OpPlay.
func TestSeekFlushesOnlyEditsAheadOfIt(t *testing.T) {
	build := func() *Engine {
		cfg := Config{SampleRate: 48_000, MaxBlock: 128, Tracks: 1, MaxVoices: 4, BPMMilli: 120_000, Seed: 1}
		cfg.Track[0].Kind = VoiceAcid
		cfg.Patterns = []PatternBank{{}}
		cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 55, Seed: 1}
		e, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.meterRate = 0
		return e
	}
	step := func(index uint16, note uint8, tick int64) cmd.Command {
		return cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: index, Arg0: packedNoteValue(note), Tick: tick}
	}
	noteAt := func(e *Engine, index int) uint32 { return e.patterns[0].slots[0].Steps[index] & 0x7f }
	var l, r [128]float32
	pushAll := func(e *Engine, cs ...cmd.Command) {
		t.Helper()
		if !e.PushBatch(cs) {
			t.Fatal("push rejected")
		}
	}
	for name, test := range map[string]struct {
		transport cmd.Command
		prepare   func(e *Engine)
	}{
		"explicit seek":      {transport: cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 0, Arg1: 100}},
		"seek to a bar line": {transport: cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1, Arg1: 0}},
	} {
		t.Run(name, func(t *testing.T) {
			e := build()
			pushAll(e, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			for e.transport.Tick() < 2_000 {
				e.Render(l[:], r[:])
			}
			// Ahead of the seek: lands at it. Behind it: stays until its tick.
			pushAll(e, step(1, 60, 2_400), test.transport, step(2, 70, 5_000))
			e.Render(l[:], r[:])
			if noteAt(e, 1) != 60 {
				t.Fatalf("the edit ahead of the seek did not land at it: %d", noteAt(e, 1))
			}
			if noteAt(e, 2) == 70 {
				t.Fatalf("the edit behind the seek was applied at the seek, tick %d", e.transport.Tick())
			}
			for e.transport.Tick() < 5_001 && e.transport.Tick() > 0 {
				e.Render(l[:], r[:])
			}
			if noteAt(e, 2) != 70 {
				t.Fatalf("the edit behind the seek never landed at its tick: %d", noteAt(e, 2))
			}
		})
	}
	// OpPlay can restart a finished placement schedule: an internal seek to tick 0.
	cfg := mixedOwnerConfig()
	cfg.LoopSong = false
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.meterRate = 0
	pushAll(e, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	for blocks := 0; blocks < 400; blocks++ { // the schedule is 960 ticks and then stops
		e.Render(l[:], r[:])
	}
	if e.transport.Playing() {
		t.Fatal("the placement schedule should have ended")
	}
	ahead := cmd.Command{Op: cmd.OpSetStep, Track: 1, Index: 3, Arg0: packedNoteValue(61), Tick: 100}
	behind := cmd.Command{Op: cmd.OpSetStep, Track: 1, Index: 4, Arg0: packedNoteValue(62), Tick: 5_000}
	pushAll(e, ahead, cmd.Command{Op: cmd.OpPlay, Track: 0xff}, behind)
	e.Render(l[:], r[:])
	slot := &e.patterns[1].slots[0]
	if slot.Steps[3]&0x7f != 61 {
		t.Fatalf("the edit ahead of the schedule restart did not land: %d", slot.Steps[3]&0x7f)
	}
	if slot.Steps[4]&0x7f == 62 {
		t.Fatal("the edit behind the schedule restart was applied by it")
	}
}
