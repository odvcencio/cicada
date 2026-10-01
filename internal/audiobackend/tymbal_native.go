//go:build linux || windows

package audiobackend

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/tymbal"
)

type tymbalBackend struct{ host tymbal.Host }

func newTymbalBackend() (Backend, error) {
	hosts := tymbal.Hosts()
	if len(hosts) == 0 {
		return nil, fmt.Errorf("Tymbal has no native audio backend on this platform")
	}
	return tymbalBackend{host: hosts[0]}, nil
}

func (tymbalBackend) Name() Name { return Tymbal }

func (b tymbalBackend) Devices() ([]Device, bool, error) {
	native, err := b.host.Devices()
	if err != nil {
		return nil, true, tymbalDeviceErrorForOS(runtime.GOOS, tymbal.Output, "", err)
	}
	devices := make([]Device, 0, len(native))
	for _, device := range native {
		devices = append(devices, Device{
			ID: device.ID, Name: device.Name, Inputs: device.Inputs, Outputs: device.Outputs,
			SampleRates:  append([]int(nil), device.SampleRates...),
			DefaultInput: device.Default&tymbal.Input != 0, DefaultOutput: device.Default&tymbal.Output != 0,
		})
	}
	return devices, true, nil
}

func (b tymbalBackend) SampleRate(config Config) (int, error) {
	output, err := b.device(config.Device, tymbal.Output)
	if err != nil {
		return 0, err
	}
	rate, err := preferredDeviceRate(output)
	if err != nil {
		return 0, err
	}
	if config.CaptureChannels > 0 {
		input, inputErr := b.device(config.CaptureDevice, tymbal.Input)
		if inputErr == nil {
			inputRate, rateErr := preferredDeviceRate(input)
			if rateErr != nil {
				return 0, rateErr
			}
			if inputRate != rate {
				return 0, fmt.Errorf("Tymbal shared duplex needs matching input/output rates; %q is %d Hz and %q is %d Hz", input.Name, inputRate, output.Name, rate)
			}
		} else if !config.AllowMissingInput || config.CaptureDevice != "" {
			return 0, inputErr
		}
	}
	if config.SampleRate > 0 && config.SampleRate != rate {
		return 0, fmt.Errorf("Tymbal output device %q uses %d Hz; requested %d Hz", output.Name, rate, config.SampleRate)
	}
	return rate, nil
}

func (b tymbalBackend) Open(config Config, render Callback) (Stream, error) {
	if err := audioConfigError(config); err != nil {
		return nil, err
	}
	if render == nil {
		return nil, fmt.Errorf("audio render callback is required")
	}
	output, err := b.device(config.Device, tymbal.Output)
	if err != nil {
		return nil, err
	}
	if output.Outputs < config.Channels {
		return nil, fmt.Errorf("Tymbal output device %q has %d channels; %d requested", output.Name, output.Outputs, config.Channels)
	}
	if config.SampleRate <= 0 {
		return nil, fmt.Errorf("Tymbal output sample rate must be positive")
	}
	actualRate, err := preferredDeviceRate(output)
	if err != nil {
		return nil, err
	}
	if actualRate != config.SampleRate {
		return nil, fmt.Errorf("Tymbal output device %q uses %d Hz; requested %d Hz", output.Name, actualRate, config.SampleRate)
	}

	var input *tymbal.Device
	inputChannels := 0
	if config.CaptureChannels > 0 {
		selected, inputErr := b.device(config.CaptureDevice, tymbal.Input)
		if inputErr != nil {
			if config.CaptureDevice != "" || !config.AllowMissingInput {
				return nil, inputErr
			}
		} else {
			inputRate, rateErr := preferredDeviceRate(selected)
			if rateErr != nil {
				return nil, rateErr
			}
			if inputRate != config.SampleRate {
				return nil, fmt.Errorf("Tymbal shared duplex needs matching input/output rates; %q is %d Hz and %q is %d Hz", selected.Name, inputRate, output.Name, config.SampleRate)
			}
			input = &selected
			inputChannels = config.CaptureChannels
			if inputChannels > selected.Inputs {
				inputChannels = selected.Inputs
			}
		}
	}

	callbackErr := &atomic.Pointer[audioError]{}
	periods := []int{config.FramesPerPeriod}
	if input != nil {
		periods = []int{config.FramesPerPeriod, 128, 384, 480, 512, 960, 1024}
	}
	var nativeStream *tymbal.Stream
	var openErr error
	timing := &tymbalTiming{tap: config.Capture, epoch: deviceEpoch.Add(1)}
	for _, period := range periods {
		cfg := tymbal.Config{
			Output: &output, OutChannels: config.Channels,
			SampleRate: config.SampleRate, Period: period, Periods: 2,
		}
		if input != nil {
			cfg.Input, cfg.InChannels = input, inputChannels
		}
		nativeStream, openErr = tymbal.Open(b.host, cfg, tymbalTimedCallback(render, callbackErr, timing))
		if openErr == nil {
			break
		}
		if input == nil || !errors.Is(openErr, tymbal.ErrFormat) {
			break
		}
	}
	if openErr != nil {
		captureName := ""
		if input != nil {
			captureName = input.Name
		}
		return nil, tymbalOpenError(output.Name, captureName, openErr)
	}
	actual := nativeStream.Actual()
	timing.actual = actual
	captureName := ""
	if input != nil {
		captureName = input.Name
	}
	return &tymbalStream{
		stream:      nativeStream,
		callbackErr: callbackErr,
		format: Format{
			Backend: Tymbal, Host: b.host.Name(), Device: output.Name, CaptureDevice: captureName, SampleRate: actual.SampleRate,
			Channels: actual.OutChannels, CaptureChannels: actual.InChannels,
			FramesPerPeriod: actual.Period, Latency: actual.LatencyOut, CaptureLatency: actual.LatencyIn,
		},
	}, nil
}

func tymbalOpenError(outputDevice, captureDevice string, err error) error {
	if captureDevice != "" {
		if errors.Is(err, tymbal.ErrFormat) {
			err = fmt.Errorf("could not negotiate a shared duplex period: %w", err)
		}
		return tymbalDuplexDeviceErrorForOS(runtime.GOOS, outputDevice, captureDevice, err)
	}
	return tymbalDeviceErrorForOS(runtime.GOOS, tymbal.Output, outputDevice, err)
}

func tymbalDuplexDeviceErrorForOS(goos, outputDevice, captureDevice string, err error) error {
	if err == nil {
		return nil
	}
	backend := "WASAPI"
	if goos == "linux" {
		backend = "ALSA"
	}
	detail := oneLineAudioError(err.Error())
	message := "could not be opened"
	if errors.Is(err, tymbal.ErrBusy) {
		message = "could not be opened because one endpoint is busy"
	}
	return fmt.Errorf("Tymbal %s playback device %q and capture device %q %s; use --audio oto: %s", backend, oneLineAudioError(outputDevice), oneLineAudioError(captureDevice), message, detail)
}

func tymbalDeviceErrorForOS(goos string, direction tymbal.Direction, device string, err error) error {
	if err == nil {
		return nil
	}
	backend := "WASAPI"
	if goos == "linux" {
		backend = "ALSA"
	}
	kind := "playback"
	if direction == tymbal.Input {
		kind = "capture"
	}
	if goos == "linux" && direction == tymbal.Output && device == "" && strings.Contains(strings.ToLower(err.Error()), "no default device") {
		return errors.New("no ALSA playback device found; use --audio oto")
	}
	if device == "" {
		device = "default " + kind + " device"
	}
	if errors.Is(err, tymbal.ErrBusy) {
		return fmt.Errorf("Tymbal %s device %q is busy; use --audio oto", backend, oneLineAudioError(device))
	}
	return fmt.Errorf("Tymbal %s device %q could not be opened; use --audio oto: %s", backend, oneLineAudioError(device), oneLineAudioError(err.Error()))
}

func oneLineAudioError(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}

var deviceEpoch atomic.Uint64

type tymbalTiming struct {
	tap    func(capture.Block, [][]float32)
	epoch  uint64
	actual tymbal.Actual
}

func tymbalCallback(render Callback, callbackErr *atomic.Pointer[audioError]) tymbal.Callback {
	return tymbalTimedCallback(render, callbackErr, nil)
}

func tymbalTimedCallback(render Callback, callbackErr *atomic.Pointer[audioError], timing *tymbalTiming) tymbal.Callback {
	// The failure cell is prepared outside the callback, including the fault path.
	failure := &audioError{}
	return func(t tymbal.Time, input, output [][]float32) {
		if timing != nil && timing.tap != nil {
			timing.tap(timing.block(t, input, output), input)
		}
		if err := render(input, output); err != nil {
			if callbackErr.Load() == nil {
				failure.err = err
				callbackErr.Store(failure)
			}
			for _, channel := range output {
				clear(channel)
			}
		}
	}
}

func (s *tymbalTiming) block(t tymbal.Time, input, output [][]float32) capture.Block {
	frames := s.actual.Period
	if len(output) > 0 {
		frames = len(output[0])
	} else if len(input) > 0 {
		frames = len(input[0])
	}
	outRef := capture.ReferenceFirstFrame
	// WASAPI exposes an audio-clock observation, not the queued render frame.
	if t.OutputFrequency != 0 {
		outRef = capture.ReferenceClockObservation
	}
	b := capture.Block{
		DeviceEpoch: s.epoch, DeviceFrame: t.Frame,
		InputPosition:  capture.DevicePosition{Position: t.InputPosition, Frequency: t.InputFrequency, Valid: t.InputFrequency != 0},
		OutputPosition: capture.DevicePosition{Position: t.OutputPosition, Frequency: t.OutputFrequency, Valid: t.OutputFrequency != 0},
		InputTime:      capture.Timestamp{Nano: t.InputNano, Valid: t.InputNano != 0, Domain: capture.ClockHostMonotonic, Reference: capture.ReferenceFirstFrame},
		OutputTime:     capture.Timestamp{Nano: t.OutputNano, Valid: t.OutputNano != 0, Domain: capture.ClockHostMonotonic, Reference: outRef},
		SampleRate:     s.actual.SampleRate, Period: s.actual.Period, Frames: frames,
		InputLatencyNano: int64(s.actual.LatencyIn), OutputLatencyNano: int64(s.actual.LatencyOut),
		InputLatencyValid: s.actual.LatencyIn > 0, OutputLatencyValid: s.actual.LatencyOut > 0,
		Layout: capture.ChannelLayout(len(input)), DeviceDropouts: t.Dropouts,
	}
	if t.Discontinuity {
		b.Flags |= capture.DeviceDiscontinuity
	}
	return b
}

func (b tymbalBackend) device(id string, direction tymbal.Direction) (tymbal.Device, error) {
	if id == "" {
		device, err := b.host.Default(direction)
		if err != nil {
			return tymbal.Device{}, tymbalDeviceErrorForOS(runtime.GOOS, direction, "", err)
		}
		return device, nil
	}
	devices, err := b.host.Devices()
	if err != nil {
		return tymbal.Device{}, tymbalDeviceErrorForOS(runtime.GOOS, direction, id, err)
	}
	for _, device := range devices {
		if device.ID != id {
			continue
		}
		if direction == tymbal.Input && device.Inputs == 0 || direction == tymbal.Output && device.Outputs == 0 {
			break
		}
		return device, nil
	}
	kind := "output"
	if direction == tymbal.Input {
		kind = "input"
	}
	return tymbal.Device{}, tymbalDeviceErrorForOS(runtime.GOOS, direction, id, fmt.Errorf("selected %s device %q is no longer available", kind, id))
}

func preferredDeviceRate(device tymbal.Device) (int, error) {
	if len(device.SampleRates) == 0 || device.SampleRates[0] <= 0 {
		return 0, fmt.Errorf("Tymbal device %q does not report a sample rate", device.Name)
	}
	return device.SampleRates[0], nil
}

type tymbalStream struct {
	stream      *tymbal.Stream
	callbackErr *atomic.Pointer[audioError]
	format      Format
	started     atomic.Bool
	closed      atomic.Bool
}

func (s *tymbalStream) Start() error {
	if s == nil || s.closed.Load() {
		return fmt.Errorf("Tymbal stream is closed")
	}
	if failure := s.callbackErr.Load(); failure != nil {
		return failure.err
	}
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("Tymbal stream cannot be restarted after it stops")
	}
	if err := s.stream.Start(); err != nil {
		return tymbalDeviceErrorForOS(runtime.GOOS, tymbal.Output, s.format.Device, err)
	}
	return nil
}

func (s *tymbalStream) Pause() {
	if s == nil || s.stream == nil || !s.started.Load() {
		return
	}
	_ = s.stream.Stop()
}

func (*tymbalStream) StopClosesDevice() bool { return true }

func (s *tymbalStream) Err() error {
	if s == nil {
		return nil
	}
	if failure := s.callbackErr.Load(); failure != nil {
		return failure.err
	}
	return s.stream.Err()
}

func (s *tymbalStream) Close() error {
	if s == nil || !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	return s.stream.Close()
}

func (s *tymbalStream) Format() Format { return s.format }

func (s *tymbalStream) Stats() Stats {
	if s == nil || s.stream == nil {
		return Stats{}
	}
	var native tymbal.Stats
	s.stream.Stats(&native)
	return Stats{Callbacks: native.Callbacks, Dropouts: native.Dropouts, Late: native.Late, CallbackMax: native.CallbackMax}
}
