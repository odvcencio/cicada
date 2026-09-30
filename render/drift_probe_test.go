package render

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

var driftExamples = []string{"acid-voice", "authored-kit", "cicada-chorus", "circuit-kit", "drums-kit", "first-acid", "fx-bus", "glassbass", "sfx-bus"}

// Replay drum events through the engine's public command ABI. Only the chance
// hash's slot argument differs between the legacy and normal probes. Voices,
// note-on parameters, timing, hat choking, routing and effects remain native.
func driftDrumCommands(t *testing.T, cfg engine.Config, bars int, legacy bool) (engine.Config, []cmd.Command) {
	t.Helper()
	cfg.Patterns = append([]engine.PatternBank(nil), cfg.Patterns...)
	clock, err := seq.NewClock(cfg.SampleRate, cfg.BPMMilli)
	if err != nil {
		t.Fatal(err)
	}
	var commands []cmd.Command
	var scratch [128]seq.Event
	var active [16]int
	for i := range active {
		active[i] = -1
	}
	bar := 0
	for _, entry := range cfg.Song {
		for i, binding := range cfg.Scenes[entry.Scene].Track[:cfg.Tracks] {
			if cfg.Track[i].Kind != engine.VoiceDrums {
				continue
			}
			switch binding.Mode {
			case engine.SceneSlot:
				active[i] = int(binding.Slot)
			case engine.SceneOff:
				active[i] = -1
			}
		}
		for repeat := 0; repeat < int(entry.Bars) && bar < bars; repeat++ {
			start := clock.SampleAtTick(int64(bar) * seq.TicksPerBar)
			end := clock.SampleAtTick(int64(bar+1) * seq.TicksPerBar)
			for i, slot := range active[:cfg.Tracks] {
				if slot < 0 || cfg.Track[i].Kind != engine.VoiceDrums {
					continue
				}
				for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
					key := uint8(slot)
					if legacy {
						key = uint8(lane)
					}
					p := &cfg.Patterns[i].Drums[slot][lane]
					n, overflow := seq.EventsInBlock(p, clock, uint8(i), key, start, int(end-start), scratch[:])
					if overflow {
						t.Fatal("probe events overflow")
					}
					for _, event := range scratch[:n] {
						arg := uint32(event.Note) | uint32(event.Velocity)<<8
						if event.Accent {
							arg |= 1 << 16
						}
						commands = append(commands, cmd.Command{Op: cmd.OpNoteOn, Track: uint8(i), Index: uint16(lane), Arg0: arg, Tick: event.Tick})
					}
				}
			}
			bar++
		}
	}
	sort.SliceStable(commands, func(i, j int) bool {
		a, b := commands[i], commands[j]
		if a.Tick != b.Tick {
			return a.Tick < b.Tick
		}
		if a.Track != b.Track {
			return a.Track < b.Track
		}
		priority := func(lane uint16) uint16 {
			if lane == uint16(drum.CH) {
				return uint16(drum.OH)
			}
			if lane == uint16(drum.OH) {
				return uint16(drum.CH)
			}
			return lane
		}
		return priority(a.Index) < priority(b.Index)
	})
	for i := 0; i < cfg.Tracks; i++ {
		if cfg.Track[i].Kind != engine.VoiceDrums {
			continue
		}
		patterns := *cfg.Patterns[i].Drums
		for slot := range patterns {
			for lane := range patterns[slot] {
				clear(patterns[slot][lane].Steps[:])
			}
		}
		cfg.Patterns[i].Drums = &patterns
	}
	return cfg, commands
}

func driftWriteWAV(t *testing.T, path string, audio driftAudio, score *notation.Score, bars, bits int, tail float64) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder, err := newWAVEncoder(bits, false, score.Seed, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWAVHeader(file, 48000, bits, uint32(len(audio.left)*encoder.frameBytes())); err != nil {
		t.Fatal(err)
	}
	var report Report
	var buffer [4096 * 8]byte
	for at := 0; at < len(audio.left); {
		n := min(4096, len(audio.left)-at)
		for i := range n {
			encoder.writeFrame(buffer[i*encoder.frameBytes():], audio.left[at+i], audio.right[at+i], math.Pow(10, mix.CeilingDB/20), &report)
		}
		if _, err := file.Write(buffer[:n*encoder.frameBytes()]); err != nil {
			t.Fatal(err)
		}
		at += n
	}
	if err := writeMetadata(file, uint32(score.TempoMilli), uint32(bars), uint32(math.Ceil(tail*48000))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func driftReadWAV(t *testing.T, path string) driftAudio {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 68 || binary.LittleEndian.Uint16(data[34:36]) != 32 {
		t.Fatal("probe requires float WAV")
	}
	n := int(binary.LittleEndian.Uint32(data[40:44])) / 8
	audio := driftAudio{make([]float32, n), make([]float32, n)}
	for i := range n {
		audio.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*8:]))
		audio.right[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[48+i*8:]))
	}
	return audio
}

// Mute the other tracks while preserving their indices in the chance hash.
// The chorus never engages the master limiter, so isolated master differences
// identify the responsible track without nonlinear interactions.
func driftIsolate(t *testing.T, score *notation.Score, track int) (*notation.Score, engine.Config) {
	t.Helper()
	copyScore := *score
	copyScore.Tracks = append([]notation.Track(nil), score.Tracks...)
	for i := range copyScore.Tracks {
		if i == track {
			continue
		}
		params := append([]notation.Param(nil), copyScore.Tracks[i].Params...)
		found := false
		for j := range params {
			if params[j].Name == "level" {
				params[j].Value = "off"
				found = true
			}
		}
		if !found {
			params = append(params, notation.Param{Name: "level", Value: "off"})
		}
		copyScore.Tracks[i].Params = params
	}
	p, ds := project.FromScore(&copyScore)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return &copyScore, cfg
}

func driftSamples(t *testing.T, out io.Writer, score *notation.Score, cfg engine.Config, a, b driftAudio, bars int) {
	t.Helper()
	isolatedA, isolatedB := make([]driftAudio, cfg.Tracks), make([]driftAudio, cfg.Tracks)
	for i := 0; i < cfg.Tracks; i++ {
		s, c := driftIsolate(t, score, i)
		isolatedA[i] = driftOffline(t, s, bars, 3, 4096)
		isolatedB[i] = driftEngine(t, c, bars, 3, driftLatency(t, c))
		d, peak, rms := driftMeasure(t, isolatedA[i], isolatedB[i])
		fmt.Fprintf(out, "TRACK %s mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f\n", score.Tracks[i].Name, d.MeanDB, d.MaxDB, peak, rms)
	}
	responsible := func(sample int) (string, float64) {
		name, peak := "none", 0.0
		for i := 0; i < cfg.Tracks; i++ {
			d := max(math.Abs(float64(isolatedA[i].left[sample])-float64(isolatedB[i].left[sample])), math.Abs(float64(isolatedA[i].right[sample])-float64(isolatedB[i].right[sample])))
			if d > peak {
				name, peak = score.Tracks[i].Name, d
			}
		}
		return name, peak
	}
	count := 0
	for i := range a.left {
		if a.left[i] == b.left[i] && a.right[i] == b.right[i] {
			continue
		}
		track, tap := responsible(i)
		fmt.Fprintf(out, "SAMPLE %d offline=(%.9g,%.9g) unified=(%.9g,%.9g) abs=%.9g track=%s track_abs=%.9g\n", i, a.left[i], a.right[i], b.left[i], b.right[i], max(math.Abs(float64(a.left[i])-float64(b.left[i])), math.Abs(float64(a.right[i])-float64(b.right[i]))), track, tap)
		count++
		if count == 20 {
			break
		}
	}
	clock, _ := seq.NewClock(48000, cfg.BPMMilli)
	for bar := 0; bar <= bars; bar++ {
		start, end := int(clock.SampleAtTick(int64(bar)*seq.TicksPerBar)), int(clock.SampleAtTick(int64(bar+1)*seq.TicksPerBar))
		if bar == bars {
			end = len(a.left)
		}
		peak, at := 0.0, start
		for i := start; i < end; i++ {
			d := max(math.Abs(float64(a.left[i])-float64(b.left[i])), math.Abs(float64(a.right[i])-float64(b.right[i])))
			if d > peak {
				peak, at = d, i
			}
		}
		track, _ := responsible(at)
		if bar == bars {
			fmt.Fprintf(out, "TAIL range=[%d,%d) peak=%.9f sample=%d track=%s\n", start, end, peak, at, track)
		} else {
			fmt.Fprintf(out, "BAR %d range=[%d,%d) peak=%.9f sample=%d track=%s\n", bar+1, start, end, peak, at, track)
		}
	}
}

func TestRenderDriftProbes(t *testing.T) {
	dir := os.Getenv("CICADA_RENDER_DRIFT_DIR")
	if dir == "" {
		t.Skip("set CICADA_RENDER_DRIFT_DIR to save investigation evidence")
	}
	// Capturing a baseline requires an explicit phase and the baseline wav.go
	// overlay. A normal rerun must not overwrite the before-change evidence.
	phase := os.Getenv("CICADA_RENDER_DRIFT_PHASE")
	if phase == "" {
		phase = "after"
	}
	if phase != "before" && phase != "after" {
		t.Fatal("CICADA_RENDER_DRIFT_PHASE must be before or after")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	score, cfg := driftScore(t, "cicada-chorus")
	manifest, err := json.Marshal(struct {
		Config  engine.Config
		Bars    int
		TailSec float64
		Trim    int
	}{cfg, 17, 3, driftLatency(t, cfg)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chorus-control.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	a := driftOffline(t, score, 17, 3, 4096)
	b := driftEngine(t, cfg, 17, 3, driftLatency(t, cfg))
	var out bytes.Buffer
	driftSamples(t, &out, score, cfg, a, b, 17)
	for _, legacy := range []bool{false, true} {
		c, commands := driftDrumCommands(t, cfg, 17, legacy)
		probe := driftEngineCommands(t, c, 17, 3, driftLatency(t, c), commands)
		d, peak, rms := driftMeasure(t, a, probe)
		fmt.Fprintf(&out, "CAUSE drum_chance legacy=%v mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f\n", legacy, d.MeanDB, d.MaxDB, peak, rms)
		if !legacy {
			_, p, _ := driftMeasure(t, b, probe)
			if p != 0 {
				t.Fatalf("command control differs from engine: %.9g", p)
			}
		}
	}
	for _, block := range []int{1, 127, 4096} {
		c := cfg
		c.MaxBlock = block
		probe := driftEngine(t, c, 17, 3, driftLatency(t, c))
		d, peak, rms := driftMeasure(t, b, probe)
		fmt.Fprintf(&out, "CAUSE block=%d mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f\n", block, d.MeanDB, d.MaxDB, peak, rms)
	}
	short := driftEngine(t, cfg, 17, 0, driftLatency(t, cfg))
	d, peak, rms := driftMeasure(t, driftAudio{b.left[:len(short.left)], b.right[:len(short.right)]}, short)
	fmt.Fprintf(&out, "CAUSE tail-0-vs-3-prefix mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f\n", d.MeanDB, d.MaxDB, peak, rms)
	if cfg.DelayA != nil || cfg.ReverbB != nil || cfg.CompMusic != nil {
		t.Fatal("chorus routing control requires no effects")
	}
	for _, track := range cfg.Track[:cfg.Tracks] {
		if track.SendA != 0 || track.SendB != 0 || track.InsertDrive != nil {
			t.Fatal("chorus routing control requires no sends or inserts")
		}
	}
	c := cfg
	c.DelayA, c.ReverbB, c.CompMusic = nil, nil, nil
	noEffects := driftEngine(t, c, 17, 3, driftLatency(t, c))
	d, peak, rms = driftMeasure(t, b, noEffects)
	fmt.Fprintf(&out, "CAUSE effect-order-and-routing mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f\n", d.MeanDB, d.MaxDB, peak, rms)
	clock, _ := seq.NewClock(48000, cfg.BPMMilli)
	for iteration := int64(0); iteration < 17; iteration++ {
		native := seq.ProbabilityHit(60, 1717, 1, 0, iteration, 13)
		legacy := seq.ProbabilityHit(60, 1717, 1, uint8(drum.CH), iteration, 13)
		if native != legacy {
			fmt.Fprintf(&out, "CHANCE bar=%d step=13 tick=%d sample=%d offline_hit=%v unified_hit=%v\n", iteration+1, (iteration*16+13)*seq.TicksPerStep, clock.SampleAtTick((iteration*16+13)*seq.TicksPerStep), legacy, native)
		}
	}
	for _, name := range driftExamples {
		s, c := driftScore(t, name)
		bars := 0
		for _, entry := range s.Song {
			bars += entry.Bars
		}
		bars = min(16, bars)
		offline := driftOffline(t, s, bars, 3, 4096)
		unified := driftEngine(t, c, bars, 3, driftLatency(t, c))
		d, peak, rms := driftMeasure(t, offline, unified)
		fmt.Fprintf(&out, "METRIC %s phase=%s bars=%d tail=3 mean_db=%.9f max_db=%.1f peak=%.9f rms=%.9f\n", name, phase, bars, d.MeanDB, d.MaxDB, peak, rms)
		driftWriteWAV(t, filepath.Join(dir, name+"."+phase+".offline.float.wav"), offline, s, bars, 32, 3)
		driftWriteWAV(t, filepath.Join(dir, name+"."+phase+".unified.float.wav"), unified, s, bars, 32, 3)
	}
	t.Log(out.String())
	if err := os.WriteFile(filepath.Join(dir, "probes."+phase+".txt"), out.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}
