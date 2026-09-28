//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync/atomic"
	"time"

	"m31labs.dev/tymbal"
)

type tymbalStudioAudio struct {
	reader         io.Reader
	host           tymbal.Host
	stream         *tymbal.Stream
	actual         tymbal.Actual
	playing        atomic.Bool
	monitor        atomic.Pointer[studioAudioMonitor]
	callbackErr    atomic.Pointer[studioAudioCallbackError]
	inputPeakL     atomic.Uint32
	inputPeakR     atomic.Uint32
	outputPeakL    atomic.Uint32
	outputPeakR    atomic.Uint32
	pcm            []byte
	inputName      string
	outputName     string
	startupWarning string
}

type studioAudioCallbackError struct{ message string }

func defaultStudioAudioOptions() studioAudioOptions {
	return studioAudioOptions{InputEnabled: true, MonitorMuted: true, MonitorGain: 0.5, MonitorMode: "stereo"}
}

func enumerateStudioAudio() (studioAudioInventory, error) {
	host, err := tymbalStudioHost()
	if err != nil {
		return studioAudioInventory{Backend: "WASAPI", Supported: true}, err
	}
	devices, err := host.Devices()
	if err != nil {
		return studioAudioInventory{Backend: host.Name(), Supported: true}, err
	}
	inventory := studioAudioInventory{Supported: true, Backend: host.Name(), Devices: make([]studioAudioDeviceInfo, 0, len(devices))}
	for _, device := range devices {
		info := studioAudioDeviceInfo{
			ID: device.ID, Name: device.Name, Inputs: device.Inputs, Outputs: device.Outputs,
			DefaultInput:  device.Default&tymbal.Input != 0,
			DefaultOutput: device.Default&tymbal.Output != 0,
		}
		if len(device.SampleRates) != 0 {
			info.SampleRate = device.SampleRates[0]
		}
		inventory.Devices = append(inventory.Devices, info)
	}
	return inventory, nil
}

func studioAudioSampleRate(options studioAudioOptions) (int, error) {
	options = normalizeStudioAudioOptions(options)
	if err := validateStudioAudioOptions(options); err != nil {
		return 0, err
	}
	host, err := tymbalStudioHost()
	if err != nil {
		return 0, err
	}
	devices, err := host.Devices()
	if err != nil {
		return 0, err
	}
	output, err := selectStudioAudioDevice(host, devices, options.OutputDevice, tymbal.Output)
	if err != nil {
		return 0, err
	}
	if len(output.SampleRates) == 0 || output.SampleRates[0] <= 0 {
		return 0, fmt.Errorf("WASAPI output device %q does not report a mix sample rate", output.Name)
	}
	if options.InputEnabled {
		input, inputErr := selectStudioAudioDevice(host, devices, options.InputDevice, tymbal.Input)
		if inputErr == nil {
			if len(input.SampleRates) == 0 || input.SampleRates[0] != output.SampleRates[0] {
				return 0, fmt.Errorf("WASAPI shared duplex needs matching input/output mix rates; %q is %s Hz and %q is %s Hz", input.Name, sampleRateText(input), output.Name, sampleRateText(output))
			}
		} else if options.InputDevice != "" {
			return 0, inputErr
		}
	}
	return output.SampleRates[0], nil
}

func sampleRateText(device tymbal.Device) string {
	if len(device.SampleRates) == 0 {
		return "unknown"
	}
	return fmt.Sprint(device.SampleRates[0])
}

func openStudioAudio(source io.Reader, options studioAudioOptions) (studioAudioDevice, error) {
	options = normalizeStudioAudioOptions(options)
	if err := validateStudioAudioOptions(options); err != nil {
		return nil, err
	}
	host, err := tymbalStudioHost()
	if err != nil {
		return nil, err
	}
	devices, err := host.Devices()
	if err != nil {
		return nil, err
	}
	output, err := selectStudioAudioDevice(host, devices, options.OutputDevice, tymbal.Output)
	if err != nil {
		return nil, err
	}
	if len(output.SampleRates) == 0 || output.SampleRates[0] <= 0 {
		return nil, fmt.Errorf("WASAPI output device %q does not report a mix sample rate", output.Name)
	}
	var input *tymbal.Device
	warning := ""
	if options.InputEnabled {
		selected, inputErr := selectStudioAudioDevice(host, devices, options.InputDevice, tymbal.Input)
		if inputErr != nil {
			if options.InputDevice != "" {
				return nil, inputErr
			}
			warning = "No default input endpoint is available; running output only."
		} else {
			if len(selected.SampleRates) == 0 || selected.SampleRates[0] != output.SampleRates[0] {
				return nil, fmt.Errorf("WASAPI shared duplex needs matching input/output mix rates; %q is %s Hz and %q is %s Hz", selected.Name, sampleRateText(selected), output.Name, sampleRateText(output))
			}
			input = &selected
		}
	}
	monitor := studioMonitorOptions(options)
	session := &tymbalStudioAudio{reader: source, host: host, outputName: output.Name, startupWarning: warning}
	if input != nil {
		session.inputName = input.Name
	}
	monitorCopy := monitor
	session.monitor.Store(&monitorCopy)
	cfg := tymbal.Config{
		Output: &output, OutChannels: output.Outputs,
		SampleRate: output.SampleRates[0], Period: liveBlockFrames, Periods: 2,
	}
	if input != nil {
		cfg.Input, cfg.InChannels = input, input.Inputs
	}
	var stream *tymbal.Stream
	var openErr error
	periods := []int{liveBlockFrames, 128, 384, 480, 512, 960, 1024}
	for _, period := range periods {
		cfg.Period = period
		stream, openErr = tymbal.Open(host, cfg, session.callback)
		if openErr == nil {
			break
		}
		if input == nil || !errors.Is(openErr, tymbal.ErrFormat) {
			return nil, openErr
		}
	}
	if openErr != nil {
		return nil, fmt.Errorf("could not negotiate a shared WASAPI duplex period: %w", openErr)
	}
	session.stream, session.actual = stream, stream.Actual()
	session.pcm = make([]byte, session.actual.Period*8)
	if err := stream.Start(); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return session, nil
}

func tymbalStudioHost() (tymbal.Host, error) {
	hosts := tymbal.Hosts()
	if len(hosts) == 0 {
		return tymbal.Host{}, fmt.Errorf("Tymbal has no Windows audio backend")
	}
	return hosts[0], nil
}

func selectStudioAudioDevice(host tymbal.Host, devices []tymbal.Device, id string, direction tymbal.Direction) (tymbal.Device, error) {
	if id == "" {
		device, err := host.Default(direction)
		if err != nil {
			return tymbal.Device{}, err
		}
		return device, nil
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
	return tymbal.Device{}, fmt.Errorf("selected %s endpoint %q is no longer available", audioDirectionName(direction), id)
}

func audioDirectionName(direction tymbal.Direction) string {
	if direction == tymbal.Input {
		return "input"
	}
	return "output"
}

func (a *tymbalStudioAudio) callback(_ tymbal.Time, input, output [][]float32) {
	if len(output) == 0 {
		return
	}
	if !a.playing.Load() {
		clearStudioAudioOutput(output)
		a.inputPeakL.Store(0)
		a.inputPeakR.Store(0)
		a.outputPeakL.Store(0)
		a.outputPeakR.Store(0)
		return
	}
	peakL, peakR, _, _ := measureStudioAudioInput(input)
	a.inputPeakL.Store(math.Float32bits(peakL))
	a.inputPeakR.Store(math.Float32bits(peakR))
	monitor := a.monitor.Load()
	if monitor == nil {
		monitor = &studioAudioMonitor{Muted: true}
	}
	if err := renderStudioAudioPeriod(a.reader, a.pcm, input, output, *monitor); err != nil {
		message := "Cicada render failed: " + err.Error()
		if errors.Is(err, io.ErrShortBuffer) {
			message = "WASAPI callback period exceeded its prepared Cicada audio buffer"
		}
		a.callbackErr.CompareAndSwap(nil, &studioAudioCallbackError{message: message})
		a.outputPeakL.Store(0)
		a.outputPeakR.Store(0)
		return
	}
	var outputL, outputR float32
	for channelIndex, channel := range output {
		for _, sample := range channel {
			abs := float32(math.Abs(float64(sample)))
			if channelIndex == 0 && abs > outputL {
				outputL = abs
			}
			if channelIndex == 1 && abs > outputR {
				outputR = abs
			}
		}
	}
	a.outputPeakL.Store(math.Float32bits(outputL))
	a.outputPeakR.Store(math.Float32bits(outputR))
}

func (a *tymbalStudioAudio) Play() error {
	if err := a.Err(); err != nil {
		return err
	}
	a.playing.Store(true)
	return nil
}

func (a *tymbalStudioAudio) Pause() { a.playing.Store(false) }

func (*tymbalStudioAudio) StopClosesDevice() bool { return true }

func (a *tymbalStudioAudio) Err() error {
	if a == nil {
		return nil
	}
	if err := a.callbackErr.Load(); err != nil {
		return errors.New(err.message)
	}
	if a.stream != nil {
		return a.stream.Err()
	}
	return nil
}

func (a *tymbalStudioAudio) Close() error {
	if a == nil || a.stream == nil {
		return nil
	}
	a.Pause()
	return a.stream.Close()
}

func (a *tymbalStudioAudio) SetMonitor(monitor studioAudioMonitor) {
	copy := monitor
	a.monitor.Store(&copy)
}

func (a *tymbalStudioAudio) Snapshot() studioAudioSnapshot {
	if a == nil {
		return studioAudioSnapshot{Backend: "WASAPI"}
	}
	snapshot := studioAudioSnapshot{
		Backend: "WASAPI shared mode", InputName: a.inputName, OutputName: a.outputName,
		InputChannels: a.actual.InChannels, OutputChannels: a.actual.OutChannels,
		SampleRate: a.actual.SampleRate, PeriodFrames: a.actual.Period,
		InputLatencyMS:    float64(a.actual.LatencyIn) / float64(time.Millisecond),
		OutputLatencyMS:   float64(a.actual.LatencyOut) / float64(time.Millisecond),
		InputLatencyKnown: a.actual.LatencyIn > 0, OutputLatencyKnown: a.actual.LatencyOut > 0,
		InputPeakL: math.Float32frombits(a.inputPeakL.Load()), InputPeakR: math.Float32frombits(a.inputPeakR.Load()),
		OutputPeakL: math.Float32frombits(a.outputPeakL.Load()), OutputPeakR: math.Float32frombits(a.outputPeakR.Load()),
		Error: a.startupWarning,
	}
	if err := a.Err(); err != nil {
		snapshot.Error = err.Error()
	}
	if a.stream != nil {
		var stats tymbal.Stats
		a.stream.Stats(&stats)
		snapshot.Callbacks, snapshot.Dropouts, snapshot.Late = stats.Callbacks, stats.Dropouts, stats.Late
		snapshot.CallbackMaxUS = float64(stats.CallbackMax) / float64(time.Microsecond)
	}
	return snapshot
}
