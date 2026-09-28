package main

import (
	"fmt"
	"io"
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/internal/audiobackend"
)

var studioMutedMonitor = studioAudioMonitor{Muted: true}

func defaultStudioAudioOptions() studioAudioOptions {
	return defaultStudioAudioOptionsFor(audiobackend.DefaultFor("studio"))
}

func defaultStudioAudioOptionsFor(name audiobackend.Name) studioAudioOptions {
	return studioAudioOptions{
		InputEnabled: runtime.GOOS == "windows" && name == audiobackend.Tymbal,
		MonitorMuted: true, MonitorGain: 0.5, MonitorMode: "stereo",
	}
}

func enumerateStudioAudio(backendName string) (studioAudioInventory, error) {
	backend, err := audiobackend.New(audiobackend.Name(backendName))
	if err != nil {
		return studioAudioInventory{}, err
	}
	devices, supported, err := backend.Devices()
	inventory := studioAudioInventory{Backend: studioAudioBackendLabel(audiobackend.Name(backendName)), Supported: supported}
	if err != nil {
		return inventory, err
	}
	if !supported {
		inventory.Message = "This backend uses the system default output and does not expose selectable devices."
		return inventory, nil
	}
	inventory.Devices = make([]studioAudioDeviceInfo, 0, len(devices))
	for _, device := range devices {
		info := studioAudioDeviceInfo{
			ID: device.ID, Name: device.Name, Inputs: device.Inputs, Outputs: device.Outputs,
			DefaultInput: device.DefaultInput, DefaultOutput: device.DefaultOutput,
		}
		if len(device.SampleRates) != 0 {
			info.SampleRate = device.SampleRates[0]
		}
		inventory.Devices = append(inventory.Devices, info)
	}
	return inventory, nil
}

func studioAudioSampleRate(backendName string, options studioAudioOptions) (int, error) {
	options = normalizeStudioAudioOptions(effectiveStudioAudioOptions(backendName, options))
	if err := validateStudioAudioOptions(options); err != nil {
		return 0, err
	}
	backend, err := audiobackend.New(audiobackend.Name(backendName))
	if err != nil {
		return 0, err
	}
	captureChannels := 0
	if options.InputEnabled {
		captureChannels = 2
	}
	return backend.SampleRate(audiobackend.Config{
		Channels: 2, FramesPerPeriod: liveBlockFrames,
		Device: options.OutputDevice, CaptureDevice: options.InputDevice,
		CaptureChannels: captureChannels, AllowMissingInput: true,
	})
}

func openStudioAudio(source io.Reader, options studioAudioOptions, backendName string, sampleRate int) (studioAudioDevice, error) {
	options = normalizeStudioAudioOptions(effectiveStudioAudioOptions(backendName, options))
	if err := validateStudioAudioOptions(options); err != nil {
		return nil, err
	}
	backend, err := audiobackend.New(audiobackend.Name(backendName))
	if err != nil {
		return nil, err
	}
	captureChannels := 0
	if options.InputEnabled {
		captureChannels = 2
	}
	session := &backendStudioAudio{
		reader:     source,
		formatName: studioAudioBackendLabel(audiobackend.Name(backendName)),
		outputName: "System default",
		rawOutput:  backendName != string(audiobackend.Tymbal),
	}
	monitor := studioMonitorOptions(options)
	session.monitorState.Store(&monitor)
	config := audiobackend.Config{
		SampleRate: sampleRate, Channels: 2, FramesPerPeriod: liveBlockFrames,
		Device: options.OutputDevice, CaptureDevice: options.InputDevice,
		CaptureChannels: captureChannels, AllowMissingInput: true,
	}
	stream, err := backend.Open(config, session.renderPeriod)
	if err != nil {
		return nil, err
	}
	session.stream = stream
	session.actual = stream.Format()
	session.pcm = make([]byte, session.actual.FramesPerPeriod*8)
	if session.actual.Device != "" {
		session.outputName = session.actual.Device
	}
	if session.actual.CaptureDevice != "" {
		session.inputName = session.actual.CaptureDevice
	}
	if options.InputEnabled && session.actual.CaptureChannels == 0 {
		session.startupWarning = "No default input endpoint is available; running output only."
	}
	return session, nil
}

func effectiveStudioAudioOptions(backendName string, options studioAudioOptions) studioAudioOptions {
	if backendName == string(audiobackend.Null) {
		options.InputEnabled = false
		options.InputDevice = ""
		options.OutputDevice = ""
	}
	return options
}

type backendStudioAudio struct {
	reader         io.Reader
	stream         audiobackend.Stream
	actual         audiobackend.Format
	rawOutput      bool
	playing        atomic.Bool
	monitorState   atomic.Pointer[studioAudioMonitor]
	inputPeakL     atomic.Uint32
	inputPeakR     atomic.Uint32
	outputPeakL    atomic.Uint32
	outputPeakR    atomic.Uint32
	pcm            []byte
	formatName     string
	inputName      string
	outputName     string
	startupWarning string
}

func (a *backendStudioAudio) renderPeriod(input, output [][]float32) error {
	if len(output) == 0 {
		return nil
	}
	if !a.playing.Load() {
		clearStudioAudioOutput(output)
		a.inputPeakL.Store(0)
		a.inputPeakR.Store(0)
		a.outputPeakL.Store(0)
		a.outputPeakR.Store(0)
		return nil
	}
	peakL, peakR, _, _ := measureStudioAudioInput(input)
	a.inputPeakL.Store(math.Float32bits(peakL))
	a.inputPeakR.Store(math.Float32bits(peakR))
	monitor := a.monitorState.Load()
	if monitor == nil {
		monitor = &studioMutedMonitor
	}
	var err error
	if a.rawOutput {
		err = renderLivePlayPeriod(a.reader, a.pcm, output)
	} else {
		err = renderStudioAudioPeriod(a.reader, a.pcm, input, output, *monitor)
	}
	if err != nil {
		clearStudioAudioOutput(output)
		return err
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
	return nil
}

func (a *backendStudioAudio) Play() error {
	if err := a.Err(); err != nil {
		return err
	}
	a.playing.Store(true)
	if err := a.stream.Start(); err != nil {
		a.playing.Store(false)
		return err
	}
	return nil
}

func (a *backendStudioAudio) Pause() {
	if a != nil {
		a.playing.Store(false)
		if a.stream != nil {
			a.stream.Pause()
		}
	}
}

func (a *backendStudioAudio) StopClosesDevice() bool {
	return a != nil && a.stream != nil && a.stream.StopClosesDevice()
}

func (a *backendStudioAudio) Err() error {
	if a == nil || a.stream == nil {
		return nil
	}
	return a.stream.Err()
}

func (a *backendStudioAudio) Close() error {
	if a == nil || a.stream == nil {
		return nil
	}
	a.playing.Store(false)
	return a.stream.Close()
}

func (a *backendStudioAudio) SetMonitor(monitor studioAudioMonitor) {
	if a == nil {
		return
	}
	copy := monitor
	a.monitorState.Store(&copy)
}

func (a *backendStudioAudio) Snapshot() studioAudioSnapshot {
	if a == nil {
		return studioAudioSnapshot{}
	}
	snapshot := studioAudioSnapshot{
		Backend: a.formatName, InputName: a.inputName, OutputName: a.outputName,
		InputChannels: a.actual.CaptureChannels, OutputChannels: a.actual.Channels,
		SampleRate: a.actual.SampleRate, PeriodFrames: a.actual.FramesPerPeriod,
		InputLatencyMS:    float64(a.actual.CaptureLatency) / float64(time.Millisecond),
		OutputLatencyMS:   float64(a.actual.Latency) / float64(time.Millisecond),
		InputLatencyKnown: a.actual.CaptureLatency > 0, OutputLatencyKnown: a.actual.Latency > 0,
		InputPeakL: math.Float32frombits(a.inputPeakL.Load()), InputPeakR: math.Float32frombits(a.inputPeakR.Load()),
		OutputPeakL: math.Float32frombits(a.outputPeakL.Load()), OutputPeakR: math.Float32frombits(a.outputPeakR.Load()),
		Error: a.startupWarning,
	}
	if a.stream != nil {
		stats := a.stream.Stats()
		snapshot.Callbacks, snapshot.Dropouts, snapshot.Late = stats.Callbacks, stats.Dropouts, stats.Late
		snapshot.CallbackMaxUS = float64(stats.CallbackMax) / float64(time.Microsecond)
	}
	if err := a.Err(); err != nil {
		snapshot.Error = err.Error()
	}
	return snapshot
}

func studioAudioBackendLabel(name audiobackend.Name) string {
	switch name {
	case audiobackend.Tymbal:
		if runtime.GOOS == "windows" {
			return "Tymbal WASAPI shared mode"
		}
		return "Tymbal ALSA"
	case audiobackend.Oto:
		return "Oto"
	case audiobackend.Null:
		return "Null"
	default:
		return fmt.Sprint(name)
	}
}
