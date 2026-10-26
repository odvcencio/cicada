package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/kernel/fx/ampcab"
)

var studioMutedMonitor = studioAudioMonitor{Muted: true}

func defaultStudioAudioOptions() studioAudioOptions {
	return defaultStudioAudioOptionsFor(audiobackend.DefaultFor("studio"))
}

func defaultStudioAudioOptionsFor(name audiobackend.Name) studioAudioOptions {
	return defaultStudioAudioOptionsForOS(name, runtime.GOOS)
}

func defaultStudioAudioOptionsForOS(name audiobackend.Name, goos string) studioAudioOptions {
	return studioAudioOptions{
		InputEnabled: goos == "windows" && name == audiobackend.Tymbal,
		MonitorMuted: true, MonitorGain: 0.5, MonitorMode: "stereo",
		PeriodFrames: liveBlockFrames,
		AmpDrive:     .35, AmpTone: .5,
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
		CaptureSupported: backend.CaptureSupported(),
		BufferChoices:    []int{64, 128, 256, 512, 1024},
	}
	if err != nil {
		return inventory, err
	}
	inventory.InputAccess, err = audiobackend.QueryInputAccess(backend)
	if err != nil {
		inventory.InputMessage = err.Error()
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
			MinPeriod: device.MinPeriod, MaxPeriod: device.MaxPeriod,
			DefaultPeriod: device.DefaultPeriod, FundamentalPeriod: device.FundamentalPeriod,
		}
		if len(device.SampleRates) != 0 {
			info.SampleRate = device.SampleRates[0]
		}
		inventory.Devices = append(inventory.Devices, info)
	}
	return inventory, nil
}

func requestStudioInputAccess(ctx context.Context, backendName string) error {
	backend, err := audiobackend.New(audiobackend.Name(backendName))
	if err != nil {
		return err
	}
	return audiobackend.RequestInputAccess(ctx, backend)
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
	if options.InputEnabled && studioInputAccessError(backend) == nil {
		captureChannels = 2
	}
	return backend.SampleRate(audiobackend.Config{
		Channels: 2, FramesPerPeriod: options.PeriodFrames,
		Device: options.OutputDevice, CaptureDevice: options.InputDevice,
		CaptureChannels: captureChannels, AllowMissingInput: true,
	})
}

func studioInputAccessError(backend audiobackend.Backend) error {
	access, err := audiobackend.QueryInputAccess(backend)
	if err != nil {
		return fmt.Errorf("microphone access could not be checked: %w", err)
	}
	switch access {
	case audiobackend.InputAccessGranted:
		return nil
	case audiobackend.InputAccessPrompt:
		return fmt.Errorf("enable input and apply audio settings to allow microphone access")
	case audiobackend.InputAccessDenied, audiobackend.InputAccessRestricted:
		return fmt.Errorf("allow Cicada Studio microphone access in System Settings > Privacy & Security > Microphone")
	default:
		return fmt.Errorf("recording input is unavailable with this audio backend")
	}
}

// Recording is strict about input. Playback may continue output-only while
// permission is missing; that must never create or arm a silent take.
func openStudioRecordingAudio(source io.Reader, options studioAudioOptions, backendName string, sampleRate int) (studioAudioDevice, error) {
	backend, err := audiobackend.New(audiobackend.Name(backendName))
	if err != nil {
		return nil, err
	}
	if err := studioInputAccessError(backend); err != nil {
		return nil, err
	}
	return openStudioAudio(source, options, backendName, sampleRate)
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
	return openStudioAudioBackend(source, options, backendName, sampleRate, backend)
}

func openStudioAudioBackend(source io.Reader, options studioAudioOptions, backendName string, sampleRate int, backend audiobackend.Backend) (studioAudioDevice, error) {
	captureChannels := 0
	var inputWarning string
	if options.InputEnabled {
		if err := studioInputAccessError(backend); err != nil {
			inputWarning = err.Error() + "; playback is running without recording input."
		} else {
			captureChannels = 2
		}
	}
	session := &backendStudioAudio{
		reader:                source,
		formatName:            studioAudioBackendLabel(audiobackend.Name(backendName)),
		outputName:            "System default",
		rawOutput:             backendName != string(audiobackend.Tymbal),
		requestedPeriodFrames: options.PeriodFrames,
		startupWarning:        inputWarning,
	}
	session.engineEpoch = studioEngineEpoch.Add(1)
	monitor := studioMonitorOptions(options)
	{
		var err error
		monitor.amp, err = ampcab.New(sampleRate, studioAmpParams(monitor))
		if err != nil && monitor.AmpEnabled {
			return nil, fmt.Errorf("amp monitor: %w", err)
		}
	}
	session.monitorState.Store(&monitor)
	config := audiobackend.Config{
		SampleRate: sampleRate, Channels: 2, FramesPerPeriod: options.PeriodFrames,
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
	if monitor.AmpEnabled && session.actual.SampleRate != sampleRate {
		_ = stream.Close()
		return nil, fmt.Errorf("amp monitor needs the requested %d Hz; device granted %d Hz", sampleRate, session.actual.SampleRate)
	}
	if !monitor.AmpEnabled && session.actual.SampleRate != sampleRate {
		monitor.amp, _ = ampcab.New(session.actual.SampleRate, studioAmpParams(monitor))
		session.monitorState.Store(&monitor)
	}
	session.pcm = make([]byte, session.actual.FramesPerPeriod*8)
	if session.actual.Device != "" {
		session.outputName = session.actual.Device
	}
	if session.actual.CaptureDevice != "" {
		session.inputName = session.actual.CaptureDevice
	}
	if options.InputEnabled && session.actual.CaptureChannels == 0 && session.startupWarning == "" {
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
	reader                io.Reader
	stream                audiobackend.Stream
	actual                audiobackend.Format
	rawOutput             bool
	playing               atomic.Bool
	monitorState          atomic.Pointer[studioAudioMonitor]
	inputPeakL            atomic.Uint32
	inputPeakR            atomic.Uint32
	inputHeldL            atomic.Uint32
	inputHeldR            atomic.Uint32
	inputSession          atomic.Uint64
	captureDeviceEpoch    atomic.Uint64
	outputPeakL           atomic.Uint32
	outputPeakR           atomic.Uint32
	pcm                   []byte
	formatName            string
	inputName             string
	outputName            string
	startupWarning        string
	lifeMu                sync.Mutex // control side only
	active                bool
	armed                 atomic.Pointer[capture.Recorder]
	captureChannel        atomic.Uint32 // 0 = device layout; 1/2 = selected mono
	source                atomic.Pointer[studioRenderSource]
	rendering             atomic.Int64
	engineEpoch           uint64 // callback owned
	engineFrame           int64  // callback owned
	currentSource         *studioRenderSource
	periodCapture         *capture.Recorder
	periodPlaying         bool
	timingSeen            bool
	requestedPeriodFrames int
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
	epoch := uint64(0)
	if capture.Place(b).Confidence != capture.TimingUnavailable {
		epoch = b.DeviceEpoch
	}
	a.captureDeviceEpoch.Store(epoch)
	a.timingSeen = true
	a.adoptSource()
	a.periodPlaying = a.playing.Load()
	a.periodCapture = a.armed.Load()
	b.EngineEpoch, b.EngineFrame = a.engineEpoch, a.engineFrame
	if a.periodCapture != nil && a.periodPlaying {
		// Keep the selected instrument input dry. A mono selection is duplicated
		// in the retained stereo take so its reamp matches the monitored source.
		mode := a.captureChannel.Load()
		if mode > 0 && len(input) >= 2 {
			ch := int(mode - 1)
			selected := [2][]float32{input[ch], input[ch]}
			a.periodCapture.Capture(b, selected[:])
		} else {
			a.periodCapture.Capture(b, input)
		}
	}
}

func (a *backendStudioAudio) CaptureDeviceEpoch() uint64 {
	return a.captureDeviceEpoch.Load()
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
	monitor := a.monitorState.Load()
	if monitor == nil {
		monitor = &studioMutedMonitor
	}
	if monitor.amp != nil {
		// The callback exclusively owns processor state; control changes arrive
		// as immutable snapshots and the processor smooths its parameters.
		_ = monitor.amp.SetParams(studioAmpParams(*monitor))
	}
	if !playing {
		clearStudioAudioOutput(output)
		a.measureInput(input)
		if a.periodCapture != nil && !a.rawOutput {
			mixStudioMonitor(input, output, *monitor)
		}
		a.measureOutput(output)
		return nil
	}
	a.measureInput(input)
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
		err = renderStudioAudioPeriod(reader, a.pcm, nil, tail[:min(len(output), 2)], studioMutedMonitor)
	}
	if err != nil {
		if a.periodCapture != nil {
			a.periodCapture.MarkIncomplete()
		}
		clearStudioAudioOutput(output)
		return err
	}
	if !a.rawOutput {
		// Monitor all input, including warmup and count-in, without moving the
		// score or adding a software queue ahead of the device period.
		mixStudioMonitor(input, output, *monitor)
	}
	a.engineFrame += int64(frames)
	a.measureOutput(output)
	return nil
}

func (a *backendStudioAudio) measureOutput(output [][]float32) {
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

func (a *backendStudioAudio) Play() error {
	a.lifeMu.Lock()
	defer a.lifeMu.Unlock()
	if err := a.Err(); err != nil {
		return err
	}
	if !a.playing.Swap(true) {
		a.resetInputMeter()
	}
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
	if a.actual.CaptureChannels == 0 && a.startupWarning != "" {
		return fmt.Errorf("%s", a.startupWarning)
	}
	if !recorder.FormatFits(a.actual.FramesPerPeriod, a.actual.CaptureChannels) {
		return fmt.Errorf("capture ring must match the device's mono/stereo input and actual period")
	}
	if a.Armed() {
		return fmt.Errorf("capture is already armed")
	}
	if err := a.Err(); err != nil {
		return err
	}
	channel := uint32(0)
	if m := a.monitorState.Load(); m != nil && m.AmpEnabled {
		switch m.Mode {
		case "mono1":
			channel = 1
		case "mono2":
			channel = 2
		}
	}
	if channel == 2 && a.actual.CaptureChannels < 2 {
		return fmt.Errorf("instrument input 2 is unavailable on this device")
	}
	a.captureChannel.Store(channel)
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
		a.resetInputMeter()
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
	previous := a.monitorState.Load()
	if previous != nil && previous.amp != nil {
		monitor.amp = previous.amp
	} else {
		monitor.amp, _ = ampcab.New(a.actual.SampleRate, studioAmpParams(monitor))
	}
	copy := monitor
	a.monitorState.Store(&copy)
}

func studioAmpParams(m studioAudioMonitor) ampcab.Params {
	return ampcab.Params{Drive: float64(m.AmpDrive), Tone: float64(m.AmpTone), Output: float64(m.AmpOutput), Cabinet: m.AmpCabinet, Bypass: !m.AmpEnabled}
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
		InputHeldL: math.Float32frombits(a.inputHeldL.Load()), InputHeldR: math.Float32frombits(a.inputHeldR.Load()), InputSession: a.inputSession.Load(),
		OutputPeakL: math.Float32frombits(a.outputPeakL.Load()), OutputPeakR: math.Float32frombits(a.outputPeakR.Load()),
	}
	if m := a.monitorState.Load(); m != nil && m.amp != nil && !a.rawOutput && a.actual.CaptureChannels > 0 {
		snapshot.MonitorFXLatencyFrames = ampcab.Latency
	}
	if a.stream != nil {
		format := a.stream.Format()
		snapshot.AudioPriority, snapshot.DeadlineKnown = format.Priority, format.HasDeadline
		stats := a.stream.Stats()
		snapshot.Callbacks, snapshot.Dropouts, snapshot.Late = stats.Callbacks, stats.Dropouts, stats.Late
		snapshot.CallbackMaxUS = float64(stats.CallbackMax) / float64(time.Microsecond)
	}
	snapshot.RequestedPeriodFrames, snapshot.DevicePeriods = a.requestedPeriodFrames, a.actual.Periods
	if snapshot.SampleRate > 0 && snapshot.PeriodFrames > 0 {
		snapshot.PeriodMS = float64(snapshot.PeriodFrames) * 1000 / float64(snapshot.SampleRate)
		snapshot.CallbackBudgetPercent = snapshot.CallbackMaxUS / (snapshot.PeriodMS * 10)
	}
	snapshot.ReportedRoundTripKnown = snapshot.InputChannels > 0 && snapshot.InputLatencyKnown && snapshot.OutputLatencyKnown
	if snapshot.ReportedRoundTripKnown {
		snapshot.ReportedRoundTripMS = snapshot.InputLatencyMS + snapshot.OutputLatencyMS
	}
	if a.requestedPeriodFrames > 0 && a.actual.FramesPerPeriod > a.requestedPeriodFrames {
		snapshot.Warning = fmt.Sprintf("Device negotiated %d frames for the requested %d-frame buffer.", a.actual.FramesPerPeriod, a.requestedPeriodFrames)
	}
	if snapshot.Dropouts > 0 || snapshot.Late > 0 || snapshot.CallbackBudgetPercent >= 100 {
		snapshot.Warning = "Audio missed its buffer budget; stop and select a larger buffer."
	}
	if a.startupWarning != "" {
		if snapshot.Warning != "" {
			snapshot.Warning += " "
		}
		snapshot.Warning += a.startupWarning
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
	return studioAudioBackendLabelForOS(name, runtime.GOOS)
}

func studioAudioBackendLabelForOS(name audiobackend.Name, goos string) string {
	switch name {
	case audiobackend.Tymbal:
		switch goos {
		case "darwin":
			return "Tymbal CoreAudio"
		case "windows":
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
	return studioAudioHostForOS(name, runtime.GOOS)
}

func studioAudioHostForOS(name audiobackend.Name, goos string) string {
	if name != audiobackend.Tymbal {
		return ""
	}
	switch goos {
	case "windows":
		return "wasapi"
	case "linux":
		return "alsa"
	case "darwin":
		return "coreaudio"
	}
	return ""
}
