package render

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func pmScore(t *testing.T) *notation.Score {
	t.Helper()
	source, err := os.ReadFile("../examples/fm-bell.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	return score
}

func TestGraphPMOfflineNativeParity(t *testing.T) {
	score := pmScore(t)
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

func TestGraphPMNativeCallbackAllocs(t *testing.T) {
	p, ds := project.FromScore(pmScore(t))
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
		t.Logf("METRIC: graph PM native callback allocations/run | rate=%d allocations=%g", rate, allocs)
	}
}

func TestGraphPMOfflineRenderAllocs(t *testing.T) {
	score := pmScore(t)
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
			t.Logf("METRIC: graph PM offline render allocated bytes | rate=%d bits=%d blocks=%d bytes=%d", rate, bits, writer.blocks, writer.allocated)
		}
	}
}

func TestGraphPMOfflineFirstBlockAllocs(t *testing.T) {
	score := pmScore(t)
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
		t.Logf("METRIC: graph PM first offline block allocated bytes | rate=%d bytes=%d", rate, allocated)
	}
}
