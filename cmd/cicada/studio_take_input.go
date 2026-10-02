package main

import (
	"fmt"
	"io"
)

type studioTakeInputOpener func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error)

// prepareTakeInput opens the duplex device before choosing the journal and ring
// format. A device may negotiate mono even when the host requested stereo.
func (t *studioTransport) prepareTakeInput() (int, int, error) {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	rate, err := t.ensureSampleRate()
	if err != nil {
		return 0, 0, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.audioOptions.InputEnabled {
		return 0, 0, fmt.Errorf("enable duplex input before arming capture")
	}
	opened := false
	if t.audio == nil {
		open := t.takeInputOpener
		if open == nil {
			open = openStudioAudio
		}
		t.audio, err = open(t.stream, t.audioOptions, t.selectedAudioBackend(), rate)
		if err != nil {
			return 0, 0, err
		}
		opened = true
	}
	actual := t.audio.Snapshot()
	if actual.InputChannels < 1 || actual.InputChannels > 2 || actual.SampleRate != rate {
		if opened {
			_ = t.audio.Close()
			t.audio = nil
		}
		return 0, 0, fmt.Errorf("capture requires mono/stereo input at the selected %d Hz", rate)
	}
	return rate, actual.InputChannels, nil
}
