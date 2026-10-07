// Package kernelimage carries a complete, validated project into a new
// TinyGo audio module before its render callback starts.
package kernelimage

import (
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
)

// ModalCapability requires the four-strike modeled percussion voice.
const ModalCapability uint16 = 1 << 6

// ModeledKitCapability opts version 13 images into modeled kit lane bindings.
const ModeledKitCapability uint16 = 1 << 8

const Capabilities = ModalCapability | ModeledKitCapability

const SupportedCapabilities = DelayCapability | PianoCapability | ExpressionCapability | NeuralAmpCapability | PMCapability | DDSPCapability | Capabilities

const MaxImageBytes = 2 << 20
const guitarImageVersion = 15  // experimental guitar in the unified image layout
const UnifiedImageVersion = 15 // unified chords, schedules, guitar and resident audio
const ChordImageVersion = UnifiedImageVersion
const qualityImageVersion = 14 // internal graph decoder only; complete image14 is rejected
const masterSoloImageVersion = 13

// Delay images retain the capability bit used by version 13.
const DelayCapability uint16 = 1 << 1

// PianoCapability requires the modeled piano voice and its sustain state.
const PianoCapability uint16 = 1 << 2

// ExpressionCapability enables the per-step expression extension and graph
// expression inputs. Older readers reject the required bit before decoding.
const ExpressionCapability uint16 = 1 << 3

// NeuralAmpCapability requires the pinned causal neural amp operation (28).
const NeuralAmpCapability uint16 = 1 << 4

// PMCapability uses the next capability bit without changing node records.
const PMCapability uint16 = 1 << 5

// DDSPCapability requires the pinned integer harmonic-plus-noise model.
const DDSPCapability uint16 = 1 << 7

// KeysCapability requires the separately loaded keyboard module. The core
// TinyGo module rejects this bit before decoding optional voice records.
const KeysCapability uint16 = 1 << 9
const imageVersion = 13              // built-in bus mute/solo and master mute/solo state
const busMixerImageVersion = 12      // built-in bus mute/solo and master mute
const sendTapImageVersion = 11       // named mixer per-send taps and track solo
const sceneSettingsImageVersion = 10 // scene settings with synced delay divisions
const priorImageVersion = 9          // custom graph glide time
const legacyImageVersion = 8         // SFX bus routing; version 7 added music-bus compressor

type Error string

func (e Error) Error() string { return string(e) }

type writer struct{ data []byte }

func (w *writer) byte(v byte) { w.data = append(w.data, v) }
func (w *writer) u16(v uint16) {
	w.data = append(w.data, byte(v), byte(v>>8))
}
func (w *writer) u32(v uint32) {
	w.data = append(w.data, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
func (w *writer) u64(v uint64) {
	w.u32(uint32(v))
	w.u32(uint32(v >> 32))
}
func (w *writer) f32(v float32) { w.u32(math.Float32bits(v)) }
func (w *writer) f64(v float64) { w.u64(math.Float64bits(v)) }

type reader struct {
	data []byte
	at   int
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.at {
		return nil, Error("truncated project image")
	}
	part := r.data[r.at : r.at+n]
	r.at += n
	return part, nil
}
func (r *reader) byte() (byte, error) {
	part, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return part[0], nil
}
func (r *reader) u16() (uint16, error) {
	part, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return uint16(part[0]) | uint16(part[1])<<8, nil
}
func (r *reader) u32() (uint32, error) {
	part, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return uint32(part[0]) | uint32(part[1])<<8 | uint32(part[2])<<16 | uint32(part[3])<<24, nil
}
func (r *reader) u64() (uint64, error) {
	lo, err := r.u32()
	if err != nil {
		return 0, err
	}
	hi, err := r.u32()
	return uint64(lo) | uint64(hi)<<32, err
}
func (r *reader) f32() (float32, error) {
	bits, err := r.u32()
	return math.Float32frombits(bits), err
}
func (r *reader) f64() (float64, error) {
	bits, err := r.u64()
	return math.Float64frombits(bits), err
}

// Encode writes byte-identical version 13 for legacy projects and the fixed
// version 15 layout for graph polyphony, schedules, and prepared audio. The decoded Config is separately
// validated by engine.New before any audio is produced.
func Encode(cfg engine.Config) ([]byte, error) {
	if cfg.MasterProcessor != nil {
		return nil, Error("prepared master processor must be loaded separately from the project image")
	}
	if cfg.Tracks < 1 || cfg.Tracks > 16 || cfg.MaxVoices < 1 || cfg.MaxVoices > 32 ||
		cfg.SampleRate < 1 || uint64(cfg.SampleRate) > math.MaxUint32 ||
		cfg.BPMMilli < 0 || uint64(cfg.BPMMilli) > math.MaxUint32 ||
		cfg.MaxBlock < 1 || cfg.MaxBlock > 4096 ||
		len(cfg.Scenes) > 65535 || len(cfg.Song) > 65535 ||
		len(cfg.Patterns) != 0 && len(cfg.Patterns) != cfg.Tracks {
		return nil, Error("project image configuration is out of range")
	}
	version := uint16(masterSoloImageVersion)
	for track := 0; track < cfg.Tracks; track++ {
		spec := cfg.Track[track]
		if spec.Kind == engine.VoiceGuitar || keysImageEnabled && spec.Kind == engine.VoiceKeys {
			version = guitarImageVersion
		}
		if spec.Polyphony != 0 && (spec.Polyphony != 4 || spec.Kind != engine.VoiceGraph) {
			return nil, Error("invalid polyphony image mode")
		}
		if version < UnifiedImageVersion && (spec.Kind == engine.VoiceGraphPoly || graphNeedsQuality(&spec.Graph)) {
			version = UnifiedImageVersion
		}
		if spec.Kit != nil {
			for _, binding := range spec.Kit {
				if version < UnifiedImageVersion && graphNeedsQuality(&binding.Program) {
					version = UnifiedImageVersion
				}
			}
		}
		if spec.Polyphony == 4 {
			version = max(version, uint16(ChordImageVersion))
		}
		if len(cfg.Patterns) > 0 {
			for _, pattern := range cfg.Patterns[track].Slots {
				if pattern.Len == 0 && pattern.Chords != [64]seq.ChordStep{} {
					return nil, Error("unused slot contains chord payload")
				}
				for i, chord := range pattern.Chords {
					if chord.Count > 0 && (spec.Polyphony != 4 && spec.Kind != engine.VoicePiano && (!keysImageEnabled || spec.Kind != engine.VoiceKeys) || i >= int(pattern.Len)) {
						return nil, Error("chord payload requires an active polyphonic step")
					}
					if chord.Count > 0 {
						version = max(version, uint16(ChordImageVersion))
					}
				}
				if pattern.Len > 0 {
					if err := pattern.Validate(); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	w := writer{data: make([]byte, 0, 32+cfg.Tracks*4096)}
	w.data = append(w.data, 'C', 'I', 'C', '1')
	if len(cfg.Schedule) > 0 || len(cfg.Clips) > 0 || len(cfg.Assets) > 0 || cfg.MasterBiasL != 0 || cfg.MasterBiasR != 0 {
		version = UnifiedImageVersion
	}
	for _, track := range cfg.Track[:cfg.Tracks] {
		if track.Kind == engine.VoiceAudio || track.Kind == engine.VoiceSample {
			version = UnifiedImageVersion
		}
	}

	if version == UnifiedImageVersion {
		if err := validateUnifiedFields(&cfg); err != nil {
			return nil, err
		}
	}
	w.u16(version)
	w.byte(byte(cfg.Tracks))
	w.byte(byte(cfg.MaxVoices))
	if cfg.LoopSong {
		w.u32(1)
	} else {
		w.u32(0)
	}
	w.u32(uint32(cfg.BPMMilli))
	w.u32(cfg.Seed)
	w.u32(uint32(cfg.SampleRate))
	w.u16(uint16(cfg.MaxBlock))
	w.u16(uint16(len(cfg.Scenes)))
	w.u16(uint16(len(cfg.Song)))
	var capabilities uint16
	for track := 0; track < cfg.Tracks; track++ {
		spec := cfg.Track[track]
		if spec.Kind == engine.VoicePiano {
			capabilities |= PianoCapability
		}
		if keysImageEnabled && spec.Kind == engine.VoiceKeys {
			capabilities |= KeysCapability
		}
		capabilities |= graphCapabilities(&spec.Graph)
		if len(cfg.Patterns) != 0 {
			for slot := range cfg.Patterns[track].Slots {
				pattern := &cfg.Patterns[track].Slots[slot]
				for step := uint8(0); step < pattern.Len && step < 64; step++ {
					if pattern.ExpressionAt(int(step)).Set {
						capabilities |= ExpressionCapability
					}
				}
			}
		}
		if spec.Kit != nil {
			for _, binding := range spec.Kit {
				capabilities |= graphCapabilities(&binding.Program)
			}
		}
		if cfg.Track[track].Kind == engine.VoiceModal {
			capabilities |= ModalCapability
		}
		if kit := cfg.Track[track].Kit; cfg.Track[track].Kind == engine.VoiceDrums && kit != nil {
			for _, binding := range kit {
				if binding.Kind == engine.KitLaneModeled {
					capabilities |= ModeledKitCapability
				}
				if graphNeedsDDSP(&binding.Program) {
					capabilities |= DDSPCapability
				}
			}
		}
	}
	w.u16(capabilities)
	if cfg.DelayA == nil {
		w.byte(0)
	} else {
		if err := cfg.DelayA.ValidateTempo(cfg.BPMMilli); err != nil {
			return nil, err
		}
		w.byte(1)
		w.byte(byte(cfg.DelayA.Division))
		w.f64(cfg.DelayA.TimeMs)
		w.f64(cfg.DelayA.Feedback)
		w.f64(cfg.DelayA.DampHz)
		w.byte(boolByte(cfg.DelayA.PingPong))
		w.f64(cfg.DelayA.Width)
		w.f64(cfg.DelayA.Mix)
	}
	if cfg.ReverbB == nil {
		w.byte(0)
	} else {
		if err := cfg.ReverbB.Validate(); err != nil {
			return nil, err
		}
		w.byte(1)
		w.f64(cfg.ReverbB.Size)
		w.f64(cfg.ReverbB.DecaySec)
		w.f64(cfg.ReverbB.DampHz)
		w.f64(cfg.ReverbB.HighpassHz)
		w.f64(cfg.ReverbB.PredelayMs)
		w.f64(cfg.ReverbB.Mix)
	}
	if cfg.CompMusic == nil {
		if cfg.CompSidechainTrack != 0 {
			return nil, Error("compressor sidechain without compressor")
		}
		w.byte(0)
	} else {
		if err := cfg.CompMusic.Validate(); err != nil {
			return nil, err
		}
		if cfg.CompSidechainTrack < 0 || cfg.CompSidechainTrack > cfg.Tracks && cfg.CompSidechainTrack != engine.SFXSidechain {
			return nil, Error("invalid compressor sidechain track")
		}
		w.byte(1)
		w.byte(byte(cfg.CompMusic.Detect))
		w.f64(cfg.CompMusic.Threshold)
		w.f64(cfg.CompMusic.Ratio)
		w.f64(cfg.CompMusic.Knee)
		w.f64(cfg.CompMusic.AttackMs)
		w.f64(cfg.CompMusic.ReleaseMs)
		w.byte(boolByte(cfg.CompMusic.MakeupAuto))
		w.f64(cfg.CompMusic.MakeupDB)
		w.f64(cfg.CompMusic.Mix)
		w.byte(byte(cfg.CompSidechainTrack))
	}
	var busFlags uint16
	if cfg.MusicBusMute {
		busFlags |= 1 << 0
	}
	if cfg.MusicBusSolo {
		busFlags |= 1 << 1
	}
	if cfg.SFXBusMute {
		busFlags |= 1 << 2
	}
	if cfg.SFXBusSolo {
		busFlags |= 1 << 3
	}
	if cfg.MasterMute {
		busFlags |= 1 << 4
	}
	if cfg.MasterSolo {
		busFlags |= 1 << 5
	}
	w.u16(busFlags)
	for track := 0; track < cfg.Tracks; track++ {
		spec := cfg.Track[track]
		w.byte(byte(spec.Kind))
		if version == UnifiedImageVersion {
			w.byte(spec.Polyphony)
		}
		w.byte(boolByte(spec.Mute))
		w.byte(boolByte(spec.GainSet))
		w.byte(boolByte(spec.BusSFX))
		w.f64(spec.GainDB)
		w.f64(spec.Pan)
		if math.IsNaN(spec.SendA) || math.IsInf(spec.SendA, 0) || spec.SendA < 0 || spec.SendA > 1 || spec.SendA > 0 && cfg.DelayA == nil {
			return nil, Error("invalid track send A")
		}
		w.f64(spec.SendA)
		if math.IsNaN(spec.SendB) || math.IsInf(spec.SendB, 0) || spec.SendB < 0 || spec.SendB > 1 || spec.SendB > 0 && cfg.ReverbB == nil {
			return nil, Error("invalid track send B")
		}
		w.f64(spec.SendB)
		w.byte(boolByte(spec.SendPre))
		w.byte(boolByte(spec.SendAPre))
		w.byte(boolByte(spec.SendBPre))
		w.byte(boolByte(spec.Solo))
		if spec.InsertDrive == nil {
			w.byte(0)
		} else {
			if err := spec.InsertDrive.Validate(); err != nil {
				return nil, err
			}
			w.byte(1)
			w.byte(byte(spec.InsertDrive.Shape))
			w.f64(spec.InsertDrive.GainDB)
			w.f64(spec.InsertDrive.ToneHz)
			w.f64(spec.InsertDrive.Mix)
		}
		switch spec.Kind {
		case engine.VoiceOff, engine.VoiceAudio:
		case engine.VoiceGuitar:
			if !spec.Experimental {
				return nil, Error("guitar requires explicit experimental opt-in")
			}
			if err := spec.Guitar.Validate(); err != nil {
				return nil, err
			}
			for id := kernel.ParamGuitarBend; id <= kernel.ParamGuitarDrive; id++ {
				w.f64(spec.Guitar.Value(id))
			}
		case engine.VoiceAcid:
			writeAcid(&w, spec.Acid)
		case engine.VoiceDrums:
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				writeDrum(&w, spec.Drums[lane])
			}
			w.byte(boolByte(spec.Kit != nil))
			if spec.Kit != nil {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					binding := spec.Kit[lane]
					w.byte(byte(binding.Kind))
					switch binding.Kind {
					case engine.KitLaneOff:
					case engine.KitLaneBuiltin:
						if binding.Recipe >= drum.LaneCount {
							return nil, Error("invalid kit recipe")
						}
						w.byte(byte(binding.Recipe))
					case engine.KitLaneGraph:
						if err := writeGraph(&w, binding.Program); err != nil {
							return nil, err
						}
					case engine.KitLaneModeled:
						if err := validateModeledBinding(binding); err != nil {
							return nil, err
						}
						w.byte(byte(binding.Model))
						w.f64(binding.ModelParams.Tune)
						w.f64(binding.ModelParams.Decay)
						w.f64(binding.ModelParams.Position)
						w.f64(binding.ModelParams.Humanize)
						w.f64(binding.ModelLevelDB)
						w.f64(binding.ModelPan)
					default:
						return nil, Error("invalid kit lane kind")
					}
				}
			}
		case engine.VoiceModal:
			if spec.Modal >= modal.ProfileCount {
				return nil, Error("invalid modal profile")
			}
			w.byte(byte(spec.Modal))
		case engine.VoiceSample:
			if spec.Sample == nil {
				return nil, Error("nil sampler image")
			}
			w.u16(spec.Sample.Asset)
			w.byte(spec.Sample.RootKey)
			w.byte(spec.Sample.Voices)
			w.byte(boolByte(spec.Sample.Loop))
		case engine.VoiceGraph, engine.VoiceGraphPoly:
			if err := writeGraph(&w, spec.Graph); err != nil {
				return nil, err
			}
		case engine.VoicePiano:
			if math.IsNaN(float64(spec.PianoSustain)) || math.IsInf(float64(spec.PianoSustain), 0) || spec.PianoSustain < 0 || spec.PianoSustain > 1 {
				return nil, Error("invalid piano sustain")
			}
			w.f32(spec.PianoSustain)
		case engine.VoiceKeys:
			if !keysImageEnabled {
				return nil, Error("keyboard module is unavailable")
			}
			if err := spec.Keys.Validate(); err != nil {
				return nil, err
			}
			w.byte(spec.Keys.Patch)
			var count uint8
			for _, value := range spec.Keys.Controls {
				if value != 0 {
					count++
				}
			}
			w.byte(count)
			for index, value := range spec.Keys.Controls {
				if value != 0 {
					w.byte(uint8(index))
					w.f32(value)
				}
			}
		default:
			return nil, Error("invalid track voice kind")
		}
		for slot := 0; slot < 16; slot++ {
			var bank engine.PatternBank
			if len(cfg.Patterns) != 0 {
				bank = cfg.Patterns[track]
			}
			pattern := bank.Slots[slot]
			if spec.Kind == engine.VoicePiano && pattern.Expression != nil {
				return nil, Error("piano voices do not support per-note expression")
			}
			if keysImageEnabled && spec.Kind == engine.VoiceKeys && pattern.Expression != nil {
				return nil, Error("keyboard voices do not support per-note expression")
			}
			w.byte(pattern.Len)
			w.u16(pattern.SwingPermille)
			w.byte(byte(pattern.Transpose))
			w.byte(pattern.GatePercent)
			w.u32(pattern.Seed)
			if pattern.Len > 64 {
				return nil, Error("pattern length exceeds 64")
			}
			if spec.Kind == engine.VoiceDrums {
				if pattern.Len > 0 && bank.Drums == nil {
					return nil, Error("missing drum lane bank")
				}
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					for step := uint8(0); step < pattern.Len; step++ {
						w.u32(bank.Drums[slot][lane].Steps[step])
					}
				}
			} else {
				for step := uint8(0); step < pattern.Len; step++ {
					w.u32(pattern.Steps[step])
					if version == UnifiedImageVersion {
						chord := pattern.Chords[step]
						w.byte(chord.Count)
						for _, note := range chord.Notes {
							w.byte(note)
						}
					}
				}
			}
			if capabilities&ExpressionCapability != 0 {
				for step := uint8(0); step < pattern.Len; step++ {
					expression := pattern.ExpressionAt(int(step))
					if err := expression.Validate(); err != nil {
						return nil, err
					}
					w.byte(boolByte(expression.Set))
					w.f32(expression.PitchCents)
					w.f32(expression.Pressure)
					w.f32(expression.Timbre)
					w.f32(expression.VibratoRateHz)
					w.f32(expression.VibratoDepthCents)
				}
			}
		}
	}
	for _, scene := range cfg.Scenes {
		if len(scene.Settings) > int(^uint16(0)) {
			return nil, Error("scene parameter setting count exceeds the image limit")
		}
		for i, setting := range scene.Settings {
			if err := validateSceneSetting(&cfg, setting); err != nil {
				return nil, err
			}
			for prior := 0; prior < i; prior++ {
				if scene.Settings[prior].Track == setting.Track && scene.Settings[prior].ID == setting.ID {
					return nil, Error("duplicate scene parameter setting")
				}
			}
		}
		for _, binding := range scene.Track {
			switch binding.Mode {
			case engine.SceneClip:
				w.byte(255)
				w.u16(binding.Clip)
			case engine.SceneKeep:
				w.byte(0)
			case engine.SceneOff:
				w.byte(1)
			case engine.SceneSlot:
				if binding.Slot >= 16 {
					return nil, Error("invalid scene slot")
				}
				w.byte(binding.Slot + 2)
			default:
				return nil, Error("invalid scene binding")
			}
		}
		w.u16(uint16(len(scene.Settings)))
		for _, setting := range scene.Settings {
			w.byte(setting.Track)
			w.u16(uint16(setting.ID))
			w.f32(setting.Value)
			w.byte(byte(setting.Division))
		}
	}
	for _, entry := range cfg.Song {
		w.u16(entry.Scene)
		w.u16(entry.Bars)
	}
	if version == UnifiedImageVersion {
		if err := writeSchedule(&w, &cfg); err != nil {
			return nil, err
		}
	}
	if len(w.data) > MaxImageBytes {
		return nil, Error("project image exceeds 2 MiB")
	}
	return w.data, nil
}

// Decode accepts exactly one image, with no trailing bytes or unknown flags.
func Decode(data []byte, sampleRate, maxBlock int) (engine.Config, error) {
	var cfg engine.Config
	err := DecodeInto(data, sampleRate, maxBlock, &cfg)
	return cfg, err
}

// DecodeInto avoids copying the large config on each decoder error path.
//
//go:noinline
func DecodeInto(data []byte, sampleRate, maxBlock int, cfg *engine.Config) error {
	if cfg == nil {
		return Error("nil project image destination")
	}
	if len(data) < 32 || len(data) > MaxImageBytes {
		return Error("invalid project image size")
	}
	r := reader{data: data}
	magic, _ := r.take(4)
	if magic[0] != 'C' || magic[1] != 'I' || magic[2] != 'C' || magic[3] != '1' {
		return Error("invalid project image magic")
	}
	version, _ := r.u16()
	if version == 14 {
		return Error("ambiguous development image version 14; recompile project source for version 15")
	}
	tracks, _ := r.byte()
	voices, _ := r.byte()
	flags, _ := r.u32()
	tempo, _ := r.u32()
	seed, _ := r.u32()
	rate, _ := r.u32()
	block, _ := r.u16()
	scenes, _ := r.u16()
	entries, _ := r.u16()
	reserved, _ := r.u16()
	if (version != guitarImageVersion && version != ChordImageVersion && version != imageVersion && version != busMixerImageVersion && version != sendTapImageVersion && version != sceneSettingsImageVersion && version != priorImageVersion && version != legacyImageVersion) || tracks < 1 || tracks > 16 || voices < 1 || voices > 32 || flags&^uint32(1) != 0 || reserved & ^(DelayCapability|PianoCapability|PMCapability|ExpressionCapability|NeuralAmpCapability|ModalCapability|ModeledKitCapability|DDSPCapability|keysImageCapability) != 0 || reserved != 0 && version < imageVersion || rate != uint32(sampleRate) || block != uint16(maxBlock) {
		return Error("project image header is incompatible")
	}
	*cfg = engine.Config{
		SampleRate: sampleRate, MaxBlock: maxBlock, Tracks: int(tracks), MaxVoices: int(voices),
		BPMMilli: int64(tempo), Seed: seed, LoopSong: flags&1 != 0,
		Patterns: make([]engine.PatternBank, int(tracks)),
		Scenes:   make([]engine.Scene, int(scenes)),
		Song:     make([]engine.SongEntry, int(entries)),
	}
	delayPresent, err := r.byte()
	if err != nil || delayPresent > 1 {
		return Error("invalid delay image flag")
	}
	if delayPresent == 1 {
		division, err := r.byte()
		if err != nil {
			return err
		}
		params := &fx.DelayParams{Division: fx.DelayDivision(division)}
		if params.TimeMs, err = r.f64(); err != nil {
			return err
		}
		if params.Feedback, err = r.f64(); err != nil {
			return err
		}
		if params.DampHz, err = r.f64(); err != nil {
			return err
		}
		pingpong, err := r.byte()
		if err != nil || pingpong > 1 {
			return Error("invalid delay pingpong flag")
		}
		params.PingPong = pingpong == 1
		if params.Width, err = r.f64(); err != nil {
			return err
		}
		if params.Mix, err = r.f64(); err != nil {
			return err
		}
		if err := params.ValidateTempo(int64(tempo)); err != nil {
			return err
		}
		cfg.DelayA = params
	}
	reverbPresent, err := r.byte()
	if err != nil || reverbPresent > 1 {
		return Error("invalid reverb image flag")
	}
	if reverbPresent == 1 {
		params := &fx.ReverbParams{}
		if params.Size, err = r.f64(); err != nil {
			return err
		}
		if params.DecaySec, err = r.f64(); err != nil {
			return err
		}
		if params.DampHz, err = r.f64(); err != nil {
			return err
		}
		if params.HighpassHz, err = r.f64(); err != nil {
			return err
		}
		if params.PredelayMs, err = r.f64(); err != nil {
			return err
		}
		if params.Mix, err = r.f64(); err != nil {
			return err
		}
		if err := params.Validate(); err != nil {
			return err
		}
		cfg.ReverbB = params
	}
	compPresent, err := r.byte()
	if err != nil || compPresent > 1 {
		return Error("invalid compressor image flag")
	}
	if compPresent == 1 {
		detect, err := r.byte()
		if err != nil {
			return err
		}
		params := &fx.CompParams{Detect: fx.CompDetector(detect)}
		if params.Threshold, err = r.f64(); err != nil {
			return err
		}
		if params.Ratio, err = r.f64(); err != nil {
			return err
		}
		if params.Knee, err = r.f64(); err != nil {
			return err
		}
		if params.AttackMs, err = r.f64(); err != nil {
			return err
		}
		if params.ReleaseMs, err = r.f64(); err != nil {
			return err
		}
		makeupAuto, err := r.byte()
		if err != nil || makeupAuto > 1 {
			return Error("invalid compressor makeup flag")
		}
		params.MakeupAuto = makeupAuto == 1
		if params.MakeupDB, err = r.f64(); err != nil {
			return err
		}
		if params.Mix, err = r.f64(); err != nil {
			return err
		}
		sidechain, err := r.byte()
		if err != nil || int(sidechain) > int(tracks) && int(sidechain) != engine.SFXSidechain {
			return Error("invalid compressor sidechain track")
		}
		if err := params.Validate(); err != nil {
			return err
		}
		cfg.CompMusic, cfg.CompSidechainTrack = params, int(sidechain)
	}
	if version >= busMixerImageVersion {
		busFlags, err := r.u16()
		allowed := uint16(0x1f)
		if version >= masterSoloImageVersion {
			allowed = 0x3f
		}
		if err != nil || busFlags&^allowed != 0 {
			return Error("invalid bus mixer image flags")
		}
		cfg.MusicBusMute, cfg.MusicBusSolo = busFlags&1 != 0, busFlags&2 != 0
		cfg.SFXBusMute, cfg.SFXBusSolo = busFlags&4 != 0, busFlags&8 != 0
		cfg.MasterMute = busFlags&16 != 0
		cfg.MasterSolo = busFlags&32 != 0
	}
	for track := 0; track < int(tracks); track++ {
		spec := &cfg.Track[track]
		kind, err := r.byte()
		if err != nil {
			return err
		}
		if version == UnifiedImageVersion {
			mode, err := r.byte()
			if err != nil || mode != 0 && (mode != 4 || engine.VoiceKind(kind) != engine.VoiceGraph) {
				return Error("invalid polyphony image mode")
			}
			spec.Polyphony = mode
		}
		mute, err := r.byte()
		if err != nil {
			return err
		}
		gainSet, err := r.byte()
		if err != nil {
			return err
		}
		busSFX, err := r.byte()
		if err != nil || mute > 1 || gainSet > 1 || busSFX > 1 {
			return Error("invalid track image header")
		}
		spec.Kind, spec.Mute, spec.GainSet, spec.BusSFX = engine.VoiceKind(kind), mute == 1, gainSet == 1, busSFX == 1
		if spec.GainDB, err = r.f64(); err != nil {
			return err
		}
		if spec.Pan, err = r.f64(); err != nil {
			return err
		}
		if spec.SendA, err = r.f64(); err != nil {
			return err
		}
		if spec.SendB, err = r.f64(); err != nil {
			return err
		}
		sendPre, err := r.byte()
		if err != nil || sendPre > 1 {
			return Error("invalid track send-pre flag")
		}
		spec.SendPre = sendPre == 1
		if version >= sendTapImageVersion {
			sendAPre, err := r.byte()
			if err != nil {
				return err
			}
			sendBPre, err := r.byte()
			if err != nil {
				return err
			}
			solo, err := r.byte()
			if err != nil || sendAPre > 1 || sendBPre > 1 || solo > 1 {
				return Error("invalid P2 track mixer flags")
			}
			spec.SendAPre, spec.SendBPre, spec.Solo = sendAPre == 1, sendBPre == 1, solo == 1
		}
		insertPresent, err := r.byte()
		if err != nil || insertPresent > 1 {
			return Error("invalid drive insert image flag")
		}
		if insertPresent == 1 {
			shape, err := r.byte()
			if err != nil {
				return err
			}
			params := &fx.DriveParams{Shape: fx.DriveShape(shape)}
			if params.GainDB, err = r.f64(); err != nil {
				return err
			}
			if params.ToneHz, err = r.f64(); err != nil {
				return err
			}
			if params.Mix, err = r.f64(); err != nil {
				return err
			}
			if err := params.Validate(); err != nil {
				return err
			}
			spec.InsertDrive = params
		}
		switch spec.Kind {
		case engine.VoiceOff:
		case engine.VoiceGuitar:
			if version != guitarImageVersion {
				return Error("guitar requires image version 15")
			}
			spec.Experimental = true
			spec.Guitar = guitar.DefaultParams()
			for id := kernel.ParamGuitarBend; id <= kernel.ParamGuitarDrive; id++ {
				value, err := r.f64()
				if err != nil {
					return err
				}
				if err := spec.Guitar.Set(id, value); err != nil {
					return err
				}
			}
		case engine.VoiceAudio:
			if version != UnifiedImageVersion {
				return Error("unsupported audio track image")
			}
		case engine.VoiceAcid:
			if spec.Acid, err = readAcid(&r); err != nil {
				return err
			}
		case engine.VoiceDrums:
			cfg.Patterns[track].Drums = new([16][drum.LaneCount]seq.Pattern)
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				if spec.Drums[lane], err = readDrum(&r); err != nil {
					return err
				}
			}
			kitPresent, err := r.byte()
			if err != nil || kitPresent > 1 {
				return Error("invalid kit image flag")
			}
			if kitPresent == 1 {
				spec.Kit = new([drum.LaneCount]engine.KitLaneBinding)
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					kind, err := r.byte()
					if err != nil {
						return err
					}
					binding := &spec.Kit[lane]
					binding.Kind = engine.KitLaneKind(kind)
					switch binding.Kind {
					case engine.KitLaneOff:
					case engine.KitLaneBuiltin:
						recipe, err := r.byte()
						if err != nil || recipe >= byte(drum.LaneCount) {
							return Error("invalid kit recipe image")
						}
						binding.Recipe = drum.Lane(recipe)
					case engine.KitLaneGraph:
						if err = readGraphInto(&r, version, reserved, &binding.Program); err != nil {
							return err
						}
					case engine.KitLaneModeled:
						if reserved&ModeledKitCapability == 0 {
							return Error("modeled kit image requires capability")
						}
						profile, err := r.byte()
						if err != nil {
							return err
						}
						binding.Model = modeledkit.Profile(profile)
						for _, value := range []*float64{&binding.ModelParams.Tune, &binding.ModelParams.Decay, &binding.ModelParams.Position, &binding.ModelParams.Humanize, &binding.ModelLevelDB, &binding.ModelPan} {
							if *value, err = r.f64(); err != nil {
								return err
							}
						}
						if err := validateModeledBinding(*binding); err != nil {
							return err
						}
					default:
						return Error("invalid kit lane image kind")
					}
				}
			}
		case engine.VoiceModal:
			if reserved&ModalCapability == 0 {
				return Error("modal image requires capability")
			}
			profile, readErr := r.byte()
			if readErr != nil || profile >= byte(modal.ProfileCount) {
				return Error("invalid modal profile image")
			}
			spec.Modal = modal.Profile(profile)
		case engine.VoiceSample:
			if version != UnifiedImageVersion {
				return Error("unsupported sampler image")
			}
			spec.Sample = &engine.SamplerConfig{}
			spec.Sample.Asset, err = r.u16()
			if err != nil {
				return err
			}
			spec.Sample.RootKey, err = r.byte()
			if err != nil {
				return err
			}
			spec.Sample.Voices, err = r.byte()
			if err != nil {
				return err
			}
			loop, err := r.byte()
			if err != nil || loop > 1 {
				return Error("invalid sampler image loop")
			}
			spec.Sample.Loop = loop == 1
		case engine.VoiceGraph, engine.VoiceGraphPoly:
			if err = readGraphInto(&r, version, reserved, &spec.Graph); err != nil {
				return err
			}
		case engine.VoicePiano:
			if reserved&PianoCapability == 0 {
				return Error("modeled piano requires capability bit 2")
			}
			if spec.PianoSustain, err = r.f32(); err != nil {
				return err
			}
			if math.IsNaN(float64(spec.PianoSustain)) || math.IsInf(float64(spec.PianoSustain), 0) || spec.PianoSustain < 0 || spec.PianoSustain > 1 {
				return Error("invalid piano sustain")
			}
		case engine.VoiceKeys:
			if !keysImageEnabled || reserved&KeysCapability == 0 || version != UnifiedImageVersion {
				return Error("keyboard voice requires version 15 and capability bit 9")
			}
			spec.Keys = new(keyboard.Spec)
			if spec.Keys.Patch, err = r.byte(); err != nil {
				return err
			}
			count, err := r.byte()
			if err != nil || count > 128 {
				return Error("invalid keyboard control count")
			}
			previous := -1
			for i := uint8(0); i < count; i++ {
				index, err := r.byte()
				if err != nil || index >= 128 || int(index) <= previous {
					return Error("invalid keyboard control index")
				}
				value, err := r.f32()
				if err != nil {
					return err
				}
				if value == 0 {
					return Error("keyboard payload contains a zero control")
				}
				spec.Keys.Controls[index] = value
				previous = int(index)
			}
			if err := spec.Keys.Validate(); err != nil {
				return err
			}
		default:
			return Error("invalid track image kind")
		}
		for slot := 0; slot < 16; slot++ {
			length, err := r.byte()
			if err != nil || length > 64 {
				return Error("invalid pattern image length")
			}
			swing, err := r.u16()
			if err != nil {
				return err
			}
			transpose, err := r.byte()
			if err != nil {
				return err
			}
			gate, err := r.byte()
			if err != nil {
				return err
			}
			seed, err := r.u32()
			if err != nil {
				return err
			}
			// Decode directly into fresh destination storage. Copying a full
			// fixed chord pattern makes TinyGo expand hundreds of scalar loads
			// and stores, without adding ownership or validation guarantees.
			pattern := &cfg.Patterns[track].Slots[slot]
			pattern.Len, pattern.SwingPermille = length, swing
			pattern.Transpose, pattern.GatePercent, pattern.Seed = int8(transpose), gate, seed
			if spec.Kind == engine.VoiceDrums {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					compiled := &cfg.Patterns[track].Drums[slot][lane]
					compiled.Len, compiled.SwingPermille = length, swing
					compiled.Transpose, compiled.GatePercent, compiled.Seed = int8(transpose), gate, seed
					for step := uint8(0); step < length; step++ {
						if compiled.Steps[step], err = r.u32(); err != nil {
							return err
						}
					}
				}
			} else {
				for step := uint8(0); step < length; step++ {
					if pattern.Steps[step], err = r.u32(); err != nil {
						return err
					}
					if version == UnifiedImageVersion {
						chord := &pattern.Chords[step]
						if chord.Count, err = r.byte(); err != nil {
							return err
						}
						for i := range chord.Notes {
							if chord.Notes[i], err = r.byte(); err != nil {
								return err
							}
						}
					}

				}
			}
			if reserved&ExpressionCapability != 0 {
				for step := uint8(0); step < length; step++ {
					expression, err := readExpression(&r)
					if err != nil {
						return err
					}
					if expression != (seq.Expression{}) {
						if pattern.Expression == nil {
							pattern.Expression = new([64]seq.Expression)
						}
						pattern.Expression[step] = expression
					}
				}
			}
			if keysImageEnabled && spec.Kind == engine.VoiceKeys && pattern.Expression != nil {
				return Error("keyboard voices do not support per-note expression")
			}
		}
	}
	for scene := range cfg.Scenes {
		for track := range cfg.Scenes[scene].Track {
			binding, err := r.byte()
			if err != nil {
				return err
			}
			switch {
			case binding == 255 && version == UnifiedImageVersion:
				clip, err := r.u16()
				if err != nil {
					return err
				}
				cfg.Scenes[scene].Track[track] = engine.SceneBinding{Mode: engine.SceneClip, Clip: clip}
			case binding == 0:
			case binding == 1:
				cfg.Scenes[scene].Track[track].Mode = engine.SceneOff
			case binding <= 17:
				cfg.Scenes[scene].Track[track] = engine.SceneBinding{Mode: engine.SceneSlot, Slot: binding - 2}
			default:
				return Error("invalid scene image binding")
			}
		}
		if version >= sceneSettingsImageVersion {
			count, err := r.u16()
			if err != nil {
				return err
			}
			cfg.Scenes[scene].Settings = make([]engine.SceneSetting, int(count))
			for i := range cfg.Scenes[scene].Settings {
				setting := &cfg.Scenes[scene].Settings[i]
				if setting.Track, err = r.byte(); err != nil {
					return err
				}
				id, err := r.u16()
				if err != nil {
					return err
				}
				setting.ID = kernel.ParamID(id)
				if setting.Value, err = r.f32(); err != nil {
					return err
				}
				division, err := r.byte()
				if err != nil {
					return err
				}
				setting.Division = fx.DelayDivision(division)
				if version != guitarImageVersion && setting.ID >= kernel.ParamGuitarBend {
					return Error("guitar controls require image version 15")
				}
				if err := validateSceneSetting(cfg, *setting); err != nil {
					return err
				}
			}
		}
	}
	for i := range cfg.Song {
		var err error
		if cfg.Song[i].Scene, err = r.u16(); err != nil {
			return err
		}
		if cfg.Song[i].Bars, err = r.u16(); err != nil {
			return err
		}
	}
	if version == UnifiedImageVersion {
		if err := readSchedule(&r, cfg); err != nil {
			return err
		}
	}
	if r.at != len(r.data) {
		return Error("project image has trailing bytes")
	}
	if version == UnifiedImageVersion {
		return validateUnifiedFields(cfg)
	}
	return nil
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
}

func writeGraph(w *writer, program graph.Program) error {
	if program.Len == 0 || program.Len > graph.MaxNodes || program.Output >= program.Len {
		return Error("invalid graph program length or output")
	}
	if math.IsNaN(program.GlideMS) || math.IsInf(program.GlideMS, 0) || program.GlideMS < 0 {
		return Error("invalid graph glide time")
	}
	w.byte(program.Len)
	w.byte(program.Output)
	w.f64(program.GlideMS)
	for i := uint8(0); i < program.Len; i++ {
		node := program.Nodes[i]
		w.byte(byte(node.Op))
		w.byte(node.A)
		w.byte(node.B)
		w.byte(node.C)
		w.f32(node.Value)
		if node.Op == graph.ADSR {
			w.byte(node.D)
			w.byte(node.E)
		}
	}
	return nil
}

// Inspect by pointer: a Program contains the full 128-node array. Copying it
// for each capability check expands the WASM reader without adding behavior.
func graphNeedsQuality(program *graph.Program) bool {
	for i := 0; i < int(program.Len) && i < graph.MaxNodes; i++ {
		switch program.Nodes[i].Op {
		case graph.ADSR, graph.Pulse, graph.SVF:
			return true
		}
	}
	return false
}

func graphCapabilities(program *graph.Program) uint16 {
	var required uint16
	for i := 0; i < int(program.Len) && i < graph.MaxNodes; i++ {
		switch program.Nodes[i].Op {
		case graph.Delay, graph.Comb, graph.Period:
			required |= DelayCapability
		case graph.PM:
			required |= PMCapability
		case graph.PitchBend, graph.Pressure, graph.Timbre:
			required |= ExpressionCapability
		case graph.DDSP:
			required |= DDSPCapability
		case graph.NeuralAmp:
			required |= NeuralAmpCapability
		}
	}
	return required
}

func graphNeedsNeuralAmp(program *graph.Program) bool {
	for i := 0; i < int(program.Len) && i < graph.MaxNodes; i++ {
		if program.Nodes[i].Op == graph.NeuralAmp {
			return true
		}
	}
	return false
}

func graphNeedsDDSP(program *graph.Program) bool {
	for i := 0; i < int(program.Len) && i < graph.MaxNodes; i++ {
		if program.Nodes[i].Op == graph.DDSP {
			return true
		}
	}
	return false
}

func readGraph(r *reader, version uint16, capabilities uint16) (graph.Program, error) {
	var program graph.Program
	err := readGraphInto(r, version, capabilities, &program)
	return program, err
}

func readGraphInto(r *reader, version uint16, capabilities uint16, program *graph.Program) error {
	length, err := r.byte()
	if err != nil || length == 0 || length > graph.MaxNodes {
		return Error("invalid graph image length")
	}
	program.Len = length
	if program.Output, err = r.byte(); err != nil || program.Output >= length {
		return Error("invalid graph image output")
	}
	if version >= priorImageVersion {
		if program.GlideMS, err = r.f64(); err != nil || math.IsNaN(program.GlideMS) || math.IsInf(program.GlideMS, 0) || program.GlideMS < 0 {
			return Error("invalid graph glide time image")
		}
	}
	for i := uint8(0); i < length; i++ {
		op, err := r.byte()
		if err != nil {
			return err
		}
		a, err := r.byte()
		if err != nil {
			return err
		}
		b, err := r.byte()
		if err != nil {
			return err
		}
		c, err := r.byte()
		if err != nil {
			return err
		}
		value, err := r.f32()
		if err != nil {
			return err
		}
		// Decode early graph payloads for migration tools; DecodeInto still refuses
		// both ambiguous complete version-14 image layouts before reading tracks.
		if version == qualityImageVersion && capabilities == 0 {
			if op == 24 {
				op = byte(graph.Pulse)
			} else if op == 25 {
				op = byte(graph.SVF)
			}
		}
		program.Nodes[i] = graph.Node{Op: graph.Op(op), A: a, B: b, C: c, Value: value}
		if graph.Op(op) == graph.ADSR {
			if version < qualityImageVersion {
				return Error("ADSR needs project image version 15")
			}
			if program.Nodes[i].D, err = r.byte(); err != nil {
				return err
			}
			if program.Nodes[i].E, err = r.byte(); err != nil {
				return err
			}
		}
	}
	if graphNeedsExpression(program) && capabilities&ExpressionCapability == 0 {
		return Error("graph expression inputs require capability bit 3")
	}
	required := graphCapabilities(program)
	if required&DelayCapability != 0 && capabilities&DelayCapability == 0 {
		return Error("graph delay operations require capability bit 1")
	}
	if graphNeedsNeuralAmp(program) && capabilities&NeuralAmpCapability == 0 {
		return Error("neural amp operation requires capability bit 4")
	}
	if required&PMCapability != 0 && capabilities&PMCapability == 0 {
		return Error("graph phase modulation requires capability bit 5")
	}
	if graphNeedsDDSP(program) && capabilities&DDSPCapability == 0 {
		return Error("DDSP operations require capability bit 7")
	}
	return nil
}

func validateSceneSetting(cfg *engine.Config, setting engine.SceneSetting) error {
	spec, ok := kernel.Param(setting.ID)
	if !ok || !spec.Live {
		return Error("scene parameter setting is not live")
	}
	if spec.Scope == "track" {
		if int(setting.Track) >= cfg.Tracks {
			return Error("scene parameter track is out of range")
		}
		if setting.ID >= kernel.ParamGuitarBend && setting.ID <= kernel.ParamGuitarDrive && cfg.Track[setting.Track].Kind != engine.VoiceGuitar {
			return Error("guitar setting requires a guitar track")
		}
	} else if setting.Track != 0xff {
		return Error("global scene parameter needs the global owner")
	}
	syncedDelay := setting.Division != fx.FreeDelay
	if syncedDelay {
		if setting.ID != kernel.ParamFxDelayTime || cfg.DelayA == nil || setting.Division.String() == "invalid" || setting.Value != 0 {
			return Error("scene delay division is invalid")
		}
		params := *cfg.DelayA
		params.Division, params.TimeMs = setting.Division, 0
		if err := params.ValidateTempo(cfg.BPMMilli); err != nil {
			return err
		}
	}
	off := spec.Off && math.IsInf(float64(setting.Value), -1)
	if math.IsNaN(float64(setting.Value)) || math.IsInf(float64(setting.Value), 0) && !off || !syncedDelay && !off && (setting.Value < spec.Min || setting.Value > spec.Max) {
		return Error("scene parameter value is out of range")
	}
	if !syncedDelay && spec.Curve == "toggle" && setting.Value != 0 && setting.Value != 1 {
		return Error("scene toggle value is out of range")
	}
	return nil
}

func writeAcid(w *writer, p acid.Params) {
	for _, value := range [...]float64{p.Tune, p.Fine, p.Wave, p.PulseWidth, p.Detune, p.Sub, p.Cutoff, p.Resonance, p.EnvMod, p.Decay, p.Accent, p.Drive, p.Release, p.Slide, p.Gate, p.LevelDB} {
		w.f64(value)
	}
	w.byte(byte(p.Filter))
	w.byte(boolByte(p.Savage))
}
func readAcid(r *reader) (acid.Params, error) {
	var p acid.Params
	values := [...]*float64{&p.Tune, &p.Fine, &p.Wave, &p.PulseWidth, &p.Detune, &p.Sub, &p.Cutoff, &p.Resonance, &p.EnvMod, &p.Decay, &p.Accent, &p.Drive, &p.Release, &p.Slide, &p.Gate, &p.LevelDB}
	for _, dst := range values {
		value, err := r.f64()
		if err != nil {
			return p, err
		}
		*dst = value
	}
	filter, err := r.byte()
	if err != nil {
		return p, err
	}
	savage, err := r.byte()
	if err != nil || savage > 1 {
		return p, Error("invalid acid image flags")
	}
	p.Filter, p.Savage = acid.FilterModel(filter), savage == 1
	return p, nil
}
func writeDrum(w *writer, p drum.Params) {
	for _, value := range [...]float64{p.Tune, p.Decay, p.Sweep, p.SweepTime, p.Click, p.Drive, p.Tone, p.Mix, p.Snappy, p.Spread, p.LevelDB, p.Pan} {
		w.f64(value)
	}
	w.byte(boolByte(p.Metal))
}
func readDrum(r *reader) (drum.Params, error) {
	var p drum.Params
	values := [...]*float64{&p.Tune, &p.Decay, &p.Sweep, &p.SweepTime, &p.Click, &p.Drive, &p.Tone, &p.Mix, &p.Snappy, &p.Spread, &p.LevelDB, &p.Pan}
	for _, dst := range values {
		value, err := r.f64()
		if err != nil {
			return p, err
		}
		*dst = value
	}
	metal, err := r.byte()
	if err != nil || metal > 1 {
		return p, Error("invalid drum image flags")
	}
	p.Metal = metal == 1
	return p, nil
}

func validateModeledBinding(binding engine.KitLaneBinding) error {
	if binding.Model >= modeledkit.ProfileCount {
		return Error("invalid modeled kit profile")
	}
	if err := binding.ModelParams.Validate(); err != nil {
		return err
	}
	if math.IsNaN(binding.ModelLevelDB) || math.IsInf(binding.ModelLevelDB, 0) ||
		(binding.ModelLevelDB != -1000 && binding.ModelLevelDB < -60) || binding.ModelLevelDB > 6 ||
		math.IsNaN(binding.ModelPan) || math.IsInf(binding.ModelPan, 0) || binding.ModelPan < -1 || binding.ModelPan > 1 {
		return Error("invalid modeled kit mixer controls")
	}
	return nil
}

func graphNeedsExpression(program *graph.Program) bool {
	for i := 0; i < int(program.Len) && i < graph.MaxNodes; i++ {
		switch program.Nodes[i].Op {
		case graph.PitchBend, graph.Pressure, graph.Timbre:
			return true
		}
	}
	return false
}

// readExpression validates a complete record once before reading its floats.
// Keeping the fixed wire record together avoids repeated bounds/error paths.
func readExpression(r *reader) (seq.Expression, error) {
	var expression seq.Expression
	record, err := r.take(21)
	if err != nil {
		return expression, err
	}
	if record[0] > 1 {
		return expression, Error("invalid note expression presence")
	}
	expression.Set = record[0] == 1
	values := [...]*float32{&expression.PitchCents, &expression.Pressure, &expression.Timbre, &expression.VibratoRateHz, &expression.VibratoDepthCents}
	for i, value := range values {
		offset := 1 + i*4
		bits := uint32(record[offset]) | uint32(record[offset+1])<<8 | uint32(record[offset+2])<<16 | uint32(record[offset+3])<<24
		*value = math.Float32frombits(bits)
	}
	return expression, expression.Validate()
}
