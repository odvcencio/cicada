package main

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/audiobackend"
)

func TestPlayAudioBannerNamesBackendAndHost(t *testing.T) {
	got := playAudioBanner(audiobackend.Format{
		Backend: audiobackend.Tymbal, Host: "ALSA", Device: "ALSA default",
		SampleRate: 48_000, FramesPerPeriod: 256,
	})
	for _, want := range []string{"tymbal", "ALSA", "48000 Hz", "256 frames"} {
		if !strings.Contains(got, want) {
			t.Errorf("play audio banner %q does not contain %q", got, want)
		}
	}
}
