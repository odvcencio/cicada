package main

import (
	"fmt"

	"m31labs.dev/cicada/host/capture"
)

// armCapture admits a prepared ring/writer and opens the device without starting
// the score. The native take writer supplies this Recorder; browser capture is
// a separate host. Mono/stereo input must use the existing duplex device clock.
func (t *studioTransport) armCapture(recorder *capture.Recorder) error {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	rate, err := t.ensureSampleRate()
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.audioOptions.InputEnabled {
		return fmt.Errorf("enable duplex input before arming capture")
	}
	opened := false
	if t.audio == nil {
		t.audio, err = openStudioAudio(t.stream, t.audioOptions, t.selectedAudioBackend(), rate)
		if err != nil {
			return err
		}
		opened = true
	}
	if err := t.audio.ArmCapture(recorder); err != nil {
		if opened {
			_ = t.audio.Close()
			t.audio = nil
		}
		return err
	}
	return nil
}

// startCapture uses the score's tempo and one bar by default. A caller may Begin
// with an explicit zero-bar CountIn instead. Calibration is the residual after
// clock mapping, never the full measured round-trip offset.
func (t *studioTransport) startCapture(calibration capture.Calibration) error {
	t.mu.Lock()
	if t.audio == nil || !t.audio.Armed() {
		t.mu.Unlock()
		return fmt.Errorf("arm capture before recording")
	}
	if t.playing {
		t.mu.Unlock()
		return fmt.Errorf("pause playback before starting count-in")
	}
	t.pendingCapture = &calibration
	t.mu.Unlock()
	if err := t.start(); err != nil {
		t.mu.Lock()
		t.pendingCapture = nil
		t.mu.Unlock()
		return err
	}
	return nil
}

func (t *studioTransport) prepareCaptureLocked(audio studioAudioDevice, bpmMilli int64) error {
	if t.pendingCapture == nil {
		return nil
	}
	calibration := *t.pendingCapture
	t.pendingCapture = nil
	countIn, err := capture.DefaultCountIn(t.sampleRate, bpmMilli)
	if err != nil {
		return err
	}
	return audio.BeginCapture(countIn, calibration)
}

func (t *studioTransport) disarmCapture() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.audio == nil {
		return nil
	}
	err := t.audio.DisarmCapture()
	if !t.playing && t.audio.StopClosesDevice() {
		closeErr := t.audio.Close()
		t.audio = nil
		if err == nil {
			err = closeErr
		}
	}
	return err
}
