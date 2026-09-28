package audiobackend

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/audiobackend/rt"
)

func TestOtoSilent60sRunOrCleanFailure(t *testing.T) {
	if os.Getenv("CICADA_AUDIO_SOAK") != "60s" {
		t.Skip("set CICADA_AUDIO_SOAK=60s to run the silent Oto soak")
	}
	restore, _, _ := rt.RaiseProcessThreads(1)
	defer restore()
	backend, err := New(Oto)
	if err != nil {
		t.Fatal(err)
	}
	rate, err := backend.SampleRate(Config{Channels: 2, FramesPerPeriod: 256})
	if err != nil {
		t.Fatal(err)
	}
	var callbacks atomic.Uint64
	var nonzero atomic.Bool
	render := silentRender(&callbacks, &nonzero)
	stream, err := backend.Open(Config{SampleRate: rate, Channels: 2, FramesPerPeriod: 256}, render)
	if err != nil {
		t.Logf("Oto failed cleanly while opening the silent stream: %v", err)
		return
	}
	defer stream.Close()
	if err := stream.Start(); err != nil {
		t.Logf("Oto failed cleanly while starting the silent stream: %v", err)
		return
	}
	time.Sleep(60 * time.Second)
	stream.Pause()
	if err := stream.Err(); err != nil {
		t.Fatalf("Oto failed during the silent run: %v", err)
	}
	if callbacks.Load() == 0 || nonzero.Load() {
		t.Fatalf("Oto silent run callbacks=%d nonzero=%t", callbacks.Load(), nonzero.Load())
	}
	t.Logf("Oto completed 60 s silently with %d callbacks", callbacks.Load())
}

func TestNullSilent60sRun(t *testing.T) {
	if os.Getenv("CICADA_AUDIO_SOAK") != "60s" {
		t.Skip("set CICADA_AUDIO_SOAK=60s to run the null backend soak")
	}
	backend, err := New(Null)
	if err != nil {
		t.Fatal(err)
	}
	rate, err := backend.SampleRate(Config{Channels: 2, FramesPerPeriod: 256})
	if err != nil {
		t.Fatal(err)
	}
	var callbacks atomic.Uint64
	var nonzero atomic.Bool
	stream, err := backend.Open(Config{SampleRate: rate, Channels: 2, FramesPerPeriod: 256}, silentRender(&callbacks, &nonzero))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Second)
	stream.Pause()
	if err := stream.Err(); err != nil {
		t.Fatalf("null backend failed during the silent run: %v", err)
	}
	if callbacks.Load() < 10_000 || nonzero.Load() {
		t.Fatalf("null silent run callbacks=%d nonzero=%t", callbacks.Load(), nonzero.Load())
	}
	t.Logf("null backend completed 60 s silently with %d callbacks", callbacks.Load())
}

func silentRender(callbacks *atomic.Uint64, nonzero *atomic.Bool) Callback {
	return func(_ [][]float32, output [][]float32) error {
		for _, channel := range output {
			for _, sample := range channel {
				if sample != 0 {
					nonzero.Store(true)
				}
			}
			clear(channel)
		}
		callbacks.Add(1)
		return nil
	}
}
