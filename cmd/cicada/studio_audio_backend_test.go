package main

import (
	"fmt"
	"os"
	"testing"

	"m31labs.dev/tymbal"
	"m31labs.dev/tymbal/tymbaltest"
)

type zeroAudioSource struct{}

func (zeroAudioSource) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

func TestStudioAudioRenderCallbackDoesNotAllocate(t *testing.T) {
	if os.Getenv("CICADA_AUDIO_CALLBACK_NOALLOC") != "1" {
		t.Skip("set CICADA_AUDIO_CALLBACK_NOALLOC=1 to measure the real-time callback in isolation")
	}
	for _, period := range []int{64, 128, 256, 512, 1024} {
		t.Run(fmt.Sprint(period), func(t *testing.T) {
			monitor := studioAudioMonitor{Gain: .5, Mode: "stereo"}
			session := &backendStudioAudio{
				reader: zeroAudioSource{},
				pcm:    make([]byte, period*8),
			}
			session.playing.Store(true)
			session.monitorState.Store(&monitor)
			callback := func(_ tymbal.Time, input, output [][]float32) {
				if err := session.renderPeriod(input, output); err != nil {
					panic(err)
				}
			}
			tymbaltest.NoAlloc(t, callback, 2, 2, period)
			t.Logf("METRIC input_monitor_period=%d callback_allocations_per_10000=0", period)
		})
	}
}
