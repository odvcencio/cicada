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

// Queued edits run earliest tick first, and an edit that applies at once runs
// ahead of one still waiting for its tick. The unread command ring counts too.
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

// The AudioWorklet carries one drain of the status ring in a fixed buffer. The
// buffer size must follow MessageCapacity, or the client rejects a full drain.
func TestWorkletMessageBufferMatchesTheMessageCapacity(t *testing.T) {
	source, err := os.ReadFile("../../host/web/processor.js")
	if err != nil {
		t.Skipf("worklet source not available: %v", err)
	}
	want := fmt.Sprintf("MESSAGE_BYTES = %d * 16", MessageCapacity)
	if !strings.Contains(string(source), want) {
		t.Fatalf("host/web/processor.js must declare %q to match engine.MessageCapacity", want)
	}
}

// A queued seek moves the tick every later comparison is made against, so the
// order the engine runs two edits in can differ from their order before the seek.
func TestPatternEditsValidFollowsQueuedSeeks(t *testing.T) {
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
	lower := func(tick int64) cmd.Command {
		return cmd.Command{Op: cmd.OpSetStep, Track: 0, Index: 3, Arg0: packedNoteValue(60), Tick: tick}
	}
	up := func(tick int64) cmd.Command {
		return cmd.Command{Op: cmd.OpSetPatternMeta, Track: 0, Arg0: uint32(uint16(24)) << 16, Tick: tick}
	}
	back := cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 0, Arg1: 100}
	// Both edits are already due, so they run in arrival order: lower, then up.
	plain := build()
	if !plain.Push(lower(4_800)) || !plain.PatternEditsValid([]cmd.Command{up(4_700)}) {
		t.Fatal("two due edits run in arrival order: the note drops before the transpose")
	}
	// With a seek queued between them, neither is due after it. They then run by
	// tick, the transpose (4,700) first, and it lifts note 100 out of range.
	seeking := build()
	if !seeking.Push(lower(4_800)) || !seeking.Push(back) {
		t.Fatal("push rejected")
	}
	if seeking.PatternEditsValid([]cmd.Command{up(4_700)}) {
		t.Fatal("after the seek the transpose runs first and faults the piano")
	}
	// The engine does fault that way: run it.
	faultOf := func(e *Engine) uint16 {
		var l, r [128]float32
		var m cmd.Message
		for blocks := 0; blocks < 1_200; blocks++ {
			e.Render(l[:], r[:])
			for e.Poll(&m) {
				if m.Kind == cmd.Fault {
					return m.A
				}
			}
		}
		return 0
	}
	if !seeking.Push(up(4_700)) {
		t.Fatal("push rejected")
	}
	if code := faultOf(seeking); code != FaultPatternEdit {
		t.Fatalf("the engine ran the edits in the predicted order and faulted with %d, want %d", code, FaultPatternEdit)
	}
	// Stamped the other way round the order is the same before and after the seek.
	ordered := build()
	if !ordered.Push(lower(4_700)) || !ordered.Push(back) || !ordered.PatternEditsValid([]cmd.Command{up(4_800)}) {
		t.Fatal("the note drops first by tick, so the transpose is valid")
	}
	if !ordered.Push(up(4_800)) || faultOf(ordered) != 0 {
		t.Fatal("the valid order must run without a fault")
	}
	// The seek can also be in the pending queue, not the ring.
	var l, r [128]float32
	queued := build()
	if !queued.Push(lower(4_800)) || !queued.Push(back) {
		t.Fatal("push rejected")
	}
	queued.Render(l[:], r[:]) // runs the seek and leaves the note edit pending
	if queued.transport.Tick() > 1_000 {
		t.Fatalf("the seek did not run: tick %d", queued.transport.Tick())
	}
	if queued.PatternEditsValid([]cmd.Command{up(4_700)}) {
		t.Fatal("pending edit and the earlier tick: the transpose still runs first")
	}
}
