//go:build linux || windows

package audiobackend

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"

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
		return nil, true, err
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
	for _, period := range periods {
		cfg := tymbal.Config{
			Output: &output, OutChannels: config.Channels,
			SampleRate: config.SampleRate, Period: period, Periods: 2,
		}
		if input != nil {
			cfg.Input, cfg.InChannels = input, inputChannels
		}
		nativeStream, openErr = tymbal.Open(b.host, cfg, tymbalCallback(render, callbackErr))
		if openErr == nil {
			break
		}
		if input == nil || !errors.Is(openErr, tymbal.ErrFormat) {
			break
		}
	}
	if openErr != nil {
		return nil, tymbalOpenError(output.Name, input != nil, openErr)
	}
	actual := nativeStream.Actual()
	captureName := ""
	if input != nil {
		captureName = input.Name
	}
	return &tymbalStream{
		stream:      nativeStream,
		callbackErr: callbackErr,
		format: Format{
			Backend: Tymbal, Device: output.Name, CaptureDevice: captureName, SampleRate: actual.SampleRate,
			Channels: actual.OutChannels, CaptureChannels: actual.InChannels,
			FramesPerPeriod: actual.Period, Latency: actual.LatencyOut, CaptureLatency: actual.LatencyIn,
		},
	}, nil
}

func tymbalOpenError(device string, duplex bool, err error) error {
	if errors.Is(err, tymbal.ErrBusy) && runtime.GOOS == "linux" {
		return fmt.Errorf("Tymbal ALSA device %q is busy; use --audio oto: %w", device, err)
	}
	if duplex && errors.Is(err, tymbal.ErrFormat) {
		return fmt.Errorf("could not negotiate a shared Tymbal duplex period: %w", err)
	}
	return err
}

func tymbalCallback(render Callback, callbackErr *atomic.Pointer[audioError]) tymbal.Callback {
	return func(_ tymbal.Time, input, output [][]float32) {
		if err := render(input, output); err != nil {
			callbackErr.CompareAndSwap(nil, &audioError{err: err})
			for _, channel := range output {
				clear(channel)
			}
		}
	}
}

func (b tymbalBackend) device(id string, direction tymbal.Direction) (tymbal.Device, error) {
	if id == "" {
		device, err := b.host.Default(direction)
		if err != nil {
			return tymbal.Device{}, err
		}
		return device, nil
	}
	devices, err := b.host.Devices()
	if err != nil {
		return tymbal.Device{}, err
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
	return tymbal.Device{}, fmt.Errorf("selected %s device %q is no longer available", kind, id)
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
	return s.stream.Start()
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
