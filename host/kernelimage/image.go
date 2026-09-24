// Package kernelimage carries a complete, validated project into a new
// TinyGo audio module before its render callback starts.
package kernelimage

import (
	"math"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/drum"
)

const MaxImageBytes = 2 << 20
const imageVersion = 2 // eleven drum lanes; version 1 carried six

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

// Encode writes project image version 2. The decoded Config is separately
// validated by engine.New before any audio is produced.
func Encode(cfg engine.Config) ([]byte, error) {
	if cfg.Tracks < 1 || cfg.Tracks > 16 || cfg.MaxVoices < 1 || cfg.MaxVoices > 32 ||
		cfg.SampleRate < 1 || uint64(cfg.SampleRate) > math.MaxUint32 ||
		cfg.BPMMilli < 0 || uint64(cfg.BPMMilli) > math.MaxUint32 ||
		cfg.MaxBlock < 1 || cfg.MaxBlock > 4096 ||
		len(cfg.Scenes) > 65535 || len(cfg.Song) > 65535 ||
		len(cfg.Patterns) != 0 && len(cfg.Patterns) != cfg.Tracks {
		return nil, Error("project image configuration is out of range")
	}
	w := writer{data: make([]byte, 0, 32+cfg.Tracks*4096)}
	w.data = append(w.data, 'C', 'I', 'C', '1')
	w.u16(imageVersion)
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
	w.u16(0)
	for track := 0; track < cfg.Tracks; track++ {
		spec := cfg.Track[track]
		w.byte(byte(spec.Kind))
		w.byte(boolByte(spec.Mute))
		w.byte(boolByte(spec.GainSet))
		w.byte(0)
		w.f64(spec.GainDB)
		w.f64(spec.Pan)
		switch spec.Kind {
		case engine.VoiceOff:
		case engine.VoiceAcid:
			writeAcid(&w, spec.Acid)
		case engine.VoiceDrums:
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				writeDrum(&w, spec.Drums[lane])
			}
		case engine.VoiceGraph:
			if spec.Graph.Len == 0 || spec.Graph.Len > graph.MaxNodes {
				return nil, Error("invalid graph program length")
			}
			w.byte(spec.Graph.Len)
			w.byte(spec.Graph.Output)
			for i := uint8(0); i < spec.Graph.Len; i++ {
				node := spec.Graph.Nodes[i]
				w.byte(byte(node.Op))
				w.byte(node.A)
				w.byte(node.B)
				w.byte(node.C)
				w.f32(node.Value)
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
				}
			}
		}
	}
	for _, scene := range cfg.Scenes {
		for _, binding := range scene.Track {
			switch binding.Mode {
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
	}
	for _, entry := range cfg.Song {
		w.u16(entry.Scene)
		w.u16(entry.Bars)
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
	if version != imageVersion || tracks < 1 || tracks > 16 || voices < 1 || voices > 32 || flags&^uint32(1) != 0 || reserved != 0 || rate != uint32(sampleRate) || block != uint16(maxBlock) {
		return Error("project image header is incompatible")
	}
	*cfg = engine.Config{
		SampleRate: sampleRate, MaxBlock: maxBlock, Tracks: int(tracks), MaxVoices: int(voices),
		BPMMilli: int64(tempo), Seed: seed, LoopSong: flags&1 != 0,
		Patterns: make([]engine.PatternBank, int(tracks)),
		Scenes:   make([]engine.Scene, int(scenes)),
		Song:     make([]engine.SongEntry, int(entries)),
	}
	for track := 0; track < int(tracks); track++ {
		spec := &cfg.Track[track]
		kind, err := r.byte()
		if err != nil {
			return err
		}
		mute, err := r.byte()
		if err != nil {
			return err
		}
		gainSet, err := r.byte()
		if err != nil {
			return err
		}
		padding, err := r.byte()
		if err != nil || mute > 1 || gainSet > 1 || padding != 0 {
			return Error("invalid track image header")
		}
		spec.Kind, spec.Mute, spec.GainSet = engine.VoiceKind(kind), mute == 1, gainSet == 1
		if spec.GainDB, err = r.f64(); err != nil {
			return err
		}
		if spec.Pan, err = r.f64(); err != nil {
			return err
		}
		switch spec.Kind {
		case engine.VoiceOff:
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
		case engine.VoiceGraph:
			length, err := r.byte()
			if err != nil || length == 0 || length > graph.MaxNodes {
				return Error("invalid graph image length")
			}
			spec.Graph.Len = length
			if spec.Graph.Output, err = r.byte(); err != nil {
				return err
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
				spec.Graph.Nodes[i] = graph.Node{Op: graph.Op(op), A: a, B: b, C: c, Value: value}
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
			pattern := seq.Pattern{Len: length, SwingPermille: swing, Transpose: int8(transpose), GatePercent: gate, Seed: seed}
			if spec.Kind == engine.VoiceDrums {
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					compiled := pattern
					for step := uint8(0); step < length; step++ {
						if compiled.Steps[step], err = r.u32(); err != nil {
							return err
						}
					}
					cfg.Patterns[track].Drums[slot][lane] = compiled
				}
			} else {
				for step := uint8(0); step < length; step++ {
					if pattern.Steps[step], err = r.u32(); err != nil {
						return err
					}
				}
			}
			cfg.Patterns[track].Slots[slot] = pattern
		}
	}
	for scene := range cfg.Scenes {
		for track := range cfg.Scenes[scene].Track {
			binding, err := r.byte()
			if err != nil {
				return err
			}
			switch {
			case binding == 0:
			case binding == 1:
				cfg.Scenes[scene].Track[track].Mode = engine.SceneOff
			case binding <= 17:
				cfg.Scenes[scene].Track[track] = engine.SceneBinding{Mode: engine.SceneSlot, Slot: binding - 2}
			default:
				return Error("invalid scene image binding")
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
	if r.at != len(r.data) {
		return Error("project image has trailing bytes")
	}
	return nil
}

func boolByte(value bool) byte {
	if value {
		return 1
	}
	return 0
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
