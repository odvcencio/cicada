package main

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/internal/audiobackend"
)

func TestStudioBufferChoiceDefaultsAndValidation(t *testing.T) {
	if got := normalizeStudioAudioOptions(studioAudioOptions{}).PeriodFrames; got != 256 {
		t.Fatalf("legacy default=%d", got)
	}
	for _, period := range []int{64, 128, 256, 512, 1024} {
		if err := validateStudioAudioOptions(normalizeStudioAudioOptions(studioAudioOptions{PeriodFrames: period})); err != nil {
			t.Fatalf("period%d: %v", period, err)
		}
	}
	for _, period := range []int{-1, 1, 63, 480, 8192} {
		if err := validateStudioAudioOptions(normalizeStudioAudioOptions(studioAudioOptions{PeriodFrames: period})); err == nil {
			t.Fatalf("acceptedunsupportedUIperiod%d", period)
		}
	}
}

func TestStudioBufferChoiceRequiresStoppedDisarmedDevice(t *testing.T) {
	audio, device := simulatedCaptureAudio(256, zeroAudioSource{})
	options := defaultStudioAudioOptionsFor(audiobackend.Null)
	tx := &studioTransport{audio: audio, audioNull: true, sampleRate: 48000, playing: true, audioOptions: options}
	changed := options
	changed.PeriodFrames = 64
	if err := tx.configureAudio(changed); err == nil || tx.audioOptions != options || device.closes != 0 {
		t.Fatalf("buffer changed while playback was active: %v", err)
	}
	tx.playing = false
	recorder, _ := capture.NewRecorder(4, 256, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	if err := audio.ArmCapture(recorder); err != nil {
		t.Fatal(err)
	}
	if err := tx.configureAudio(changed); err == nil || tx.audioOptions != options || device.closes != 0 {
		t.Fatalf("buffer changed while capture was armed: %v", err)
	}
	if err := audio.DisarmCapture(); err != nil {
		t.Fatal(err)
	}
	if err := tx.configureAudio(changed); err != nil {
		t.Fatal(err)
	}
	if tx.audio != nil || device.closes != 1 || tx.sampleRate != 0 || tx.audioOptions.PeriodFrames != 64 {
		t.Fatal("idle buffer change did not reopen the device format")
	}
}

func TestStudioAudioRuntimeProjectionDoesNotDiscoverOrOpenDevices(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	// A device-inventory query would fail for this nonexistent backend. Runtime
	// projection deliberately needs neither inventory nor a physical endpoint.
	s.transport.audioBackend = "unavailable-for-test"
	s.transport.audioOptions.PeriodFrames = 64
	s.transport.audioOptions.RecordingOffsetMS = 17.5
	response := studioCall(t, s.routes(), "/api/audio/config?runtime=1", nil)
	var state studioAudioRuntimeState
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &state) != nil || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("runtime projection failed: %d %s", response.Code, response.Body)
	}
	if state.Options.PeriodFrames != 64 || state.Options.RecordingOffsetMS != 17.5 || state.Runtime.Error != "" || s.transport.audio != nil || s.captureID != "" || len(s.takes.Takes()) != 0 {
		t.Fatalf("runtime polling changed audio: %+v", state)
	}
}

func TestCaptureBufferHasOneSecondWriterHeadroomAtLowPeriods(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		for _, frames := range []int{64, 128, 240, 256, 512, 1024, 8192} {
			a, _ := simulatedCaptureAudio(frames, zeroAudioSource{})
			tx := &studioTransport{audio: a}
			slots, period := tx.takeCaptureBufferShape(rate)
			if period != frames || slots < 64 || slots*period < rate {
				t.Fatalf("rate%d period%d -> slots%d frames%d", rate, frames, slots, period)
			}
		}
	}
}

func TestLowPeriodCaptureRetainsOneSecondWhileWriterStalls(t *testing.T) {
	const frames, rate = 64, 48000
	a, _ := simulatedCaptureAudio(frames, zeroAudioSource{})
	tx := &studioTransport{audio: a}
	slots, period := tx.takeCaptureBufferShape(rate)
	entered, release := make(chan struct{}), make(chan struct{})
	var saved int
	var changed bool
	recorder, err := capture.NewRecorder(slots, period, 2, func(_ capture.RecordedBlock, input [][]float32) error {
		if saved == 0 {
			close(entered)
			<-release
		}
		for i := range input[0] {
			changed = changed || math.Float32bits(input[0][i]) != math.Float32bits(.1234567) || math.Float32bits(input[1][i]) != math.Float32bits(-.7654321)
		}
		saved += len(input[0])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	input := [][]float32{make([]float32, frames), make([]float32, frames)}
	for i := range input[0] {
		input[0][i], input[1][i] = .1234567, -.7654321
	}
	for i := range rate / frames {
		recorder.Capture(capture.Block{EngineFrame: int64(i * frames), Frames: frames, Period: frames, SampleRate: rate, Layout: capture.LayoutStereo}, input)
		if i == 0 {
			<-entered
		}
	}
	close(release)
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if saved != rate || changed || recorder.Snapshot().Incomplete || recorder.Snapshot().Ring.LostFrames != 0 {
		t.Fatalf("writer pause lost/changed audio: frames=%d changed=%t capture=%+v", saved, changed, recorder.Snapshot())
	}
	t.Logf("METRIC low_period=64 writer_stall_headroom_frames=%d raw_pcm_max_delta=0 capture_lost_frames=0", saved)
}

func TestArmedInputMonitoringHasNoSoftwareQueueAndRetainsCountInRaw(t *testing.T) {
	for _, frames := range []int{64, 128, 256} {
		t.Run(fmt.Sprint(frames), func(t *testing.T) {
			a, _ := simulatedCaptureAudio(frames, zeroAudioSource{})
			a.SetMonitor(studioAudioMonitor{Gain: .5, Mode: "stereo"})
			var raw float32
			r, _ := capture.NewRecorder(8, frames, 2, func(_ capture.RecordedBlock, input [][]float32) error { raw = input[0][0]; return nil })
			if err := a.ArmCapture(r); err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			input, output := [][]float32{make([]float32, frames), make([]float32, frames)}, [][]float32{make([]float32, frames), make([]float32, frames)}
			input[0][0], input[1][0] = .75, -.5
			input[0][40], input[1][40] = .75, -.5
			simulatedCapturePeriod(t, a, 0, input, output)
			if output[0][0] != .375 || output[1][0] != -.25 || a.engineFrame != 0 || r.Snapshot().WrittenFrames != 0 {
				t.Fatal("armed monitor advanced/recorded score or delayed input")
			}
			countIn, _ := capture.NewCountIn(48000, 120000, 1)
			if err := a.BeginCapture(countIn, capture.Calibration{}); err != nil {
				t.Fatal(err)
			}
			if err := a.Play(); err != nil {
				t.Fatal(err)
			}
			simulatedCapturePeriod(t, a, uint64(frames), input, output)
			if output[0][40] != .375 || output[1][40] != -.25 || a.engineFrame != 0 {
				t.Fatal("count-in lost direct monitoring or advanced score")
			}
			if err := a.DisarmCapture(); err != nil {
				t.Fatal(err)
			}
			if math.Float32bits(raw) != math.Float32bits(.75) {
				t.Fatalf("monitorchangedrawPCM: %g", raw)
			}
		})
	}
}

type budgetAudioStream struct {
	simulatedCaptureStream
	stats  audiobackend.Stats
	format audiobackend.Format
}

func (s *budgetAudioStream) Stats() audiobackend.Stats   { return s.stats }
func (s *budgetAudioStream) Format() audiobackend.Format { return s.format }

func TestStudioLowLatencyStatusDistinguishesDeviceReportsFromMeasuredLatency(t *testing.T) {
	a, _ := simulatedCaptureAudio(240, zeroAudioSource{})
	a.requestedPeriodFrames = 64
	a.actual.Latency, a.actual.CaptureLatency, a.actual.Periods = 4*time.Millisecond, 3*time.Millisecond, 2
	a.stream = &budgetAudioStream{format: audiobackend.Format{Priority: "mmcss", HasDeadline: true}, stats: audiobackend.Stats{CallbackMax: 2 * time.Millisecond}}
	snapshot := a.Snapshot()
	if snapshot.RequestedPeriodFrames != 64 || snapshot.PeriodFrames != 240 || snapshot.PeriodMS != 5 || !snapshot.ReportedRoundTripKnown || snapshot.ReportedRoundTripMS != 7 || snapshot.CallbackBudgetPercent != 40 || snapshot.AudioPriority != "mmcss" || !snapshot.DeadlineKnown || snapshot.Warning == "" {
		t.Fatalf("incorrect latency report: %+v", snapshot)
	}
	a.actual.CaptureLatency = 0
	snapshot = a.Snapshot()
	if snapshot.ReportedRoundTripKnown || snapshot.ReportedRoundTripMS != 0 {
		t.Fatal("unknowninputlatencywasfabricated")
	}
	stream := a.stream.(*budgetAudioStream)
	stream.stats.Dropouts = 1
	if got := a.Snapshot().Warning; got != "Audio missed its buffer budget; stop and select a larger buffer." {
		t.Fatalf("miss warning=%q", got)
	}
}
