package main

import (
	"encoding/json"
	"math"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"m31labs.dev/cicada/host/capture"
)

func TestStudioInputMeterHoldsShortOverloadWithoutChangingRecordedInput(t *testing.T) {
	a, _ := simulatedCaptureAudio(8, zeroAudioSource{})
	input := [][]float32{{.999969, -.2, .1, 0, 0, 0, 0, 0}, {.25, -.3, 0, 0, 0, 0, 0, 0}}
	original := [][]float32{append([]float32(nil), input[0]...), append([]float32(nil), input[1]...)}
	var retained [][]float32
	r, err := capture.NewRecorder(16, 8, 2, func(_ capture.RecordedBlock, in [][]float32) error {
		if retained == nil {
			retained = [][]float32{append([]float32(nil), in[0]...), append([]float32(nil), in[1]...)}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ArmCapture(r); err != nil {
		t.Fatal(err)
	}
	if err = a.BeginCapture(capture.CountIn{}, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	a.SetMonitor(studioAudioMonitor{Gain: 2, Mode: "stereo"})
	if err = a.Play(); err != nil {
		t.Fatal(err)
	}
	output := [][]float32{make([]float32, 8), make([]float32, 8)}
	simulatedCapturePeriod(t, a, 0, input, output)
	if !reflect.DeepEqual(input, original) {
		t.Fatal("metering or monitoring changed original input")
	}
	simulatedCapturePeriod(t, a, 8, [][]float32{make([]float32, 8), make([]float32, 8)}, output)
	tx := &studioTransport{audio: a, playing: true, audioOptions: studioAudioOptions{InputEnabled: true}}
	meter := tx.inputMeter()
	if !meter.Active || meter.Channels != 2 || meter.PeakL != 0 || meter.HeldPeakL != original[0][0] || meter.HeldPeakR != .3 || !meter.Clipped {
		t.Fatalf("short overload was lost between polls: %+v", meter)
	}
	if err = a.DisarmCapture(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retained, original) {
		t.Fatal("raw take differs from original input")
	}
	session := meter.Session
	a.Pause()
	if err = a.Play(); err != nil {
		t.Fatal(err)
	}
	next := tx.inputMeter()
	if next.Session == session || next.Clipped || next.HeldPeakL != 0 || next.HeldPeakR != 0 {
		t.Fatalf("next playback kept the old overload: %+v", next)
	}
	a.measureInput(original)
	tx.stop()
	stopped := tx.inputMeter()
	if tx.audio != nil || stopped.Active || !stopped.Clipped || stopped.HeldPeakL != original[0][0] {
		t.Fatalf("closing the device hid the finished pass's peak: %+v", stopped)
	}
}

func TestStudioCaptureMeterProjectionIsReadOnlyAndDoesNotOpenMicrophone(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	s.transport.audioOptions.InputEnabled = true
	journal := s.takes
	response := studioCall(t, s.routes(), "/api/capture", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("capture projection: %d %s", response.Code, response.Body)
	}
	var state struct {
		Input studioInputMeter `json:"input"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Input.Enabled || state.Input.Active || s.transport.audio != nil || s.captureID != "" || s.takes != journal {
		t.Fatalf("polling opened or armed a microphone: %+v", state)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("capture projection can be cached")
	}
}

func TestStudioInputMeterConcurrentPollingAndResetStayFinite(t *testing.T) {
	a, _ := simulatedCaptureAudio(8, zeroAudioSource{})
	tx := &studioTransport{audio: a, playing: true, audioOptions: studioAudioOptions{InputEnabled: true}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			a.measureInput([][]float32{{.5, float32(math.Inf(1))}, {float32(math.NaN()), .25}})
		}
	}()
	for range 1000 {
		a.resetInputMeter()
		if _, err := json.Marshal(tx.inputMeter()); err != nil {
			t.Fatal("invalid input broke polling JSON", err)
		}
	}
	wg.Wait()
}
