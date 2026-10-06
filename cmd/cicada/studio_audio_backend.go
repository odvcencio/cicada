package main

import (
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/host/capture"
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
	name := audiobackend.Name(backendName)
	inventory := studioAudioInventory{
		Backend: studioAudioBackendLabel(name), BackendName: string(name), Host: studioAudioHost(name), Supported: supported,
	}
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
	session.engineEpoch = studioEngineEpoch.Add(1)
	monitor := studioMonitorOptions(options)
	session.monitorState.Store(&monitor)
	config := audiobackend.Config{
		SampleRate: sampleRate, Channels: 2, FramesPerPeriod: liveBlockFrames,
		Device: options.OutputDevice, CaptureDevice: options.InputDevice,
		CaptureChannels: captureChannels, AllowMissingInput: true,
		Capture: session.captureInput,
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
	lifeMu         sync.Mutex // control side only
	active         bool
	armed          atomic.Pointer[capture.Recorder]
	source         atomic.Pointer[studioRenderSource]
	rendering      atomic.Int64
	engineEpoch    uint64 // callback owned
	engineFrame    int64  // callback owned
	currentSource  *studioRenderSource
	periodCapture  *capture.Recorder
	periodPlaying  bool
	timingSeen     bool
}

var studioEngineEpoch atomic.Uint64

type studioRenderSource struct {
	reader io.Reader
	epoch  uint64
}

// SetSource publishes a prepared render source. The callback adopts it at the
// next block boundary, including a new engine epoch when Stop returns to home.
func (a *backendStudioAudio) SetSource(reader io.Reader) {
	a.source.Store(&studioRenderSource{reader: reader, epoch: studioEngineEpoch.Add(1)})
}

func (a *backendStudioAudio) captureInput(b capture.Block, input [][]float32) {
	a.rendering.Add(1)
	a.timingSeen = true
	a.adoptSource()
	a.periodPlaying = a.playing.Load()
	a.periodCapture = a.armed.Load()
	b.EngineEpoch, b.EngineFrame = a.engineEpoch, a.engineFrame
	if a.periodCapture != nil && a.periodPlaying {
		a.periodCapture.Capture(b, input)
	}
}

func (a *backendStudioAudio) adoptSource() {
	if source := a.source.Load(); source != nil && source != a.currentSource {
		a.currentSource = source
		a.engineEpoch, a.engineFrame = source.epoch, 0
	}
}

func (a *backendStudioAudio) renderPeriod(input, output [][]float32) error {
	if !a.timingSeen {
		a.rendering.Add(1)
		a.adoptSource()
	}
	defer a.rendering.Add(-1)
	if len(output) == 0 {
		return nil
	}
	playing := a.playing.Load()
	if a.timingSeen {
		playing = a.periodPlaying
	}
	if !playing {
		clearStudioAudioOutput(output)
		peakL, peakR, _, _ := measureStudioAudioInput(input)
		a.inputPeakL.Store(math.Float32bits(peakL))
		a.inputPeakR.Store(math.Float32bits(peakR))
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
	reader := a.reader
	if a.currentSource != nil {
		reader = a.currentSource.reader
	}
	clearStudioAudioOutput(output)
	delay := 0
	if a.periodCapture != nil {
		delay = a.periodCapture.LeadIn(output)
	}
	frames := len(output[0]) - delay
	var tail [2][]float32
	for ch := range min(len(output), 2) {
		tail[ch] = output[ch][delay:]
	}
	if frames > 0 && a.rawOutput {
		err = renderLivePlayPeriod(reader, a.pcm, tail[:min(len(output), 2)])
	} else if frames > 0 {
		var monitored [2][]float32
		for ch := range min(len(input), 2) {
			monitored[ch] = input[ch][min(delay, len(input[ch])):]
		}
		err = renderStudioAudioPeriod(reader, a.pcm, monitored[:min(len(input), 2)], tail[:min(len(output), 2)], *monitor)
	}
	if err != nil {
		if a.periodCapture != nil {
			a.periodCapture.MarkIncomplete()
		}
		clearStudioAudioOutput(output)
		return err
	}
	a.engineFrame += int64(frames)
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
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	if err := a.Err(); err != nil {
		return err
	}
	a.playing.Store(true)
	if err := a.startDevice(); err != nil {
		a.playing.Store(false)
		return err
	}
	return nil
}

func (a *backendStudioAudio) startDevice() error {
	if a.active {
		return nil
	}
	if err := a.stream.Start(); err != nil {
		return err
	}
	a.active = true
	return nil
}

func (a *backendStudioAudio) Armed() bool { return a != nil && a.armed.Load() != nil }

func (a *backendStudioAudio) ArmCapture(recorder *capture.Recorder) error {
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	if !recorder.FormatFits(a.actual.FramesPerPeriod, a.actual.CaptureChannels) {
		return fmt.Errorf("capture ring must match the device's mono/stereo input and actual period")
	}
	if a.Armed() {
		return fmt.Errorf("capture is already armed")
	}
	if err := a.Err(); err != nil {
		return err
	}
	a.armed.Store(recorder)
	if err := a.startDevice(); err != nil {
		a.armed.Store(nil)
		return err
	}
	return nil
}

func (a *backendStudioAudio) DisarmCapture() error {
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	recorder := a.armed.Swap(nil)
	if !a.playing.Load() && a.active {
		a.stream.Pause()
		a.active = false
	}
	if recorder != nil {
		return recorder.Close()
	}
	return nil
}

func (a *backendStudioAudio) BeginCapture(countIn capture.CountIn, calibration capture.Calibration) error {
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	if recorder := a.armed.Load(); recorder != nil {
		return recorder.Begin(countIn, calibration)
	}
	return fmt.Errorf("arm capture before recording")
}

func (a *backendStudioAudio) Pause() {
	if a != nil {
		a.lifeMu.Lock()
		defer a.lifeMu.Unlock()
		a.playing.Store(false)
		if recorder := a.armed.Load(); recorder != nil {
			recorder.End()
		}
		for a.rendering.Load() != 0 {
			time.Sleep(time.Millisecond)
		}
		if a.stream != nil && !a.Armed() {
			a.stream.Pause()
			a.active = false
		}
	}
}

func (a *backendStudioAudio) StopClosesDevice() bool {
	return a != nil && a.stream != nil && a.stream.StopClosesDevice() && !a.Armed()
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
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	a.playing.Store(false)
	err := a.stream.Close()
	a.active = false
	if recorder := a.armed.Swap(nil); recorder != nil {
		if captureErr := recorder.Close(); err == nil {
			err = captureErr
		}
	}
	return err
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
		Backend: a.formatName, BackendName: string(a.actual.Backend), Host: a.actual.Host,
		InputName: a.inputName, OutputName: a.outputName,
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
	if recorder := a.armed.Load(); recorder != nil {
		state := recorder.Snapshot()
		snapshot.Armed, snapshot.Capture = true, &state
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

func studioAudioHost(name audiobackend.Name) string {
	if name != audiobackend.Tymbal {
		return ""
	}
	if runtime.GOOS == "windows" {
		return "wasapi"
	}
	if runtime.GOOS == "linux" {
		return "alsa"
	}
	return ""
}
