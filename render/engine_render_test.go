package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// These helpers deliberately remain test-only: export cannot change paths until
// the owner has listened to the A/B files. All comparisons use float32 PCM before
// integer quantization, with normalization and dither disabled.
type driftAudio struct{ left, right []float32 }

func driftScore(t *testing.T, name string) (*notation.Score, engine.Config) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "examples", name+".cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("project diagnostics: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return score, cfg
}

func driftOffline(t *testing.T, score *notation.Score, bars int, tail float64, block int) driftAudio {
	t.Helper()
	var wav bytes.Buffer
	dither := false
	_, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: bars, TailSec: tail, Block: block, Dither: &dither}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	data := wav.Bytes()
	n := int(binary.LittleEndian.Uint32(data[40:44])) / 8
	out := driftAudio{make([]float32, n), make([]float32, n)}
	for i := range out.left {
		out.left[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*8:]))
		out.right[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[48+i*8:]))
	}
	return out
}

func driftLatency(t *testing.T, cfg engine.Config) int {
	t.Helper()
	limiter, err := mix.NewLimiter(cfg.SampleRate)
	if err != nil {
		t.Fatal(err)
	}
	latency := limiter.LatencyFrames()
	for _, track := range cfg.Track[:cfg.Tracks] {
		if track.InsertDrive != nil {
			latency += fx.DriveLatencyFrames
			break
		}
	}
	return latency
}

func driftEngine(t *testing.T, cfg engine.Config, bars int, tail float64, trim int) driftAudio {
	return driftEngineCommands(t, cfg, bars, tail, trim, nil)
}

func driftEngineCommands(t *testing.T, cfg engine.Config, bars int, tail float64, trim int, commands []cmd.Command) driftAudio {
	t.Helper()
	clock, err := seq.NewClock(cfg.SampleRate, cfg.BPMMilli)
	if err != nil {
		t.Fatal(err)
	}
	end := int(clock.SampleAtTick(int64(bars) * seq.TicksPerBar))
	n := end + int(math.Ceil(tail*float64(cfg.SampleRate))) + trim
	player, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	out := driftAudio{make([]float32, n), make([]float32, n)}
	for at := 0; at < n; {
		if at == end {
			if !player.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff}) {
				t.Fatal("stop rejected")
			}
		}
		frames := min(cfg.MaxBlock, n-at)
		if at < end {
			frames = min(frames, end-at)
		}
		for len(commands) > 0 && clock.SampleAtTick(commands[0].Tick) < int64(at+frames) {
			if !player.Push(commands[0]) {
				t.Fatal("probe command rejected")
			}
			commands = commands[1:]
		}
		player.Render(out.left[at:at+frames], out.right[at:at+frames])
		var message cmd.Message
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault: %+v", message)
			}
		}
		at += frames
	}
	return driftAudio{out.left[trim:], out.right[trim:]}
}

func driftMeasure(t *testing.T, a, b driftAudio) (FingerprintDifference, float64, float64) {
	t.Helper()
	if len(a.left) != len(b.left) {
		t.Fatal("audio lengths differ")
	}
	af, err := FingerprintStereo(a.left, a.right, 48000)
	if err != nil {
		t.Fatal(err)
	}
	bf, err := FingerprintStereo(b.left, b.right, 48000)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := FingerprintDrift(af, bf)
	if err != nil {
		t.Fatal(err)
	}
	peak, rms := driftDelta(t, a, b)
	return diff, peak, rms
}

func driftDelta(t *testing.T, a, b driftAudio) (float64, float64) {
	t.Helper()
	if len(a.left) != len(b.left) {
		t.Fatal("audio lengths differ")
	}
	var peak, power float64
	for i := range a.left {
		l, r := float64(a.left[i])-float64(b.left[i]), float64(a.right[i])-float64(b.right[i])
		peak = max(peak, math.Abs(l), math.Abs(r))
		power += l*l + r*r
	}
	return peak, math.Sqrt(power / float64(2*len(a.left)))
}

// Run explicitly with CICADA_RENDER_DRIFT_DIR set. Reports and audio never go
// into the repository. This does not update any golden or tolerance.
func TestRenderDriftInvestigation(t *testing.T) {
	dir := os.Getenv("CICADA_RENDER_DRIFT_DIR")
	if dir == "" {
		t.Skip("set CICADA_RENDER_DRIFT_DIR to save investigation evidence")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	for _, example := range []struct {
		name string
		bars int
		tail float64
	}{{"first-acid", 8, 0}, {"cicada-chorus", 17, 3}} {
		score, cfg := driftScore(t, example.name)
		offline := driftOffline(t, score, example.bars, example.tail, 4096)
		for _, variant := range []struct {
			name string
			trim int
		}{{"raw", 0}, {"latency-compensated", driftLatency(t, cfg)}} {
			unified := driftEngine(t, cfg, example.bars, example.tail, variant.trim)
			diff, peak, rms := driftMeasure(t, offline, unified)
			fmt.Fprintf(&report, "METRIC %s bars=%d tail=%.1f variant=%s mean_db=%.9f max_db=%.6f peak=%.9f rms=%.9f\n", example.name, example.bars, example.tail, variant.name, diff.MeanDB, diff.MaxDB, peak, rms)
		}
	}
	t.Log(report.String())
	if err := os.WriteFile(filepath.Join(dir, "reproduction.txt"), report.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}
