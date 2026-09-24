// Package engine owns the bounded, allocation-free audio-thread state.
package engine

import (
	"math"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
)

type Error string

func (e Error) Error() string { return string(e) }

type VoiceKind uint8

const (
	VoiceOff VoiceKind = iota
	VoiceAcid
	VoiceDrums
	VoiceGraph
)

type TrackConfig struct {
	Kind    VoiceKind
	Acid    acid.Params
	Drums   [drum.LaneCount]drum.Params
	Graph   graph.Program
	GainDB  float64
	GainSet bool
	Pan     float64
	Mute    bool
}

// PatternBank is immutable project data copied into the engine by New.
// Drum slots hold an independent pattern for each synthesized lane.
type PatternBank struct {
	Slots [16]seq.Pattern
	Drums *[16][drum.LaneCount]seq.Pattern
}

type Config struct {
	SampleRate int
	MaxBlock   int
	Tracks     int
	MaxVoices  int
	BPMMilli   int64
	Seed       uint32
	Track      [16]TrackConfig
	Patterns   []PatternBank
	Scenes     []Scene
	Song       []SongEntry
	LoopSong   bool
}

type voiceSlot struct {
	kind  VoiceKind
	acid  *acid.Voice
	drums *drum.Kit
	graph *graph.Voice
	mix   mix.Track
}

// Engine has fixed command and message rings. The host owns cross-thread
// communication and calls Push only on the render thread.
type Engine struct {
	sampleRate, maxBlock, tracks int
	bpmMilli                     int64
	voices                       [16]voiceSlot
	transport                    seq.Transport
	limiter                      *mix.Limiter
	commands                     [512]cmd.Command
	commandRead, commandWrite    uint16
	messages                     [256]cmd.Message
	messageRead, messageWrite    uint16
	overflowMessages             [2]cmd.Message
	overflowRead, overflowLen    uint8
	pending                      [512]cmd.Command
	pendingLen                   int
	layerMask                    uint32
	meterRate, meterBlock        uint32
	faulted                      bool
	patterns                     [16]patternTrack
	eventScratch                 [128]seq.Event
	renderFrame, renderFrames    int
	scenes                       []Scene
	song                         []SongEntry
	loopSong, songMode           bool
	songIndex                    int
	songEndTick                  int64
}

func New(cfg Config) (*Engine, error) {
	if cfg.MaxBlock < 1 || cfg.MaxBlock > 4096 || cfg.Tracks < 1 || cfg.Tracks > 16 || cfg.MaxVoices < 1 || cfg.MaxVoices > 32 {
		return nil, Error("engine configuration is out of range")
	}
	if cfg.BPMMilli == 0 {
		cfg.BPMMilli = 120_000
	}
	transport, err := seq.NewTransport(cfg.SampleRate, cfg.BPMMilli)
	if err != nil {
		return nil, err
	}
	limiter, err := mix.NewLimiter(cfg.SampleRate)
	if err != nil {
		return nil, err
	}
	e := &Engine{sampleRate: cfg.SampleRate, maxBlock: cfg.MaxBlock, tracks: cfg.Tracks, bpmMilli: cfg.BPMMilli, transport: transport, limiter: limiter, layerMask: (1 << cfg.Tracks) - 1, meterRate: 4}
	voices := 0
	for i := 0; i < cfg.Tracks; i++ {
		e.patterns[i].active = -1
		for slot := range e.patterns[i].slots {
			e.patterns[i].slots[slot] = seq.Pattern{Len: 16, GatePercent: 55, Seed: cfg.Seed}
		}
		spec := cfg.Track[i]
		gain := spec.GainDB
		if !spec.GainSet {
			gain = -6
		}
		if math.IsNaN(gain) || math.IsInf(gain, 0) || gain < -60 || gain > 6 || math.IsNaN(spec.Pan) || math.IsInf(spec.Pan, 0) || spec.Pan < -1 || spec.Pan > 1 {
			return nil, Error("track mixer parameter is out of range")
		}
		v := &e.voices[i]
		v.kind = spec.Kind
		v.mix = mix.NewTrack(gain, spec.Pan, spec.Mute)
		switch spec.Kind {
		case VoiceOff:
		case VoiceAcid:
			voices++
			v.acid, err = acid.New(cfg.SampleRate)
			if err == nil && spec.Acid != (acid.Params{}) {
				err = v.acid.SetParams(spec.Acid)
			}
		case VoiceDrums:
			voices += int(drum.LaneCount)
			e.patterns[i].drumSlots = new([16][drum.LaneCount]seq.Pattern)
			for slot := range e.patterns[i].drumSlots {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					e.patterns[i].drumSlots[slot][lane] = e.patterns[i].slots[slot]
				}
			}
			v.drums, err = drum.New(cfg.SampleRate, cfg.Seed)
			if err == nil {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					if spec.Drums[lane] != (drum.Params{}) {
						err = v.drums.SetParams(lane, spec.Drums[lane])
						if err != nil {
							break
						}
					}
				}
			}
		case VoiceGraph:
			voices++
			v.graph, err = graph.NewVoice(spec.Graph, cfg.SampleRate)
		default:
			return nil, Error("unknown voice kind")
		}
		if err != nil {
			return nil, err
		}
	}
	if voices > cfg.MaxVoices {
		return nil, Error("engine exceeds maximum voices")
	}
	if len(cfg.Patterns) != 0 && len(cfg.Patterns) != cfg.Tracks {
		return nil, Error("pattern bank count must match tracks")
	}
	for track, bank := range cfg.Patterns {
		isDrum := e.voices[track].kind == VoiceDrums
		if isDrum != (bank.Drums != nil) {
			return nil, Error("pattern bank kind differs from track")
		}
		for slot, pattern := range bank.Slots {
			if pattern.Len == 0 {
				if isDrum {
					for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
						if bank.Drums[slot][lane].Len != 0 {
							return nil, Error("unused drum slot contains lane data")
						}
					}
				}
				continue
			}
			if pattern.Validate() != nil {
				return nil, Error("invalid preloaded pattern")
			}
			e.patterns[track].slots[slot] = pattern
			if !isDrum {
				continue
			}
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				lanePattern := bank.Drums[slot][lane]
				if lanePattern.Len != pattern.Len || lanePattern.SwingPermille != pattern.SwingPermille || lanePattern.GatePercent != pattern.GatePercent || lanePattern.Seed != pattern.Seed || lanePattern.Transpose != 0 || lanePattern.Validate() != nil {
					return nil, Error("invalid preloaded drum lane")
				}
				for step := uint8(0); step < lanePattern.Len; step++ {
					decoded, _ := seq.UnpackStep(lanePattern.Steps[step])
					if decoded.Gate && (decoded.Tie || decoded.Note != uint8(lane)) {
						return nil, Error("drum lane step has wrong routing")
					}
				}
				e.patterns[track].drumSlots[slot][lane] = lanePattern
			}
		}
	}
	if len(cfg.Scenes) > 1<<16 || len(cfg.Song) > 1<<16 {
		return nil, Error("arrangement exceeds the wire index range")
	}
	for _, scene := range cfg.Scenes {
		for track, binding := range scene.Track {
			if binding.Mode > SceneSlot || track >= cfg.Tracks && binding.Mode != SceneKeep || binding.Mode == SceneSlot && binding.Slot >= 16 {
				return nil, Error("scene binding is out of range")
			}
		}
	}
	for _, entry := range cfg.Song {
		if int(entry.Scene) >= len(cfg.Scenes) || entry.Bars < 1 || entry.Bars > 999 {
			return nil, Error("song entry is out of range")
		}
	}
	e.scenes = append([]Scene(nil), cfg.Scenes...)
	e.song = append([]SongEntry(nil), cfg.Song...)
	e.loopSong = cfg.LoopSong
	return e, nil
}

func (e *Engine) Push(c cmd.Command) bool {
	if c.Validate(uint8(e.tracks)) != nil || e.commandWrite-e.commandRead >= uint16(len(e.commands)) {
		return false
	}
	e.commands[e.commandWrite%uint16(len(e.commands))] = c
	e.commandWrite++
	return true
}

// PushBatch accepts all commands or none, including on queue exhaustion.
func (e *Engine) PushBatch(commands []cmd.Command) bool {
	if len(commands) > len(e.commands)-int(e.commandWrite-e.commandRead) {
		return false
	}
	for _, command := range commands {
		if command.Validate(uint8(e.tracks)) != nil {
			return false
		}
	}
	for _, command := range commands {
		e.commands[e.commandWrite%uint16(len(e.commands))] = command
		e.commandWrite++
	}
	return true
}

// InjectFault lets a host report malformed wire input without dropping it.
func (e *Engine) InjectFault(code uint16) { e.fault(code) }

func (e *Engine) Poll(m *cmd.Message) bool {
	if m == nil {
		return false
	}
	if e.messageRead != e.messageWrite {
		*m = e.messages[e.messageRead%uint16(len(e.messages))]
		e.messageRead++
		return true
	}
	if e.overflowRead < e.overflowLen {
		*m = e.overflowMessages[e.overflowRead]
		e.overflowRead++
		return true
	}
	return false
}

func (e *Engine) Reset() {
	for i := 0; i < e.tracks; i++ {
		e.resetVoice(i)
		e.patterns[i].active = -1
		e.patterns[i].eventCount, e.patterns[i].eventIndex = 0, 0
		e.patterns[i].heldValid = false
		e.patterns[i].playingNote = 0
	}
	e.limiter.Reset()
	e.transport, _ = seq.NewTransport(e.sampleRate, e.bpmMilli)
	e.commandRead, e.commandWrite, e.messageRead, e.messageWrite = 0, 0, 0, 0
	e.overflowRead, e.overflowLen = 0, 0
	e.pendingLen, e.meterBlock = 0, 0
	e.renderFrame, e.renderFrames = 0, 0
	e.layerMask = (1 << e.tracks) - 1
	e.songMode, e.songIndex, e.songEndTick = false, 0, 0
	e.faulted = false
}

func (e *Engine) Render(outL, outR []float32) {
	if len(outL) != len(outR) || len(outL) < 1 || len(outL) > e.maxBlock {
		clear(outL)
		clear(outR)
		e.fault(1)
		return
	}
	clear(outL)
	clear(outR)
	if e.faulted {
		return
	}
	e.drainCommands()
	if e.faulted {
		return
	}
	e.renderFrames = len(outL)
	e.renderFrame = 0
	e.scheduleAll()
	if e.faulted {
		return
	}
	scheduledTempo := e.transport.BPMMilli()
	var blockPeak float32
	for frame := range outL {
		e.renderFrame = frame
		if e.transport.BPMMilli() != scheduledTempo {
			e.scheduleAll()
			scheduledTempo = e.transport.BPMMilli()
		}
		e.processPatternEvents(seq.NoteOff)
		e.advanceSong()
		if e.faulted {
			clear(outL[frame:])
			clear(outR[frame:])
			return
		}
		e.applyPending()
		if e.faulted {
			clear(outL[frame:])
			clear(outR[frame:])
			return
		}
		e.processPatternEvents(seq.NoteOn)
		if e.faulted {
			clear(outL[frame:])
			clear(outR[frame:])
			return
		}
		var dry mix.Dry
		for track := 0; track < e.tracks; track++ {
			if e.layerMask&(1<<track) == 0 {
				continue
			}
			v := &e.voices[track]
			switch v.kind {
			case VoiceAcid:
				sample := v.acid.Next()
				if v.acid.Fault() {
					e.fault(2)
					clear(outL[frame:])
					clear(outR[frame:])
					return
				}
				dry.Add(sample, sample, v.mix)
			case VoiceDrums:
				left, right := v.drums.NextStereo()
				if v.drums.Fault() {
					e.fault(3)
					clear(outL[frame:])
					clear(outR[frame:])
					return
				}
				dry.Add(left, right, v.mix)
			case VoiceGraph:
				sample := v.graph.Next()
				dry.Add(sample, sample, v.mix)
			}
		}
		left, right := dry.Music()
		outL[frame], outR[frame], _ = e.limiter.Process(left, right)
		if e.limiter.Fault() {
			e.fault(4)
			clear(outL[frame:])
			clear(outR[frame:])
			return
		}
		blockPeak = max(blockPeak, float32(math.Max(math.Abs(float64(outL[frame])), math.Abs(float64(outR[frame])))))
		e.transport.Advance(1)
	}
	e.renderFrames = 0
	e.meterBlock++
	if e.meterRate != 0 && e.meterBlock%e.meterRate == 0 {
		e.emit(cmd.Message{Kind: cmd.Meter, Track: 0xff, B: math.Float32bits(blockPeak), Tick: e.transport.Tick()})
	}
}

func (e *Engine) drainCommands() {
	for e.commandRead != e.commandWrite {
		c := e.commands[e.commandRead%uint16(len(e.commands))]
		e.commandRead++
		if c.Op == cmd.OpSetLayerMask && c.Tick == 0 {
			c.Tick = (e.transport.Tick()/seq.TicksPerBar + 1) * seq.TicksPerBar
		}
		if e.transport.Playing() && c.Tick == 0 && (c.Op == cmd.OpSetStep || c.Op == cmd.OpSetPatternLen || c.Op == cmd.OpSetPatternMeta) {
			c.Tick = (e.transport.Tick()/seq.TicksPerBar + 1) * seq.TicksPerBar
		}
		if e.pendingLen == len(e.pending) {
			e.fault(5)
			return
		}
		if c.Tick > 0 && c.Tick < e.transport.Tick() {
			e.emit(cmd.Message{Kind: cmd.Late, Track: c.Track, A: uint16(c.Op), Tick: c.Tick})
		}
		e.pending[e.pendingLen] = c
		e.pendingLen++
		if e.faulted {
			return
		}
	}
	e.applyPending()
}

func (e *Engine) applyPending() {
	for {
		best, priority := -1, 5
		for i := 0; i < e.pendingLen; i++ {
			if e.pending[i].Tick <= e.transport.Tick() && commandPriority(e.pending[i].Op) < priority {
				best, priority = i, commandPriority(e.pending[i].Op)
			}
		}
		if best < 0 {
			return
		}
		c := e.pending[best]
		copy(e.pending[best:], e.pending[best+1:e.pendingLen])
		e.pendingLen--
		e.apply(c)
		if e.faulted {
			return
		}
	}
}

func commandPriority(op cmd.Op) int {
	switch op {
	case cmd.OpNoteOff, cmd.OpStop, cmd.OpSeek:
		return 0
	case cmd.OpSelectPattern, cmd.OpLaunchScene, cmd.OpSetChain:
		return 1
	case cmd.OpNoteOn, cmd.OpPlay:
		return 3
	default:
		return 2
	}
}

func (e *Engine) apply(c cmd.Command) {
	switch c.Op {
	case cmd.OpPlay:
		e.transport.Play()
		if len(e.song) > 0 && !e.songMode {
			e.startSong()
		}
		if e.renderFrames > 0 {
			e.scheduleAll()
		}
		e.emit(cmd.Message{Kind: cmd.Playhead, Track: 0xff, Tick: e.transport.Tick()})
	case cmd.OpStop:
		e.transport.Stop()
		for i := 0; i < e.tracks; i++ {
			e.noteOff(i, 0xffff)
			e.patterns[i].playingNote = 0
			e.patterns[i].heldValid = false
		}
	case cmd.OpSeek:
		if e.transport.SeekTick(int64(c.Arg0)*seq.TicksPerBar+int64(c.Arg1)) != nil {
			e.fault(6)
			return
		}
		for i := 0; i < e.tracks; i++ {
			e.resetVoice(i)
		}
		e.limiter.Reset()
		for i := 0; i < e.tracks; i++ {
			e.patterns[i].playingNote = 0
			e.patterns[i].heldValid = false
		}
		if e.songMode {
			e.startSong()
		}
		if e.renderFrames > 0 {
			e.scheduleAll()
		}
	case cmd.OpSetTempo:
		if e.transport.QueueTempo(int64(c.Arg0)) != nil {
			e.fault(7)
		}
	case cmd.OpNoteOn:
		track := int(c.Track)
		note, velocity := uint8(c.Arg0), uint8(c.Arg0>>8)
		accent, slide := c.Arg0&(1<<16) != 0, c.Arg0&(1<<17) != 0
		v := &e.voices[track]
		e.patterns[track].playingNote = 0
		e.patterns[track].heldValid = false
		switch v.kind {
		case VoiceAcid:
			v.acid.NoteOn(note, accent, slide, velocity)
		case VoiceGraph:
			v.graph.NoteOn(note, velocity, slide)
		case VoiceDrums:
			if c.Index >= uint16(drum.LaneCount) {
				e.fault(8)
				return
			}
			v.drums.Hit(drum.Lane(c.Index), velocity, accent)
		default:
			e.fault(9)
			return
		}
		e.emit(cmd.Message{Kind: cmd.NoteOn, Track: c.Track, A: uint16(note), Tick: e.transport.Tick()})
	case cmd.OpNoteOff:
		if e.voices[c.Track].kind == VoiceDrums && c.Index >= uint16(drum.LaneCount) {
			e.fault(8)
			return
		}
		e.noteOff(int(c.Track), c.Index)
		e.patterns[c.Track].playingNote = 0
		e.patterns[c.Track].heldValid = false
		e.emit(cmd.Message{Kind: cmd.NoteOff, Track: c.Track, Tick: e.transport.Tick()})
	case cmd.OpSetStep, cmd.OpSetPatternLen, cmd.OpSetPatternMeta, cmd.OpSelectPattern:
		e.applyPatternCommand(c)
	case cmd.OpLaunchScene:
		e.applySceneCommand(c)
	case cmd.OpSetLayerMask:
		e.layerMask = c.Arg0
	case cmd.OpMeterRate:
		e.meterRate = c.Arg0
	default:
		e.fault(10) // No accepted command may be silently discarded.
	}
}

func (e *Engine) noteOff(track int, lane uint16) {
	v := &e.voices[track]
	switch v.kind {
	case VoiceAcid:
		v.acid.NoteOff()
	case VoiceGraph:
		v.graph.NoteOff()
	case VoiceDrums:
		if lane < uint16(drum.LaneCount) {
			v.drums.NoteOff(drum.Lane(lane))
		} else {
			for n := drum.Lane(0); n < drum.LaneCount; n++ {
				v.drums.NoteOff(n)
			}
		}
	}
}

func (e *Engine) resetVoice(track int) {
	v := &e.voices[track]
	switch v.kind {
	case VoiceAcid:
		v.acid.Reset()
	case VoiceGraph:
		v.graph.Reset()
	case VoiceDrums:
		v.drums.Reset()
	}
}

func (e *Engine) emit(message cmd.Message) {
	if e.messageWrite-e.messageRead >= uint16(len(e.messages)) {
		if message.Kind == cmd.Meter {
			return
		}
		for i := e.messageRead; i != e.messageWrite; i++ {
			if e.messages[i%uint16(len(e.messages))].Kind != cmd.Meter {
				continue
			}
			for j := i; j != e.messageWrite-1; j++ {
				e.messages[j%uint16(len(e.messages))] = e.messages[(j+1)%uint16(len(e.messages))]
			}
			e.messageWrite--
			break
		}
		if e.messageWrite-e.messageRead >= uint16(len(e.messages)) {
			if e.overflowLen == 0 {
				e.overflowMessages[0] = message
				e.overflowLen = 1
				if message.Kind != cmd.Fault {
					e.overflowMessages[1] = cmd.Message{Kind: cmd.Fault, Track: 0xff, A: 11, Tick: e.transport.Tick()}
					e.overflowLen = 2
				}
			}
			e.faulted = true
			e.transport.Stop()
			for track := 0; track < e.tracks; track++ {
				e.resetVoice(track)
			}
			e.limiter.Reset()
			return
		}
	}
	e.messages[e.messageWrite%uint16(len(e.messages))] = message
	e.messageWrite++
}

func (e *Engine) fault(code uint16) {
	if e.faulted {
		return
	}
	e.faulted = true
	e.renderFrames = 0
	e.transport.Stop()
	for i := 0; i < e.tracks; i++ {
		e.resetVoice(i)
	}
	e.limiter.Reset()
	e.emit(cmd.Message{Kind: cmd.Fault, Track: 0xff, A: code, Tick: e.transport.Tick()})
}
