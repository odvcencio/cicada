package main

import (
	"fmt"
	"time"

	"m31labs.dev/cicada/host/capture"
)

// Manual correction is the residual after reported device timing, pinned to
// the currently opened device. Wait only on the control side for its first
// clock observation; the capture/render callback never waits.
func (t *studioTransport) recordingCalibration() (capture.Calibration, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recordingCalibrationLocked()
}

func (t *studioTransport) recordingCalibrationLocked() (capture.Calibration, error) {
	ms := t.audioOptions.RecordingOffsetMS
	if ms == 0 {
		return capture.Calibration{}, nil
	}
	device, ok := t.audio.(interface{ CaptureDeviceEpoch() uint64 })
	if !ok {
		return capture.Calibration{}, fmt.Errorf("recording timing correction requires a native input clock")
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		if epoch := device.CaptureDeviceEpoch(); epoch != 0 {
			return capture.Calibration{DeviceEpoch: epoch, RemainingFrames: vocalMillisecondsFrames(ms, t.sampleRate), Valid: true}, nil
		}
		if !time.Now().Before(deadline) {
			return capture.Calibration{}, fmt.Errorf("microphone clock is not ready; retry recording timing correction")
		}
		time.Sleep(time.Millisecond)
	}
}

// The backing can end before the mapped input reaches punch-out. Keep duplex
// callbacks running through that tail; the recorder stops on its input boundary.
func (t *studioTransport) captureRunningLocked() bool {
	if t.audio == nil {
		return false
	}
	snapshot := t.audio.Snapshot()
	return snapshot.Capture != nil && snapshot.Capture.Recording
}

// armCapture admits a prepared ring/writer and opens the device without starting
// the score. The native take writer supplies this Recorder; browser capture is
// a separate host. Mono/stereo input must use the existing duplex device clock.
func (t *studioTransport) armCapture(recorder *capture.Recorder, preparedMode ...string) error {
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
	t.stopPreviewLocked()
	opened := false
	if t.audio == nil {
		open := t.takeInputOpener
		if open == nil {
			open = openStudioRecordingAudio
		}
		t.audio, err = open(t.stream, t.audioOptions, t.selectedAudioBackend(), rate)
		if err != nil {
			return err
		}
		opened = true
	}
	if len(preparedMode) > 0 {
		device, ok := t.audio.(interface{ SetPreparedCaptureMode(string) })
		if !ok {
			if opened {
				_ = t.audio.Close()
				t.audio = nil
			}
			return fmt.Errorf("this audio backend cannot select the prepared input channel")
		}
		device.SetPreparedCaptureMode(preparedMode[0])
		defer device.SetPreparedCaptureMode("")
	}
	if err := t.audio.ArmCapture(recorder); err != nil {
		if opened {
			_ = t.audio.Close()
			t.audio = nil
		}
		return err
	}
	t.preparedCapture = len(preparedMode) > 0
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
	// A paused armed device can have an advanced playback cursor. Discard that
	// stream so count-in placement is relative to a fresh recording origin.
	t.stopLocked()
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
	if !calibration.Valid {
		var err error
		calibration, err = t.recordingCalibrationLocked()
		if err != nil {
			return err
		}
	}
	countIn, err := capture.DefaultCountIn(t.sampleRate, bpmMilli)
	if err != nil {
		return err
	}
	return audio.BeginCapture(countIn, calibration)
}

func (t *studioTransport) disarmCapture() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.preparedCapture = false
	if t.audio == nil {
		return nil
	}
	err := t.audio.DisarmCapture()
	t.lastInput = t.audio.Snapshot()
	if !t.playing && t.audio.StopClosesDevice() {
		closeErr := t.audio.Close()
		t.audio = nil
		if err == nil {
			err = closeErr
		}
	}
	return err
}
