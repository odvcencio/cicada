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
	t.stopPreviewLocked()
	opened := false
	if t.audio == nil {
		open := t.takeInputOpener
		if open == nil {
			open = openStudioRecordingAudio
		}
		t.audio, err = open(t.stream, t.audioOptions, t.selectedAudioBackend(), rate)
		if err != nil {
			return 0, 0, err
		}
		opened = true
	}
	actual := t.audio.Snapshot()
	if actual.InputChannels < 1 || actual.InputChannels > 2 || actual.SampleRate != rate || actual.PeriodFrames < 1 || actual.PeriodFrames > 8192 {
		if opened {
			_ = t.audio.Close()
			t.audio = nil
		}
		if actual.InputChannels == 0 && actual.Warning != "" {
			return 0, 0, fmt.Errorf("%s", actual.Warning)
		}
		return 0, 0, fmt.Errorf("capture requires mono/stereo input at the selected %d Hz", rate)
	}
	return rate, actual.InputChannels, nil
}

// Allocate at least one second of writer headroom for the actual callback
// period. A fixed 64-slot ring lasts only 85 ms at 64 frames / 48 kHz.
// This queue belongs to the recording writer; it adds no monitoring latency.
func (t *studioTransport) takeCaptureBufferShape(rate int) (slots, frames int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	frames = t.audio.Snapshot().PeriodFrames
	slots = max(64, (rate+frames-1)/frames+2)
	return slots, frames
}
