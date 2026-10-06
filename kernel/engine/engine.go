// Package engine owns the bounded, allocation-free audio-thread state.
package engine

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/piano"
)

type Error string

func (e Error) Error() string { return string(e) }

type VoiceKind uint8

const (
	VoiceOff VoiceKind = iota
	VoiceAcid
	VoiceDrums
	VoiceGraph
	VoicePiano
)

type KitLaneKind uint8

const (
	KitLaneOff KitLaneKind = iota
	KitLaneBuiltin
	KitLaneGraph
)

// KitLaneBinding selects one voice for a named drum source lane. Zero means
// the lane is omitted and silent. Programs are compiled before Engine.New.
type KitLaneBinding struct {
	Kind    KitLaneKind
	Recipe  drum.Lane
	Program graph.Program
}

type TrackConfig struct {
	Kind         VoiceKind
	Acid         acid.Params
	Drums        [drum.LaneCount]drum.Params
	Kit          *[drum.LaneCount]KitLaneBinding
	Graph        graph.Program
	Polyphony    uint8   `json:",omitzero"` // zero: legacy mono; four: experimental graph pool
	PianoSustain float32 `json:",omitempty"`
	GainDB       float64
	GainSet      bool
	Pan          float64
	Mute         bool
	Solo         bool
	InsertDrive  *fx.DriveParams
	SendA        float64
	SendB        float64
	SendPre      bool
	SendAPre     bool
	SendBPre     bool
	BusSFX       bool
}

// SFXSidechain selects the post-fader SFX bus as the music compressor detector.
const SFXSidechain = 17

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
	DelayA     *fx.DelayParams
	ReverbB    *fx.ReverbParams
	CompMusic  *fx.CompParams
	// MasterGainDB is a static gain applied immediately before the master
	// limiter. Zero leaves the existing master path bit-identical.
	MasterGainDB float64
	// MasterProcessor is prepared by the host before New. It is single-owner
	// callback state, placed after master gain and before the existing limiter.
	MasterProcessor StereoProcessor `json:"-"`
	MusicBusMute    bool
	MusicBusSolo    bool
	SFXBusMute      bool
	SFXBusSolo      bool
	MasterMute      bool
	// MasterSolo is retained as project state; the final output has no peer bus.
	MasterSolo bool
	// CompSidechainTrack is zero for self-detection, a one-based track index,
	// or SFXSidechain for the post-fader SFX bus.
	CompSidechainTrack int
}

type voiceSlot struct {
	kind                           VoiceKind
	acid                           *acid.Voice
	drums                          *drum.Kit
	graph                          *graph.Voice
	piano                          *piano.Instrument
	pianoSustain                   float32
	poly                           *graph.Pool
	mix                            mix.Track
	targetMix                      mix.Track
	mixSmooth                      float32
	muteGain, muteTarget, muteStep float32
	gainDB, pan                    float32
	insert                         *fx.Drive
	align                          *mix.Delay
	sendA                          float32
	sendB                          float32
	targetSendA                    float32
	targetSendB                    float32
	sendSmooth                     float32
	sendAPre, sendBPre             bool
	muted, sourceOff               bool
	soloed                         bool
	busSFX                         bool
	acidTarget                     acid.Params
	drumTargets                    [drum.LaneCount]drum.Params
}

type meterAccum struct {
	peak  float32
	power float64
}

// Engine has fixed command and message rings. The host owns cross-thread
// communication and calls Push only on the render thread.
type Engine struct {
	sampleRate, maxBlock, tracks int
	paramAlpha                   [kernel.ParamCount]float32
	macroCurrent                 [16]float32
	macroStart                   [16]float32
	macroTarget                  [16]float32
	macroRampFrames              [16]uint32
	macroRampElapsed             [16]uint32
	macroLayerEnabled            [16]bool
	macroLayerThresholds         [16][4]uint8
	macroLayerThresholdCount     [16]uint8
	macroLayerInitialized        [16]bool
	macroLayerMasks              [16][4]uint32
	macroLayerRelease            [16]uint8
	macroLayerLevel              [16]uint8
	macroLayerQuiet              [16]uint8
	director                     [16]directorTrack
	sceneFadeFrames              uint32
	phraseBars                   uint32
	liveEvents                   bool // set by any live-control op; keeps Bar and MacroReached off for hosts that never drain messages
	lastBarTick                  int64
	bpmMilli                     int64
	voices                       [16]voiceSlot
	transport                    seq.Transport
	limiter                      *mix.Limiter
	delayA                       *fx.Delay
	reverbB                      *fx.Reverb
	compMusic                    *fx.Compressor
	compSidechainTrack           int
	masterGain                   float32
	masterProcessor              StereoProcessor
	musicBusMute, musicBusSolo   bool
	sfxBusMute, sfxBusSolo       bool
	masterMute, masterSolo       bool
	commands                     [512]cmd.Command
	commandRead, commandWrite    uint16
	messages                     [256]cmd.Message
	messageRead, messageWrite    uint16
	overflowMessages             [2]cmd.Message
	overflowRead, overflowLen    uint8
	pending                      [512]cmd.Command
	pendingLen                   int
	layerMask                    uint32 // effective: layerAuthored gated by the macro layer tables
	layerAuthored                uint32 // set by scenes, OpSetLayerMask and source-off; never by macros
	meterRate, meterBlock        uint32
	meterFrames                  uint32
	trackMeter                   [16]meterAccum
	returnAMeter, returnBMeter   meterAccum
	musicMeter, sfxMeter         meterAccum
	preMasterMeter, masterMeter  meterAccum
	compGR, limiterGR            float32
	hasSFX                       bool
	soloCount                    int
	playheadFrames               int
	faulted                      bool
	patterns                     [16]patternTrack
	eventScratch                 [128]seq.Event
	renderFrame, renderFrames    int
	sceneDefaults                *sceneParameterState
	scenes                       []Scene
	currentScene                 int
	sceneSequence                uint64
	song                         []SongEntry
	loopSong, songMode           bool
	songIndex                    int
	songEndTick                  int64
	manualSceneTick              int64
	manualPatternTick            [16]int64
}

// TrackCount reports the immutable track count established by New.
func (e *Engine) TrackCount() int { return e.tracks }

// TrackVoiceKind reports the source selected at construction.
func (e *Engine) TrackVoiceKind(track int) VoiceKind {
	if track < 0 || track >= e.tracks {
		return VoiceOff
	}
	return e.voices[track].kind
}

// TrackPolyphonic reports an immutable voice-mode capability to native hosts.
func (e *Engine) TrackPolyphonic(track int) bool {
	return track >= 0 && track < e.tracks && e.voices[track].poly != nil
}

// MacroValue reports the current and target values for a macro ID. Invalid IDs return zero values.
func (e *Engine) MacroValue(id int) (current, target float32) {
	if id < 0 || id >= len(e.macroCurrent) {
		return 0, 0
	}
	return e.macroCurrent[id], e.macroTarget[id]
}

// CurrentScene reports the last launched scene and its launch sequence.
// The index is -1 before a scene launches.
// Call it only from the goroutine that owns Render.
func (e *Engine) CurrentScene() (int, uint64) { return e.currentScene, e.sceneSequence }

func New(cfg Config) (*Engine, error) {
	return NewFromConfig(&cfg)
}

//go:noinline
func NewFromConfig(cfg *Config) (*Engine, error) {
	if cfg == nil {
		return nil, Error("engine configuration is out of range")
	}
	if cfg.MaxBlock < 1 || cfg.MaxBlock > 4096 || cfg.Tracks < 1 || cfg.Tracks > 16 || cfg.MaxVoices < 1 || cfg.MaxVoices > 32 {
		return nil, Error("engine configuration is out of range")
	}
	if math.IsNaN(cfg.MasterGainDB) || math.IsInf(cfg.MasterGainDB, 0) || cfg.MasterGainDB < -120 || cfg.MasterGainDB > 24 {
		return nil, Error("master gain is out of range")
	}
	bpmMilli := cfg.BPMMilli
	if bpmMilli == 0 {
		bpmMilli = 120_000
	}
	transport, err := seq.NewTransport(cfg.SampleRate, bpmMilli)
	if err != nil {
		return nil, err
	}
	limiter, err := mix.NewLimiter(cfg.SampleRate)
	if err != nil {
		return nil, err
	}
	masterGain := float32(1)
	if cfg.MasterGainDB != 0 {
		masterGain = float32(math.Pow(10, cfg.MasterGainDB/20))
	}
	e := &Engine{sampleRate: cfg.SampleRate, maxBlock: cfg.MaxBlock, tracks: cfg.Tracks, bpmMilli: bpmMilli, transport: transport, limiter: limiter, masterGain: masterGain,
		musicBusMute: cfg.MusicBusMute, musicBusSolo: cfg.MusicBusSolo, sfxBusMute: cfg.SFXBusMute, sfxBusSolo: cfg.SFXBusSolo, masterMute: cfg.MasterMute, masterSolo: cfg.MasterSolo,
		layerMask: (1 << cfg.Tracks) - 1, layerAuthored: (1 << cfg.Tracks) - 1, meterRate: 4, playheadFrames: cfg.SampleRate / 60, manualSceneTick: -1, currentScene: -1, lastBarTick: -1}
	for id := 0; id < len(kernel.Params); id++ {
		e.paramAlpha[id] = smoothingAlpha(kernel.ParamID(id), cfg.SampleRate)
	}
	for i := range e.manualPatternTick {
		e.manualPatternTick[i] = -1
	}
	if err := e.initEffects(cfg, bpmMilli); err != nil {
		return nil, err
	}
	voices, err := e.initTrackVoices(cfg)
	if err != nil {
		return nil, err
	}
	e.updateMuteTargets()
	if voices > cfg.MaxVoices {
		return nil, Error("engine exceeds maximum voices")
	}
	if err := e.loadPatterns(cfg); err != nil {
		return nil, err
	}
	if err := e.loadArrangement(cfg, bpmMilli); err != nil {
		return nil, err
	}
	e.captureSceneDefaults()
	return e, nil
}

//go:noinline
func (e *Engine) initEffects(cfg *Config, bpmMilli int64) error {
	var err error
	if cfg.DelayA != nil {
		e.delayA, err = fx.NewDelay(cfg.SampleRate, bpmMilli)
		if err == nil {
			err = e.delayA.SetParams(*cfg.DelayA)
		}
		if err != nil {
			return err
		}
		e.delayA.Reset()
	}
	if cfg.ReverbB != nil {
		e.reverbB, err = fx.NewReverb(cfg.SampleRate)
		if err == nil {
			err = e.reverbB.SetParams(*cfg.ReverbB)
		}
		if err != nil {
			return err
		}
		e.reverbB.Reset()
	}
	if cfg.CompSidechainTrack < 0 || cfg.CompSidechainTrack > cfg.Tracks && cfg.CompSidechainTrack != SFXSidechain || cfg.CompMusic == nil && cfg.CompSidechainTrack != 0 {
		return Error("invalid compressor sidechain track")
	}
	if cfg.CompMusic != nil {
		e.compMusic, err = fx.NewCompressor(cfg.SampleRate)
		if err == nil {
			err = e.compMusic.SetParams(*cfg.CompMusic)
		}
		if err != nil {
			return err
		}
		e.compMusic.Reset()
		e.compSidechainTrack = cfg.CompSidechainTrack
	}
	return nil
}

//go:noinline
func (e *Engine) initTrackVoices(cfg *Config) (int, error) {
	if cfg.MasterProcessor != nil {
		latency := cfg.MasterProcessor.LatencyFrames()
		if latency < 0 || latency > cfg.SampleRate {
			return 0, Error("invalid master processor latency")
		}
		e.masterProcessor = cfg.MasterProcessor
		e.masterProcessor.Reset()
	}
	var err error
	voices := 0
	hasDrive := false
	for i := 0; i < cfg.Tracks; i++ {
		if cfg.Track[i].InsertDrive != nil {
			hasDrive = true
		}
	}
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
			return 0, Error("track mixer parameter is out of range")
		}
		if math.IsNaN(spec.SendA) || math.IsInf(spec.SendA, 0) || spec.SendA < 0 || spec.SendA > 1 || spec.SendA > 0 && e.delayA == nil {
			return 0, Error("track send A is invalid or has no delay return")
		}
		if math.IsNaN(spec.SendB) || math.IsInf(spec.SendB, 0) || spec.SendB < 0 || spec.SendB > 1 || spec.SendB > 0 && e.reverbB == nil {
			return 0, Error("track send B is invalid or has no reverb return")
		}
		v := &e.voices[i]
		if spec.Polyphony != 0 && spec.Polyphony != 4 || spec.Polyphony == 4 && spec.Kind != VoiceGraph {
			return 0, Error("polyphony requires a graph track and a four-voice limit")
		}
		e.director[i].gain = 1
		v.kind = spec.Kind
		v.mix = mix.NewTrack(gain, spec.Pan, false)
		if spec.Mute {
			v.mix = mix.Track{}
		}
		v.targetMix = v.mix
		v.mixSmooth = e.paramAlpha[kernel.ParamMixGain]
		v.sendSmooth = e.paramAlpha[kernel.ParamMixSendA]
		v.gainDB, v.pan = float32(gain), float32(spec.Pan)
		v.muteGain, v.muteTarget = 1, 1
		v.muteStep = 1 / float32(math.Ceil(float64(kernel.Params[kernel.ParamMixMute].SmoothingMS)*.001*float64(cfg.SampleRate)))
		v.sendA, v.sendB = float32(spec.SendA), float32(spec.SendB)
		v.targetSendA, v.targetSendB = v.sendA, v.sendB
		v.sendAPre, v.sendBPre = spec.SendPre || spec.SendAPre, spec.SendPre || spec.SendBPre
		v.sourceOff, v.muted, v.soloed = spec.Mute, spec.Mute, spec.Solo
		if spec.Solo {
			e.soloCount++
		}
		v.busSFX = spec.BusSFX
		if spec.BusSFX {
			e.hasSFX = true
		}
		if spec.InsertDrive != nil {
			if spec.Kind == VoiceOff {
				return 0, Error("silent track cannot have a drive insert")
			}
			v.insert, err = fx.NewDrive(cfg.SampleRate)
			if err == nil {
				err = v.insert.SetParams(*spec.InsertDrive)
			}
			if err != nil {
				return 0, err
			}
			v.insert.Reset()
		} else if hasDrive {
			v.align, err = mix.NewDelay(fx.DriveLatencyFrames)
			if err != nil {
				return 0, err
			}
		}
		switch spec.Kind {
		case VoiceOff:
		case VoiceAcid:
			voices++
			v.acid, err = acid.New(cfg.SampleRate)
			if err == nil && spec.Acid != (acid.Params{}) {
				err = v.acid.SetParams(spec.Acid)
			}
			if err == nil {
				v.acidTarget = v.acid.Params()
			}
		case VoiceDrums:
			e.patterns[i].drumSlots = new([16][drum.LaneCount]seq.Pattern)
			for slot := range e.patterns[i].drumSlots {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					e.patterns[i].drumSlots[slot][lane] = e.patterns[i].slots[slot]
				}
			}
			v.drums, err = drum.New(cfg.SampleRate, cfg.Seed)
			if err == nil {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					if spec.Kit != nil {
						binding := spec.Kit[lane]
						switch binding.Kind {
						case KitLaneOff:
							err = v.drums.Disable(lane)
						case KitLaneBuiltin:
							err = v.drums.SetRecipe(lane, binding.Recipe)
						case KitLaneGraph:
							err = v.drums.SetGraph(lane, binding.Program)
						default:
							return 0, Error("unknown kit lane kind")
						}
						if binding.Kind != KitLaneOff {
							voices++
						}
					} else if lane >= drum.LT && spec.Drums[lane] == (drum.Params{}) {
						// Zero params leave added lanes off. Legacy lanes retain
						// their defaults for direct host configurations.
						err = v.drums.Disable(lane)
					} else {
						voices++
						if spec.Drums[lane] != (drum.Params{}) {
							err = v.drums.SetParams(lane, spec.Drums[lane])
						}
					}
					if err != nil {
						break
					}
					v.drumTargets[lane] = v.drums.Params(lane)
				}
			}
		case VoiceGraph:
			if spec.Polyphony == 4 {
				voices += 4
				v.poly, err = graph.NewPool(spec.Graph, cfg.SampleRate)
			} else {
				voices++
				v.graph, err = graph.NewVoice(spec.Graph, cfg.SampleRate)
			}
		case VoicePiano:
			voices += piano.MaxVoices
			v.piano, err = piano.New(cfg.SampleRate)
			if err == nil {
				err = v.piano.SetSustain(spec.PianoSustain)
				v.pianoSustain = spec.PianoSustain
			}
		default:
			return 0, Error("unknown voice kind")
		}
		if err != nil {
			return 0, err
		}
	}
	return voices, nil
}

//go:noinline
func (e *Engine) loadPatterns(cfg *Config) error {
	if len(cfg.Patterns) != 0 && len(cfg.Patterns) != cfg.Tracks {
		return Error("pattern bank count must match tracks")
	}
	// Iterate the fixed banks by address. Copying the bank before validation
	// makes TinyGo scalarize all 16 chord arrays into a large load routine.
	for track := range cfg.Patterns {
		bank := &cfg.Patterns[track]
		isDrum := e.voices[track].kind == VoiceDrums
		if isDrum != (bank.Drums != nil) {
			return Error("pattern bank kind differs from track")
		}
		for slot := range bank.Slots {
			pattern := &bank.Slots[slot]
			if pattern.Len == 0 {
				if pattern.Chords != [64]seq.ChordStep{} {
					return Error("unused slot contains chord payload")
				}
				if isDrum {
					for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
						if bank.Drums[slot][lane].Len != 0 {
							return Error("unused drum slot contains lane data")
						}
					}
				}
				continue
			}
			if pattern.Validate() != nil {
				return Error("invalid preloaded pattern")
			}
			if e.voices[track].kind == VoicePiano && !validPianoPattern(*pattern) {
				return Error("piano notes must be MIDI 21 to 108")
			}
			for step := uint8(0); step < pattern.Len; step++ {
				if pattern.Chords[step].Count > 0 && e.voices[track].poly == nil && e.voices[track].kind != VoicePiano {
					return Error("chord pattern requires a polyphonic graph track")
				}
			}
			e.patterns[track].slots[slot] = *pattern
			if !isDrum {
				continue
			}
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				lanePattern := bank.Drums[slot][lane]
				if lanePattern.Len != pattern.Len || lanePattern.SwingPermille != pattern.SwingPermille || lanePattern.GatePercent != pattern.GatePercent || lanePattern.Seed != pattern.Seed || lanePattern.Transpose != 0 || lanePattern.Validate() != nil {
					return Error("invalid preloaded drum lane")
				}
				for step := uint8(0); step < lanePattern.Len; step++ {
					decoded, _ := seq.UnpackStep(lanePattern.Steps[step])
					if decoded.Gate && (decoded.Tie || decoded.Note != uint8(lane)) {
						return Error("drum lane step has wrong routing")
					}
				}
				e.patterns[track].drumSlots[slot][lane] = lanePattern
			}
		}
	}
	return nil
}

//go:noinline
func (e *Engine) loadArrangement(cfg *Config, bpmMilli int64) error {
	if len(cfg.Scenes) > 1<<16 || len(cfg.Song) > 1<<16 {
		return Error("arrangement exceeds the wire index range")
	}
	for _, scene := range cfg.Scenes {
		for track, binding := range scene.Track {
			if binding.Mode > SceneSlot || track >= cfg.Tracks && binding.Mode != SceneKeep || binding.Mode == SceneSlot && binding.Slot >= 16 {
				return Error("scene binding is out of range")
			}
		}
		type parameterAddress struct {
			track uint8
			id    kernel.ParamID
		}
		seen := make(map[parameterAddress]bool, len(scene.Settings))
		for _, setting := range scene.Settings {
			address := parameterAddress{track: setting.Track, id: setting.ID}
			spec, ok := kernel.Param(setting.ID)
			if !ok || !spec.Live || seen[address] {
				return Error("scene parameter setting is invalid")
			}
			seen[address] = true
			syncedDelay := setting.Division != fx.FreeDelay
			if syncedDelay {
				if setting.ID != kernel.ParamFxDelayTime || e.delayA == nil || setting.Division.String() == "invalid" || setting.Value != 0 {
					return Error("scene delay division is invalid")
				}
				params := e.delayA.Params()
				params.Division, params.TimeMs = setting.Division, 0
				if err := params.ValidateTempo(bpmMilli); err != nil {
					return err
				}
			}
			off := spec.Off && math.IsInf(float64(setting.Value), -1)
			if spec.Scope == "track" && int(setting.Track) >= cfg.Tracks || spec.Scope == "global" && setting.Track != 0xff {
				return Error("scene parameter owner is out of range")
			}
			if math.IsNaN(float64(setting.Value)) || math.IsInf(float64(setting.Value), 0) && !off || !syncedDelay && !off && (setting.Value < spec.Min || setting.Value > spec.Max) {
				return Error("scene parameter value is out of range")
			}
			if !syncedDelay && spec.Curve == "toggle" && setting.Value != 0 && setting.Value != 1 {
				return Error("scene toggle value is out of range")
			}
		}
	}
	for _, entry := range cfg.Song {
		if int(entry.Scene) >= len(cfg.Scenes) || entry.Bars < 1 || entry.Bars > 999 {
			return Error("song entry is out of range")
		}
	}
	e.scenes = make([]Scene, len(cfg.Scenes))
	for i := range cfg.Scenes {
		e.scenes[i] = cfg.Scenes[i]
		e.scenes[i].Settings = append([]SceneSetting(nil), cfg.Scenes[i].Settings...)
	}
	e.song = append([]SongEntry(nil), cfg.Song...)
	e.loopSong = cfg.LoopSong
	return nil
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
		e.patterns[i].chainLen = 0
		e.patterns[i].chainArmed = false
		e.patterns[i].chainRepeat = 0
	}
	e.limiter.Reset()
	if e.masterProcessor != nil {
		e.masterProcessor.Reset()
	}
	if e.delayA != nil {
		e.delayA.Reset()
	}
	if e.reverbB != nil {
		e.reverbB.Reset()
	}
	if e.compMusic != nil {
		e.compMusic.Reset()
	}
	e.transport, _ = seq.NewTransport(e.sampleRate, e.bpmMilli)
	e.commandRead, e.commandWrite, e.messageRead, e.messageWrite = 0, 0, 0, 0
	e.overflowRead, e.overflowLen = 0, 0
	e.pendingLen, e.meterBlock = 0, 0
	e.liveEvents, e.phraseBars, e.lastBarTick = false, 0, -1
	e.macroLayerEnabled = [16]bool{}
	e.playheadFrames = e.sampleRate / 60
	e.renderFrame, e.renderFrames = 0, 0
	e.layerMask, e.layerAuthored = (1<<e.tracks)-1, (1<<e.tracks)-1
	e.songMode, e.songIndex, e.songEndTick = false, 0, 0
	e.manualSceneTick = -1
	e.currentScene = -1
	for i := range e.manualPatternTick {
		e.manualPatternTick[i] = -1
	}
	e.resetDirector()
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
			if e.delayA != nil && e.delayA.SetTempo(e.transport.BPMMilli()) != nil {
				e.fault(13)
				clear(outL[frame:])
				clear(outR[frame:])
				return
			}
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
		e.advanceDirector()
		e.processBarBoundary()
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
		var sendAL, sendAR, sendBL, sendBR, sideL, sideR float32
		for track := 0; track < e.tracks; track++ {
			v := &e.voices[track]
			v.mix.Left += (v.targetMix.Left - v.mix.Left) * v.mixSmooth
			v.mix.Right += (v.targetMix.Right - v.mix.Right) * v.mixSmooth
			v.sendA += (v.targetSendA - v.sendA) * v.sendSmooth
			v.sendB += (v.targetSendB - v.sendB) * v.sendSmooth
			if v.muteGain < v.muteTarget {
				v.muteGain = min(v.muteTarget, v.muteGain+v.muteStep)
			} else if v.muteGain > v.muteTarget {
				v.muteGain = max(v.muteTarget, v.muteGain-v.muteStep)
			}
			var left, right float32
			switch v.kind {
			case VoiceAcid:
				sample := v.acid.Next()
				if v.acid.Fault() {
					e.fault(2)
					clear(outL[frame:])
					clear(outR[frame:])
					return
				}
				left, right = sample, sample
			case VoiceDrums:
				left, right = v.drums.NextStereo()
				if v.drums.Fault() {
					e.fault(3)
					clear(outL[frame:])
					clear(outR[frame:])
					return
				}
			case VoiceGraph:
				var sample float32
				if v.poly != nil {
					sample = v.poly.Next()
				} else {
					sample = v.graph.Next()
				}
				left, right = sample, sample
			case VoicePiano:
				left, right = v.piano.NextStereo()
			}
			if v.insert != nil {
				left, right = v.insert.Process(left, right)
				if v.insert.Fault() {
					e.fault(12)
					clear(outL[frame:])
					clear(outR[frame:])
					return
				}
			} else if v.align != nil {
				left, right = v.align.Process(left, right)
			}
			left *= e.director[track].gain
			right *= e.director[track].gain
			var trackL, trackR float32
			if e.layerMask&(1<<track) != 0 && v.kind != VoiceOff {
				trackL, trackR = left*(v.mix.Left*v.muteGain), right*(v.mix.Right*v.muteGain)
				if v.sendA > 0 && v.muteGain > 0 && !v.sourceOff {
					if v.sendAPre {
						sendAL += left * v.sendA * v.muteGain
						sendAR += right * v.sendA * v.muteGain
					} else {
						sendAL += left * v.mix.Left * v.muteGain * v.sendA
						sendAR += right * v.mix.Right * v.muteGain * v.sendA
					}
				}
				if v.sendB > 0 && v.muteGain > 0 && !v.sourceOff {
					if v.sendBPre {
						sendBL += left * v.sendB * v.muteGain
						sendBR += right * v.sendB * v.muteGain
					} else {
						sendBL += left * v.mix.Left * v.muteGain * v.sendB
						sendBR += right * v.mix.Right * v.muteGain * v.sendB
					}
				}
				tap := mix.Track{Left: v.mix.Left * v.muteGain, Right: v.mix.Right * v.muteGain}
				if v.busSFX {
					dry.AddSFX(left, right, tap)
				} else {
					dry.Add(left, right, tap)
				}
				if e.compSidechainTrack == track+1 {
					sideL, sideR = trackL, trackR
				}
			}
			accumulateMeter(&e.trackMeter[track], trackL, trackR)
		}
		if e.delayA != nil {
			returnL, returnR := e.delayA.Process(sendAL, sendAR)
			if e.delayA.Fault() {
				e.fault(14)
				clear(outL[frame:])
				clear(outR[frame:])
				return
			}
			accumulateMeter(&e.returnAMeter, returnL, returnR)
			dry.AddReturn(returnL, returnR)
		}
		if e.reverbB != nil {
			returnL, returnR := e.reverbB.Process(sendBL, sendBR)
			if e.reverbB.Fault() {
				e.fault(15)
				clear(outL[frame:])
				clear(outR[frame:])
				return
			}
			accumulateMeter(&e.returnBMeter, returnL, returnR)
			dry.AddReturn(returnL, returnR)
		}
		left, right := dry.Music()
		sfxL, sfxR := dry.SFX()
		accumulateMeter(&e.sfxMeter, sfxL, sfxR)
		if e.sfxBusMute || e.musicBusSolo && !e.sfxBusSolo {
			sfxL, sfxR = 0, 0
		}
		if e.compMusic != nil {
			if e.compSidechainTrack == 0 {
				left, right = e.compMusic.Process(left, right)
			} else if e.compSidechainTrack == SFXSidechain {
				left, right = e.compMusic.ProcessSidechain(left, right, sfxL, sfxR)
			} else {
				left, right = e.compMusic.ProcessSidechain(left, right, sideL, sideR)
			}
			if e.compMusic.Fault() {
				e.fault(16)
				clear(outL[frame:])
				clear(outR[frame:])
				return
			}
			e.compGR = max(e.compGR, float32(e.compMusic.GainReductionDB()))
		}
		accumulateMeter(&e.musicMeter, left, right)
		if e.musicBusMute || e.sfxBusSolo && !e.musicBusSolo {
			left, right = 0, 0
		}
		left, right = left+sfxL, right+sfxR
		if e.masterMute {
			left, right = 0, 0
		}
		if e.masterGain != 1 {
			left *= e.masterGain
			right *= e.masterGain
		}
		if e.masterProcessor != nil {
			left, right = e.masterProcessor.Process(left, right)
			if e.masterProcessor.Fault() {
				e.fault(19)
				clear(outL[frame:])
				clear(outR[frame:])
				return
			}
		}
		accumulateMeter(&e.preMasterMeter, left, right)
		outL[frame], outR[frame], _ = e.limiter.Process(left, right)
		if e.limiter.Fault() {
			e.fault(4)
			clear(outL[frame:])
			clear(outR[frame:])
			return
		}
		e.limiterGR = max(e.limiterGR, float32(e.limiter.GainReductionDB()))
		accumulateMeter(&e.masterMeter, outL[frame], outR[frame])
		blockPeak = max(blockPeak, float32(math.Max(math.Abs(float64(outL[frame])), math.Abs(float64(outR[frame])))))
		e.advanceMacroRamps()
		e.transport.Advance(1)
		if e.transport.Playing() {
			e.playheadFrames--
			if e.playheadFrames <= 0 {
				e.emit(cmd.Message{Kind: cmd.Playhead, Track: 0xff, Tick: e.transport.Tick()})
				e.playheadFrames = e.sampleRate / 60
			}
		} else {
			e.playheadFrames = e.sampleRate / 60
		}
	}
	e.renderFrames = 0
	e.meterFrames += uint32(len(outL))
	e.meterBlock++
	if e.meterRate != 0 && e.meterBlock%e.meterRate == 0 {
		e.emitMeters(blockPeak)
	}
}

func accumulateMeter(m *meterAccum, left, right float32) {
	peak := float32(math.Max(math.Abs(float64(left)), math.Abs(float64(right))))
	m.peak = max(m.peak, peak)
	m.power += (float64(left)*float64(left) + float64(right)*float64(right)) * .5
}

func smoothingAlpha(id kernel.ParamID, sampleRate int) float32 {
	return float32(1 - math.Exp(-1/(float64(kernel.Params[id].SmoothingMS)*.001*float64(sampleRate))))
}

func (e *Engine) emitMeters(masterBlockPeak float32) {
	tick := e.transport.Tick()
	for track := 0; track < e.tracks; track++ {
		e.emitMeterPair(uint8(track), &e.trackMeter[track], tick)
		e.trackMeter[track] = meterAccum{}
	}
	e.emitMeterPair(0xf0, &e.returnAMeter, tick)
	e.emitMeterPair(0xf1, &e.returnBMeter, tick)
	e.emitMeterPair(0xf2, &e.musicMeter, tick)
	e.emitMeterPair(0xf3, &e.sfxMeter, tick)
	e.emitMeterPair(0xfd, &e.preMasterMeter, tick)
	e.emit(cmd.Message{Kind: cmd.Meter, Track: 0xff, A: 0, B: math.Float32bits(masterBlockPeak), Tick: tick})
	e.emit(cmd.Message{Kind: cmd.Meter, Track: 0xfc, A: 2, B: math.Float32bits(e.compGR), Tick: tick})
	e.emit(cmd.Message{Kind: cmd.Meter, Track: 0xfb, A: 2, B: math.Float32bits(e.limiterGR), Tick: tick})
	e.emit(cmd.Message{Kind: cmd.Meter, Track: 0xff, A: 1, B: math.Float32bits(meterRMS(&e.masterMeter, e.meterFrames)), Tick: tick})
	e.masterMeter = meterAccum{}
	e.returnAMeter, e.returnBMeter = meterAccum{}, meterAccum{}
	e.musicMeter, e.sfxMeter, e.preMasterMeter = meterAccum{}, meterAccum{}, meterAccum{}
	e.compGR, e.limiterGR, e.meterFrames = 0, 0, 0
}

func (e *Engine) emitMeterPair(track uint8, meter *meterAccum, tick int64) {
	e.emit(cmd.Message{Kind: cmd.Meter, Track: track, A: 0, B: math.Float32bits(meter.peak), Tick: tick})
	e.emit(cmd.Message{Kind: cmd.Meter, Track: track, A: 1, B: math.Float32bits(meterRMS(meter, e.meterFrames)), Tick: tick})
}

func meterRMS(meter *meterAccum, frames uint32) float32 {
	if frames == 0 {
		return 0
	}
	return float32(math.Sqrt(meter.power / float64(frames)))
}

func (e *Engine) advanceMacroRamps() {
	for id, total := range e.macroRampFrames {
		if total == 0 {
			continue
		}
		elapsed := e.macroRampElapsed[id] + 1
		if elapsed >= total {
			e.macroCurrent[id] = e.macroTarget[id]
			e.macroRampFrames[id], e.macroRampElapsed[id] = 0, 0
			if e.liveEvents {
				e.emit(cmd.Message{Kind: cmd.MacroReached, Track: uint8(id), Tick: e.transport.Tick()})
			}
			continue
		}
		e.macroRampElapsed[id] = elapsed
		e.macroCurrent[id] = e.macroStart[id] + (e.macroTarget[id]-e.macroStart[id])*float32(elapsed)/float32(total)
	}
}

func (e *Engine) drainCommands() {
	for e.commandRead != e.commandWrite {
		c := e.commands[e.commandRead%uint16(len(e.commands))]
		e.commandRead++
		if c.Op == cmd.OpSetLayerMask && c.Tick == 0 {
			c.Tick = (e.transport.Tick()/seq.TicksPerBar + 1) * seq.TicksPerBar
		}
		if e.transport.Playing() && c.Tick == 0 && (c.Op == cmd.OpSetStep || c.Op == cmd.OpSetChordStep || c.Op == cmd.OpSetPatternLen || c.Op == cmd.OpSetPatternMeta) {
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
	chainPhase := false
	for {
		best, priority := -1, 5
		for i := 0; i < e.pendingLen; i++ {
			if e.pending[i].Tick <= e.transport.Tick() && commandPriority(e.pending[i].Op) < priority {
				best, priority = i, commandPriority(e.pending[i].Op)
			}
		}
		if best < 0 {
			e.advanceChains()
			return
		}
		if priority > 1 && !chainPhase {
			e.advanceChains() // automatic switches precede same-tick parameter changes
			if e.faulted {
				return
			}
			chainPhase = true
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
	case cmd.OpSelectPattern, cmd.OpLaunchScene, cmd.OpSetChain, cmd.OpSetState, cmd.OpTriggerStinger:
		return 1
	case cmd.OpNoteOn, cmd.OpPlay:
		return 3
	default:
		return 2
	}
}

func (e *Engine) apply(c cmd.Command) {
	switch c.Op {
	case cmd.OpSetState, cmd.OpTriggerStinger:
		e.applyDirector(c)
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
		e.resetDirector()
		e.transport.Stop()
		e.lastBarTick = -1
		for i := 0; i < e.tracks; i++ {
			e.noteOff(i, 0xffff)
			e.patterns[i].playingNote = 0
			e.patterns[i].heldValid = false
		}
	case cmd.OpSeek:
		e.resetDirector()
		if e.transport.SeekTick(int64(c.Arg0)*seq.TicksPerBar+int64(c.Arg1)) != nil {
			e.fault(6)
			return
		}
		e.lastBarTick = -1
		for i := 0; i < e.tracks; i++ {
			e.resetVoice(i)
		}
		e.limiter.Reset()
		if e.delayA != nil {
			e.delayA.Reset()
		}
		if e.reverbB != nil {
			e.reverbB.Reset()
		}
		if e.compMusic != nil {
			e.compMusic.Reset()
		}
		for i := 0; i < e.tracks; i++ {
			e.patterns[i].playingNote = 0
			e.patterns[i].heldValid = false
			if e.patterns[i].chainLen != 0 {
				e.patterns[i].chainNext = 0
				e.patterns[i].chainRepeat = 0
				e.patterns[i].chainDue = ((e.transport.Tick() + seq.TicksPerStep - 1) / seq.TicksPerStep) * seq.TicksPerStep
				e.patterns[i].chainArmed = true
			}
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
	case cmd.OpSetParam:
		e.setParam(c)
	case cmd.OpDefineMacro:
		e.liveEvents = true
		id := int(c.Index)
		value := math.Float32frombits(c.Arg0)
		e.macroCurrent[id], e.macroStart[id], e.macroTarget[id] = value, value, value
		e.macroRampFrames[id], e.macroRampElapsed[id] = 0, 0
	case cmd.OpSetMacro:
		e.liveEvents = true
		id := int(c.Index)
		target := math.Float32frombits(c.Arg0)
		e.macroStart[id], e.macroTarget[id] = e.macroCurrent[id], target
		e.macroRampElapsed[id], e.macroRampFrames[id] = 0, c.Arg1
		if c.Arg1 == 0 {
			e.macroCurrent[id] = target
			e.emit(cmd.Message{Kind: cmd.MacroReached, Track: uint8(id), Tick: e.transport.Tick()})
		}
	case cmd.OpSetLayers:
		e.liveEvents = true
		id := int(c.Index)
		e.macroLayerEnabled[id] = true
		e.macroLayerInitialized[id] = false
		e.macroLayerThresholds[id] = [4]uint8{}
		e.macroLayerThresholdCount[id] = 0
		thresholds := c.Arg0
		for level := range 4 {
			threshold := uint8(thresholds)
			if threshold == 0 {
				break
			}
			e.macroLayerThresholds[id][level] = threshold
			e.macroLayerThresholdCount[id]++
			thresholds >>= 8
		}
		e.macroLayerRelease[id] = uint8(c.Arg1)
		if e.macroLayerRelease[id] == 0 {
			e.macroLayerRelease[id] = 3
		}
		e.macroLayerLevel[id], e.macroLayerQuiet[id] = 0, 0
	case cmd.OpSetLayerMasks:
		id := int(c.Index)
		e.macroLayerMasks[id] = [4]uint32{uint32(uint16(c.Arg0)), uint32(uint16(c.Arg0 >> 16)), uint32(uint16(c.Arg1)), uint32(uint16(c.Arg1 >> 16))}
		e.macroLayerInitialized[id] = false
	case cmd.OpSetPhraseBars:
		e.liveEvents = true
		e.phraseBars = c.Arg0
	case cmd.OpNoteOn:
		if e.voices[c.Track].poly != nil {
			e.fault(cmd.FaultPolyLive)
			return
		}
		track := int(c.Track)
		note, velocity := uint8(c.Arg0), uint8(c.Arg0>>8)
		accent, slide := c.Arg0&(1<<16) != 0, c.Arg0&(1<<17) != 0
		v := &e.voices[track]
		if v.kind == VoicePiano && e.patterns[track].playingNote != 0 {
			e.releasePianoPattern(track)
		}
		e.patterns[track].playingNote = 0
		e.patterns[track].heldValid = false
		switch v.kind {
		case VoiceAcid:
			v.acid.NoteOn(note, accent, slide, velocity)
		case VoiceGraph:
			v.graph.NoteOn(note, velocity, slide)
		case VoicePiano:
			if v.piano.NoteOn(note, velocity) != nil {
				e.fault(8)
				return
			}
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
		if e.voices[c.Track].poly != nil {
			e.fault(cmd.FaultPolyLive)
			return
		}
		if e.voices[c.Track].kind == VoicePiano && c.Index != 0xffff && (c.Index < 21 || c.Index > 108) {
			e.fault(8)
			return
		}
		if e.voices[c.Track].kind == VoiceDrums && c.Index >= uint16(drum.LaneCount) {
			e.fault(8)
			return
		}
		e.noteOff(int(c.Track), c.Index)
		if e.voices[c.Track].kind != VoicePiano || c.Index == 0xffff {
			e.patterns[c.Track].playingNote = 0
			e.patterns[c.Track].heldValid = false
		}
		e.emit(cmd.Message{Kind: cmd.NoteOff, Track: c.Track, Tick: e.transport.Tick()})
	case cmd.OpSetStep, cmd.OpSetChordStep, cmd.OpSetPatternLen, cmd.OpSetPatternMeta, cmd.OpSelectPattern:
		if c.Op == cmd.OpSelectPattern && c.Arg0 == 0 {
			e.manualPatternTick[c.Track] = e.transport.Tick()
		}
		e.applyPatternCommand(c)
	case cmd.OpSetChain:
		e.applyChainCommand(c)
	case cmd.OpLaunchScene:
		e.applySceneCommand(c)
	case cmd.OpSetLayerMask:
		e.setAuthoredLayerMask(c.Arg0)
	case cmd.OpMeterRate:
		e.meterRate = c.Arg0
	default:
		e.fault(10) // No accepted command may be silently discarded.
	}
}

func (e *Engine) setParam(c cmd.Command) { e.setParamMode(c, false) }

func (e *Engine) setParamImmediate(c cmd.Command) { e.setParamMode(c, true) }

func (e *Engine) setParamMode(c cmd.Command, immediate bool) {
	spec, ok := kernel.Param(kernel.ParamID(c.Index))
	if !ok || !spec.Live {
		e.fault(18)
		return
	}
	value := math.Float32frombits(c.Arg0)
	off := spec.Off && math.IsInf(float64(value), -1)
	if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) && !off || !off && (value < spec.Min || value > spec.Max) {
		e.fault(18)
		return
	}
	if spec.Curve == "toggle" && value != 0 && value != 1 {
		e.fault(18)
		return
	}
	if spec.Scope == "track" {
		if int(c.Track) >= e.tracks {
			e.fault(18)
			return
		}
		v := &e.voices[c.Track]
		switch kernel.ParamID(c.Index) {
		case kernel.ParamMixGain:
			v.gainDB = value
			v.sourceOff = off
			if off {
				e.layerAuthored &^= 1 << c.Track
			} else {
				e.layerAuthored |= 1 << c.Track
			}
			e.composeLayerMask()
			if off {
				v.targetMix = mix.Track{}
			} else {
				v.targetMix = mix.NewTrack(float64(v.gainDB), float64(v.pan), false)
			}
			if immediate {
				v.mix = v.targetMix
			}
		case kernel.ParamMixPan:
			v.pan = value
			if v.sourceOff {
				v.targetMix = mix.Track{}
			} else {
				v.targetMix = mix.NewTrack(float64(v.gainDB), float64(v.pan), false)
			}
			if immediate {
				v.mix = v.targetMix
			}
		case kernel.ParamMixSendA:
			v.targetSendA = value
			if immediate {
				v.sendA = value
			}
		case kernel.ParamMixSendB:
			v.targetSendB = value
			if immediate {
				v.sendB = value
			}
		case kernel.ParamMixMute:
			v.muted = value == 1
			e.updateMuteTargets()
		case kernel.ParamMixSolo:
			if v.soloed != (value == 1) {
				v.soloed = value == 1
				if v.soloed {
					e.soloCount++
				} else {
					e.soloCount--
				}
				e.updateMuteTargets()
			}
		case kernel.ParamPianoSustain:
			if v.kind != VoicePiano || v.piano.SetSustain(value) != nil {
				e.fault(18)
				return
			}
			v.pianoSustain = value
		case kernel.ParamAcidCutoff, kernel.ParamAcidReso, kernel.ParamAcidEnvmod, kernel.ParamAcidDecay, kernel.ParamAcidAccent:
			if v.kind != VoiceAcid || v.acid == nil {
				e.fault(18)
				return
			}
			params := v.acidTarget
			switch kernel.ParamID(c.Index) {
			case kernel.ParamAcidCutoff:
				params.Cutoff = float64(value)
			case kernel.ParamAcidReso:
				params.Resonance = float64(value)
			case kernel.ParamAcidEnvmod:
				params.EnvMod = float64(value)
			case kernel.ParamAcidDecay:
				params.Decay = float64(value) / 1000
			case kernel.ParamAcidAccent:
				params.Accent = float64(value)
			}
			v.acidTarget = params
			var setErr error
			if immediate {
				setErr = v.acid.SetParams(params)
			} else {
				setErr = v.acid.SetParamsTarget(params, float64(e.paramAlpha[c.Index]))
			}
			if setErr != nil {
				e.fault(18)
			}
		default:
			lane, control, drumParameter := drumParamControl(kernel.ParamID(c.Index))
			if !drumParameter || v.kind != VoiceDrums || v.drums == nil {
				e.fault(18)
				return
			}
			params := v.drumTargets[lane]
			switch control {
			case drumTune:
				params.Tune = float64(value)
			case drumDecay:
				params.Decay = float64(value) / 1000
			case drumLevel:
				params.LevelDB = float64(value)
				if off {
					params.LevelDB = -1000
				}
			case drumPan:
				params.Pan = float64(value)
			}
			v.drumTargets[lane] = params
			var setErr error
			if immediate {
				setErr = v.drums.SetParams(lane, params)
			} else {
				setErr = v.drums.SetParamsTarget(lane, params, float64(e.paramAlpha[c.Index]))
			}
			if setErr != nil {
				e.fault(18)
			}
		}
		return
	}
	if c.Track != 0xff {
		e.fault(18)
		return
	}
	e.setGlobalParam(kernel.ParamID(c.Index), value)
}

type drumParamField uint8

const (
	drumTune drumParamField = iota
	drumDecay
	drumLevel
	drumPan
)

func drumParamControl(id kernel.ParamID) (drum.Lane, drumParamField, bool) {
	switch id {
	case kernel.ParamDrumBdTune:
		return drum.BD, drumTune, true
	case kernel.ParamDrumBdDecay:
		return drum.BD, drumDecay, true
	case kernel.ParamDrumBdLevel:
		return drum.BD, drumLevel, true
	case kernel.ParamDrumBdPan:
		return drum.BD, drumPan, true
	case kernel.ParamDrumSdTune:
		return drum.SD, drumTune, true
	case kernel.ParamDrumSdDecay:
		return drum.SD, drumDecay, true
	case kernel.ParamDrumSdLevel:
		return drum.SD, drumLevel, true
	case kernel.ParamDrumSdPan:
		return drum.SD, drumPan, true
	case kernel.ParamDrumChTune:
		return drum.CH, drumTune, true
	case kernel.ParamDrumChDecay:
		return drum.CH, drumDecay, true
	case kernel.ParamDrumChLevel:
		return drum.CH, drumLevel, true
	case kernel.ParamDrumChPan:
		return drum.CH, drumPan, true
	case kernel.ParamDrumOhTune:
		return drum.OH, drumTune, true
	case kernel.ParamDrumOhDecay:
		return drum.OH, drumDecay, true
	case kernel.ParamDrumOhLevel:
		return drum.OH, drumLevel, true
	case kernel.ParamDrumOhPan:
		return drum.OH, drumPan, true
	case kernel.ParamDrumCpTune:
		return drum.CP, drumTune, true
	case kernel.ParamDrumCpDecay:
		return drum.CP, drumDecay, true
	case kernel.ParamDrumCpLevel:
		return drum.CP, drumLevel, true
	case kernel.ParamDrumCpPan:
		return drum.CP, drumPan, true
	case kernel.ParamDrumRsTune:
		return drum.RS, drumTune, true
	case kernel.ParamDrumRsDecay:
		return drum.RS, drumDecay, true
	case kernel.ParamDrumRsLevel:
		return drum.RS, drumLevel, true
	case kernel.ParamDrumRsPan:
		return drum.RS, drumPan, true
	case kernel.ParamDrumLtTune:
		return drum.LT, drumTune, true
	case kernel.ParamDrumLtDecay:
		return drum.LT, drumDecay, true
	case kernel.ParamDrumLtLevel:
		return drum.LT, drumLevel, true
	case kernel.ParamDrumLtPan:
		return drum.LT, drumPan, true
	case kernel.ParamDrumMtTune:
		return drum.MT, drumTune, true
	case kernel.ParamDrumMtDecay:
		return drum.MT, drumDecay, true
	case kernel.ParamDrumMtLevel:
		return drum.MT, drumLevel, true
	case kernel.ParamDrumMtPan:
		return drum.MT, drumPan, true
	case kernel.ParamDrumHtTune:
		return drum.HT, drumTune, true
	case kernel.ParamDrumHtDecay:
		return drum.HT, drumDecay, true
	case kernel.ParamDrumHtLevel:
		return drum.HT, drumLevel, true
	case kernel.ParamDrumHtPan:
		return drum.HT, drumPan, true
	case kernel.ParamDrumCbTune:
		return drum.CB, drumTune, true
	case kernel.ParamDrumCbDecay:
		return drum.CB, drumDecay, true
	case kernel.ParamDrumCbLevel:
		return drum.CB, drumLevel, true
	case kernel.ParamDrumCbPan:
		return drum.CB, drumPan, true
	case kernel.ParamDrumCyTune:
		return drum.CY, drumTune, true
	case kernel.ParamDrumCyDecay:
		return drum.CY, drumDecay, true
	case kernel.ParamDrumCyLevel:
		return drum.CY, drumLevel, true
	case kernel.ParamDrumCyPan:
		return drum.CY, drumPan, true
	default:
		return 0, 0, false
	}
}

func (e *Engine) updateMuteTargets() {
	for track := 0; track < e.tracks; track++ {
		v := &e.voices[track]
		v.muteTarget = 1
		if v.muted || e.soloCount > 0 && !v.soloed {
			v.muteTarget = 0
		}
	}
}

func (e *Engine) setDelayDivision(division fx.DelayDivision) {
	if e.delayA == nil {
		e.fault(18)
		return
	}
	params := e.delayA.Params()
	params.Division, params.TimeMs = division, 0
	if e.delayA.SetParams(params) != nil {
		e.fault(18)
	}
}

func (e *Engine) setGlobalParam(id kernel.ParamID, value float32) {
	switch id {
	case kernel.ParamFxDriveGain, kernel.ParamFxDriveTone, kernel.ParamFxDriveMix:
		for track := 0; track < e.tracks; track++ {
			drive := e.voices[track].insert
			if drive == nil {
				continue
			}
			params := drive.Params()
			switch id {
			case kernel.ParamFxDriveGain:
				params.GainDB = float64(value)
			case kernel.ParamFxDriveTone:
				params.ToneHz = float64(value)
			case kernel.ParamFxDriveMix:
				params.Mix = float64(value)
			}
			if drive.SetParams(params) != nil {
				e.fault(18)
				return
			}
		}
	case kernel.ParamFxDelayTime, kernel.ParamFxDelayFeedback, kernel.ParamFxDelayDamp, kernel.ParamFxDelayPingpong, kernel.ParamFxDelayWidth, kernel.ParamFxDelayMix:
		if e.delayA != nil {
			params := e.delayA.Params()
			switch id {
			case kernel.ParamFxDelayTime:
				params.Division, params.TimeMs = fx.FreeDelay, float64(value)
			case kernel.ParamFxDelayFeedback:
				params.Feedback = float64(value)
			case kernel.ParamFxDelayDamp:
				params.DampHz = float64(value)
			case kernel.ParamFxDelayPingpong:
				params.PingPong = value == 1
			case kernel.ParamFxDelayWidth:
				params.Width = float64(value)
			case kernel.ParamFxDelayMix:
				params.Mix = float64(value)
			}
			if e.delayA.SetParams(params) != nil {
				e.fault(18)
				return
			}
		}
	case kernel.ParamFxReverbSize, kernel.ParamFxReverbDecay, kernel.ParamFxReverbDamp, kernel.ParamFxReverbHighpass, kernel.ParamFxReverbPredelay, kernel.ParamFxReverbMix:
		if e.reverbB != nil {
			params := e.reverbB.Params()
			switch id {
			case kernel.ParamFxReverbSize:
				params.Size = float64(value)
			case kernel.ParamFxReverbDecay:
				params.DecaySec = float64(value) / 1000
			case kernel.ParamFxReverbDamp:
				params.DampHz = float64(value)
			case kernel.ParamFxReverbHighpass:
				params.HighpassHz = float64(value)
			case kernel.ParamFxReverbPredelay:
				params.PredelayMs = float64(value)
			case kernel.ParamFxReverbMix:
				params.Mix = float64(value)
			}
			if e.reverbB.SetParams(params) != nil {
				e.fault(18)
				return
			}
		}
	case kernel.ParamFxCompThreshold, kernel.ParamFxCompRatio, kernel.ParamFxCompKnee, kernel.ParamFxCompAttack, kernel.ParamFxCompRelease, kernel.ParamFxCompMakeup, kernel.ParamFxCompMix:
		if e.compMusic != nil {
			params := e.compMusic.Params()
			switch id {
			case kernel.ParamFxCompThreshold:
				params.Threshold = float64(value)
			case kernel.ParamFxCompRatio:
				params.Ratio = float64(value)
			case kernel.ParamFxCompKnee:
				params.Knee = float64(value)
			case kernel.ParamFxCompAttack:
				params.AttackMs = float64(value)
			case kernel.ParamFxCompRelease:
				params.ReleaseMs = float64(value)
			case kernel.ParamFxCompMakeup:
				params.MakeupAuto, params.MakeupDB = false, float64(value)
			case kernel.ParamFxCompMix:
				params.Mix = float64(value)
			}
			if e.compMusic.SetParams(params) != nil {
				e.fault(18)
				return
			}
		}
	default:
		e.fault(18)
		return
	}
}

func (e *Engine) noteOff(track int, lane uint16) {
	v := &e.voices[track]
	switch v.kind {
	case VoiceAcid:
		v.acid.NoteOff()
	case VoiceGraph:
		if v.poly != nil {
			v.poly.ReleaseAll()
		} else {
			v.graph.NoteOff()
		}
	case VoicePiano:
		if lane == 0xffff {
			v.piano.AllNotesOff()
		} else {
			v.piano.NoteOff(uint8(lane))
		}
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
	if v.insert != nil {
		v.insert.Reset()
	}
	if v.align != nil {
		v.align.Reset()
	}
	switch v.kind {
	case VoiceAcid:
		v.acid.Reset()
	case VoiceGraph:
		if v.poly != nil {
			v.poly.Reset()
		} else {
			v.graph.Reset()
		}
	case VoicePiano:
		v.piano.Reset()
		_ = v.piano.SetSustain(v.pianoSustain)
	case VoiceDrums:
		v.drums.Reset()
	}
}

func (e *Engine) emit(message cmd.Message) {
	if message.Kind == cmd.Playhead {
		for i := e.messageRead; i != e.messageWrite; i++ {
			if e.messages[i%uint16(len(e.messages))].Kind != cmd.Playhead {
				continue
			}
			for j := i; j != e.messageWrite-1; j++ {
				e.messages[j%uint16(len(e.messages))] = e.messages[(j+1)%uint16(len(e.messages))]
			}
			e.messageWrite--
			break
		}
	}
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
			if e.delayA != nil {
				e.delayA.Reset()
			}
			if e.reverbB != nil {
				e.reverbB.Reset()
			}
			if e.compMusic != nil {
				e.compMusic.Reset()
			}
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
	if e.delayA != nil {
		e.delayA.Reset()
	}
	if e.reverbB != nil {
		e.reverbB.Reset()
	}
	if e.compMusic != nil {
		e.compMusic.Reset()
	}
	e.emit(cmd.Message{Kind: cmd.Fault, Track: 0xff, A: code, Tick: e.transport.Tick()})
}
