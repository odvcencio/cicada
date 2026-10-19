package main

import (
	"fmt"
	"os"
	"testing"

	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/tymbal"
	"m31labs.dev/tymbal/tymbaltest"
)

func TestStudioNativeAudioHostNames(t *testing.T) {
	for _, test := range []struct{ goos, host, label string }{
		{"darwin", "coreaudio", "Tymbal CoreAudio"},
		{"windows", "wasapi", "Tymbal WASAPI shared mode"},
		{"linux", "alsa", "Tymbal ALSA"},
	} {
		if host, label := studioAudioHostForOS(audiobackend.Tymbal, test.goos), studioAudioBackendLabelForOS(audiobackend.Tymbal, test.goos); host != test.host || label != test.label {
			t.Fatalf("%s: host=%q label=%q, want %q and %q", test.goos, host, label, test.host, test.label)
		}
		if host := studioAudioHostForOS(audiobackend.Oto, test.goos); host != "" {
			t.Fatalf("Oto inherited native host %q on %s", host, test.goos)
		}
	}
}

func TestMacPlaybackDoesNotEnableMicrophoneUntilRequested(t *testing.T) {
	options := defaultStudioAudioOptionsForOS(audiobackend.Tymbal, "darwin")
	if options.InputEnabled || !options.MonitorMuted || options.PeriodFrames != 256 {
		t.Fatalf("new Mac session should start with playback and muted, disabled input: %+v", options)
	}
}

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
