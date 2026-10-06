package render

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestGraphDelayOfflineRejectsOutOfRangeNotes(t *testing.T) {
	for _, primitive := range []string{"delay(noise(), 1 / pitch)", "comb(noise(), 1 / pitch, 0.8, 0.3)"} {
		for _, note := range []struct {
			pitch     string
			transpose int
		}{{"c0", 0}, {"c1", -12}} {
			t.Run(fmt.Sprintf("%s/%s/%d", primitive, note.pitch, note.transpose), func(t *testing.T) {
				source := fmt.Sprintf("instrument sound { voice mono { out = %s } } track t sound {} pattern p notes steps=1 transpose=%d { %s } scene s { t=p } song { s }", primitive, note.transpose, note.pitch)
				score, ds := notation.Parse([]byte(source))
				if score == nil || len(ds) != 0 {
					t.Fatalf("parse: %+v", ds)
				}
				p, ds := project.FromScore(score)
				if p == nil {
					t.Fatalf("project: %+v", ds)
				}
				for _, rate := range []int{48_000, 96_000} {
					_, liveErr := project.CompileEngine(p, rate, 128)
					var output bytes.Buffer
					opts := Options{SampleRate: rate, Bars: 1, Block: 128}
					_, err := WAV(score, opts, &output)
					if (err != nil) != (liveErr != nil) || (err != nil) != (rate == 96_000) {
						t.Fatalf("rate %d: live=%v offline=%v", rate, liveErr, err)
					}
					if err != nil {
						if !strings.Contains(err.Error(), "CICADA-PARAM") || !strings.Contains(err.Error(), "delay time is out of range") || output.Len() != 0 {
							t.Fatalf("invalid export wrote %d bytes: %v", output.Len(), err)
						}
						dir := filepath.Join(t.TempDir(), "stems")
						if _, err := Stems(score, opts, dir); err == nil || !strings.Contains(err.Error(), "delay time is out of range") {
							t.Fatalf("invalid stems accepted: %v", err)
						}
						if _, err := os.Stat(dir); !os.IsNotExist(err) {
							t.Fatalf("invalid stems created output: %v", err)
						}
					}
				}
			})
		}
	}
}

func TestGraphDelayOfflineKitLaneValidation(t *testing.T) {
	for _, primitive := range []string{"delay(noise(), %d / pitch)", "comb(noise(), %d / pitch, 0.8, 0.3)"} {
		for _, test := range []struct {
			periods, rate int
			invalid       bool
		}{{8, 48_000, true}, {4, 48_000, false}, {4, 96_000, true}} {
			t.Run(fmt.Sprintf("%s/%d/%d", primitive, test.periods, test.rate), func(t *testing.T) {
				source := fmt.Sprintf("instrument sound { voice mono { out = %s } } kit kit { bd = sound } track t kit {} pattern p drums steps=1 { bd: x } scene s { t=p } song { s }", fmt.Sprintf(primitive, test.periods))
				score, ds := notation.Parse([]byte(source))
				if score == nil || len(ds) != 0 {
					t.Fatalf("parse: %+v", ds)
				}
				var output bytes.Buffer
				_, err := WAV(score, Options{SampleRate: test.rate, Bars: 1}, &output)
				if (err != nil) != test.invalid {
					t.Fatalf("kit export: %v", err)
				}
				if test.invalid && (!strings.Contains(err.Error(), "delay time is out of range") || !strings.Contains(err.Error(), "lane bd") || output.Len() != 0) {
					t.Fatalf("invalid kit wrote %d bytes: %v", output.Len(), err)
				}
			})
		}
	}
}

func TestGraphDelayOfflineValidatesAssignedPatterns(t *testing.T) {
	source := []byte("instrument pluck { voice mono { out = comb(noise(), 1 / pitch, 0.8, 0.3) } } instrument tone { voice mono { out = sine(pitch) } } track strings pluck {} track bass tone {} pattern high notes steps=1 { a3 } pattern low notes steps=1 { c0 } pattern unused notes steps=1 { c0 } scene s { strings=high bass=low } song { s }")
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", ds)
	}
	if _, err := project.CompileEngine(p, 96_000, 128); err != nil {
		t.Fatalf("valid live score: %v", err)
	}
	if _, err := WAV(score, Options{SampleRate: 96_000, Bars: 1}, io.Discard); err != nil {
		t.Fatalf("unassigned notes rejected: %v", err)
	}
}

func delayScore(t *testing.T) *notation.Score {
	t.Helper()
	source, err := os.ReadFile("../examples/pluck.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	return score
}

func TestGraphDelayOfflineNativeParity(t *testing.T) {
	score := delayScore(t)
	for _, rate := range []int{44_100, 48_000} {
		var reference []byte
		for _, block := range []int{1, 128, 4096} {
			var out bytes.Buffer
			report, err := WAV(score, Options{SampleRate: rate, Bits: 32, Bars: 2, Block: block}, &out)
			if err != nil {
				t.Fatal(err)
			}
			if reference == nil {
				reference = append([]byte(nil), out.Bytes()...)
			} else if !bytes.Equal(reference, out.Bytes()) {
				t.Fatalf("offline PCM differs at rate %d block %d", rate, block)
			}
			assertSceneEngineMatchesWAV(t, score, out.Bytes(), rate, 0, int(report.Frames))
		}
	}
}

func TestGraphDelayNativeCallbackAllocs(t *testing.T) {
	p, ds := project.FromScore(delayScore(t))
	if p == nil {
		t.Fatalf("project: %+v", ds)
	}
	for _, rate := range []int{44_100, 48_000} {
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		live, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var left, right [128]float32
		allocs := testing.AllocsPerRun(100, func() {
			live.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			for range rate * 4 / 128 {
				live.Render(left[:], right[:])
				var message cmd.Message
				for live.Poll(&message) {
					if message.Kind == cmd.Fault {
						t.Fatalf("engine fault: %+v", message)
					}
				}
			}
			live.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
			live.Render(left[:], right[:])
			var message cmd.Message
			for live.Poll(&message) {
			}
		})
		if allocs != 0 {
			t.Fatalf("native callback allocated %g objects", allocs)
		}
		t.Logf("METRIC: graph delay native callback allocations/run | rate=%d allocations=%g", rate, allocs)
	}
}

// Observe allocation bytes between WAV writes, after setup and header output.
// The final patch of the header is intentionally excluded. This measures the
// offline render loop, including PCM encoding, rather than voice construction.
type allocationWriter struct {
	stats               runtime.MemStats
	previous, allocated uint64
	blocks              int
}

// renderWAVMeasuringAllocs renders with GOMAXPROCS pinned to 1 after a GC,
// the same isolation testing.AllocsPerRun uses: MemStats.TotalAlloc is
// process-wide, so concurrently running goroutines would otherwise be counted
// as allocations of the render loop.
func renderWAVMeasuringAllocs(score *notation.Score, options Options, writer *allocationWriter) error {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	runtime.GC()
	_, err := WAV(score, options, writer)
	return err
}

func (w *allocationWriter) Write(data []byte) (int, error) {
	if len(data) <= 44 {
		return len(data), nil
	}
	runtime.ReadMemStats(&w.stats)
	if w.blocks > 0 {
		w.allocated += w.stats.TotalAlloc - w.previous
	}
	w.previous = w.stats.TotalAlloc
	w.blocks++
	return len(data), nil
}

func TestGraphDelayOfflineRenderAllocs(t *testing.T) {
	score := delayScore(t)
	for _, rate := range []int{44_100, 48_000} {
		for _, bits := range []int{24, 32} {
			var writer allocationWriter
			err := renderWAVMeasuringAllocs(score, Options{SampleRate: rate, Bits: bits, Bars: 2, Block: 128}, &writer)
			if err != nil {
				t.Fatal(err)
			}
			if writer.blocks < 2 || writer.allocated != 0 {
				t.Fatalf("offline render: %d blocks, %d allocated bytes", writer.blocks, writer.allocated)
			}
			t.Logf("METRIC: graph delay offline render allocated bytes | rate=%d bits=%d blocks=%d bytes=%d", rate, bits, writer.blocks, writer.allocated)
		}
	}
}

func TestGraphDelayOfflineFirstBlockAllocs(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	runtime.GC()
	score := delayScore(t)
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", ds)
	}
	for _, rate := range []int{44_100, 48_000} {
		tracks, err := compileTracks(score, p, rate)
		if err != nil {
			t.Fatal(err)
		}
		limiter, err := mix.NewLimiter(rate)
		if err != nil {
			t.Fatal(err)
		}
		encoder, err := newWAVEncoder(24, true, uint64(score.Seed), 1)
		if err != nil {
			t.Fatal(err)
		}
		events := []scheduled{
			{track: 0, event: seq.Event{Kind: seq.NoteOn, Note: 69, Velocity: 127}},
			{track: 1, event: seq.Event{Kind: seq.NoteOn, Note: 69, Velocity: 127}},
		}
		var buffer [128 * 6]byte
		var report Report
		var before, after runtime.MemStats
		prepareFirstBlockAllocationMeasurement(t)
		runtime.ReadMemStats(&before)
		err = renderBlock(io.Discard, tracks, nil, nil, nil, 0, 0, 0, 1, busMixerState{}, limiter, nil, &encoder, events, 0, 128, buffer[:], &report)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		allocated := after.TotalAlloc - before.TotalAlloc
		if allocated != 0 {
			t.Fatalf("first offline block allocated %d bytes", allocated)
		}
		t.Logf("METRIC: graph delay first offline block allocated bytes | rate=%d bytes=%d", rate, allocated)
	}
}
