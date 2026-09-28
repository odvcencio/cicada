//go:build windows

package audiobackend

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/tymbal"
)

func TestTymbalWASAPISilent60sRun(t *testing.T) {
	if os.Getenv("CICADA_AUDIO_WASAPI_SOAK") != "60s" {
		t.Skip("set CICADA_AUDIO_WASAPI_SOAK=60s on the Windows host to run the WASAPI soak")
	}
	restore, _, _ := tymbal.RaiseProcessThreads(1)
	defer restore()
	backend, err := New(Tymbal)
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
		t.Fatalf("Tymbal WASAPI failed during the silent run: %v", err)
	}
	format := stream.Format()
	minimumCallbacks := uint64(int64(format.SampleRate) * 60 / int64(format.FramesPerPeriod) * 80 / 100)
	if callbacks.Load() < minimumCallbacks || nonzero.Load() {
		t.Fatalf("Tymbal WASAPI silent run callbacks=%d nonzero=%t", callbacks.Load(), nonzero.Load())
	}
	t.Logf("Tymbal WASAPI completed 60 s at %d Hz / %d frames with %d zero-output callbacks", format.SampleRate, format.FramesPerPeriod, callbacks.Load())
}
