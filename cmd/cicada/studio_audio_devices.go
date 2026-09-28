package main

import (
	"fmt"
	"io"
	"math"
)

type studioAudioOptions struct {
	InputDevice  string  `json:"inputDevice"`
	OutputDevice string  `json:"outputDevice"`
	InputEnabled bool    `json:"inputEnabled"`
	MonitorMuted bool    `json:"monitorMuted"`
	MonitorGain  float32 `json:"monitorGain"`
	MonitorMode  string  `json:"monitorMode"`
}

type studioAudioDeviceInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Inputs        int    `json:"inputs"`
	Outputs       int    `json:"outputs"`
	SampleRate    int    `json:"sampleRate"`
	DefaultInput  bool   `json:"defaultInput"`
	DefaultOutput bool   `json:"defaultOutput"`
}

type studioAudioInventory struct {
	Supported bool                    `json:"supported"`
	Backend   string                  `json:"backend"`
	Message   string                  `json:"message,omitempty"`
	Devices   []studioAudioDeviceInfo `json:"devices"`
}

type studioAudioSnapshot struct {
	Backend            string  `json:"backend"`
	InputName          string  `json:"inputName,omitempty"`
	OutputName         string  `json:"outputName,omitempty"`
	InputChannels      int     `json:"inputChannels"`
	OutputChannels     int     `json:"outputChannels"`
	SampleRate         int     `json:"sampleRate"`
	PeriodFrames       int     `json:"periodFrames"`
	InputLatencyMS     float64 `json:"inputLatencyMs"`
	OutputLatencyMS    float64 `json:"outputLatencyMs"`
	InputLatencyKnown  bool    `json:"inputLatencyKnown"`
	OutputLatencyKnown bool    `json:"outputLatencyKnown"`
	InputPeakL         float32 `json:"inputPeakL"`
	InputPeakR         float32 `json:"inputPeakR"`
	OutputPeakL        float32 `json:"outputPeakL"`
	OutputPeakR        float32 `json:"outputPeakR"`
	Dropouts           uint64  `json:"dropouts"`
	Late               uint64  `json:"late"`
	Callbacks          uint64  `json:"callbacks"`
	CallbackMaxUS      float64 `json:"callbackMaxUs"`
	Error              string  `json:"error,omitempty"`
}

type studioAudioState struct {
	Inventory studioAudioInventory `json:"inventory"`
	Options   studioAudioOptions   `json:"options"`
	Runtime   studioAudioSnapshot  `json:"runtime"`
	Playing   bool                 `json:"playing"`
}

func (t *studioTransport) ensureSampleRate() (int, error) {
	t.mu.Lock()
	if t.sampleRate > 0 {
		rate := t.sampleRate
		t.mu.Unlock()
		return rate, nil
	}
	if t.audioNull {
		t.sampleRate = liveSampleRate
		t.mu.Unlock()
		return liveSampleRate, nil
	}
	if t.audioOptions == (studioAudioOptions{}) {
		t.audioOptions = defaultStudioAudioOptions()
	}
	options := normalizeStudioAudioOptions(t.audioOptions)
	t.mu.Unlock()
	rate, err := studioAudioSampleRate(options)
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	if t.sampleRate == 0 {
		t.sampleRate = rate
	}
	rate = t.sampleRate
	t.mu.Unlock()
	return rate, nil
}

func (t *studioTransport) audioState() studioAudioState {
	inventory, err := enumerateStudioAudio()
	if err != nil {
		inventory.Message = err.Error()
	}
	t.mu.Lock()
	options := t.audioOptions
	if options == (studioAudioOptions{}) {
		options = defaultStudioAudioOptions()
	}
	options = normalizeStudioAudioOptions(options)
	state := studioAudioState{Inventory: inventory, Options: options, Playing: t.playing}
	if t.audio != nil {
		state.Runtime = t.audio.Snapshot()
	} else {
		state.Runtime.Backend = inventory.Backend
		state.Runtime.SampleRate = t.sampleRate
		if !inventory.Supported {
			state.Runtime.OutputName = "System default"
			state.Runtime.OutputChannels = 2
			if state.Runtime.SampleRate == 0 {
				state.Runtime.SampleRate = liveSampleRate
			}
		}
	}
	if state.Runtime.Error == "" {
		state.Runtime.Error = t.errText
	}
	t.mu.Unlock()
	return state
}

func (t *studioTransport) configureAudio(options studioAudioOptions) error {
	options = normalizeStudioAudioOptions(options)
	if err := validateStudioAudioOptions(options); err != nil {
		return err
	}
	inventory, err := enumerateStudioAudio()
	if err != nil {
		return err
	}
	if err := validateStudioAudioDevices(inventory, options); err != nil {
		return err
	}
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	t.mu.Lock()
	defer t.mu.Unlock()
	routeChanged := options.InputEnabled != t.audioOptions.InputEnabled || options.InputDevice != t.audioOptions.InputDevice || options.OutputDevice != t.audioOptions.OutputDevice
	if t.playing && routeChanged {
		return fmt.Errorf("stop playback before changing audio devices")
	}
	t.audioOptions = options
	if t.audio != nil {
		t.audio.SetMonitor(studioMonitorOptions(options))
	}
	if !routeChanged {
		return nil
	}
	if t.audio != nil {
		t.audio.Pause()
		if err := t.audio.Close(); err != nil {
			t.audio = nil
			return err
		}
		t.audio = nil
	}
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	if t.stream != nil {
		t.stream.Close()
		t.stream = nil
	}
	t.sampleRate = 0
	t.playing = false
	t.pending = false
	t.pendingSong = ""
	t.pendingSongID = 0
	t.scene = ""
	t.activeSlots = nil
	t.stoppedTracks = nil
	t.errText = ""
	return nil
}

type studioAudioMonitor struct {
	Muted bool
	Gain  float32
	Mode  string
}

type studioAudioDevice interface {
	Play() error
	Pause()
	StopClosesDevice() bool
	Err() error
	Close() error
	SetMonitor(studioAudioMonitor)
	Snapshot() studioAudioSnapshot
}

func normalizeStudioAudioOptions(options studioAudioOptions) studioAudioOptions {
	if options.MonitorMode == "" {
		options.MonitorMode = "stereo"
	}
	return options
}

func studioMonitorOptions(options studioAudioOptions) studioAudioMonitor {
	return studioAudioMonitor{Muted: options.MonitorMuted, Gain: options.MonitorGain, Mode: options.MonitorMode}
}

func validateStudioAudioOptions(options studioAudioOptions) error {
	if math.IsNaN(float64(options.MonitorGain)) || math.IsInf(float64(options.MonitorGain), 0) || options.MonitorGain < 0 || options.MonitorGain > 2 {
		return fmt.Errorf("input monitor gain must be between 0 and 2")
	}
	switch options.MonitorMode {
	case "stereo", "mono1", "mono2":
	default:
		return fmt.Errorf("input monitor channel mode must be stereo, mono1, or mono2")
	}
	return nil
}

func validateStudioAudioDevices(inventory studioAudioInventory, options studioAudioOptions) error {
	if !inventory.Supported {
		if options.InputEnabled || options.InputDevice != "" || options.OutputDevice != "" {
			return fmt.Errorf("this audio backend uses the system default output and does not expose selectable input devices")
		}
		return nil
	}
	if options.OutputDevice != "" {
		if _, ok := findStudioAudioDevice(inventory, options.OutputDevice, false); !ok {
			return fmt.Errorf("selected output device is no longer available")
		}
	}
	if options.InputEnabled && options.InputDevice != "" {
		if _, ok := findStudioAudioDevice(inventory, options.InputDevice, true); !ok {
			return fmt.Errorf("selected input device is no longer available")
		}
	}
	return nil
}

func findStudioAudioDevice(inventory studioAudioInventory, id string, input bool) (studioAudioDeviceInfo, bool) {
	for _, device := range inventory.Devices {
		if device.ID != id {
			continue
		}
		if input && device.Inputs > 0 || !input && device.Outputs > 0 {
			return device, true
		}
		return studioAudioDeviceInfo{}, false
	}
	return studioAudioDeviceInfo{}, false
}

func clampStudioAudioSample(value float32) float32 {
	if math.IsNaN(float64(value)) {
		return 0
	}
	if value > 1 {
		return 1
	}
	if value < -1 {
		return -1
	}
	return value
}

func clearStudioAudioOutput(output [][]float32) {
	for _, channel := range output {
		clear(channel)
	}
}

func renderStudioAudioPeriod(reader io.Reader, pcm []byte, input, output [][]float32, monitor studioAudioMonitor) error {
	clearStudioAudioOutput(output)
	if len(output) == 0 {
		return nil
	}
	need := len(output[0]) * 8
	if need > len(pcm) {
		return io.ErrShortBuffer
	}
	if _, err := io.ReadFull(reader, pcm[:need]); err != nil {
		return err
	}
	mixStudioAudio(pcm[:need], input, output, monitor)
	return nil
}

// mixStudioAudio adds a monitored input to the existing stereo Cicada render.
// All buffers are borrowed, period-sized slices and are reused by the driver.
func mixStudioAudio(pcm []byte, input, output [][]float32, monitor studioAudioMonitor) {
	if len(output) == 0 {
		return
	}
	frames := len(output[0])
	if len(pcm) < frames*8 {
		return
	}
	for frame := 0; frame < frames; frame++ {
		left := math.Float32frombits(uint32(pcm[frame*8]) | uint32(pcm[frame*8+1])<<8 | uint32(pcm[frame*8+2])<<16 | uint32(pcm[frame*8+3])<<24)
		right := math.Float32frombits(uint32(pcm[frame*8+4]) | uint32(pcm[frame*8+5])<<8 | uint32(pcm[frame*8+6])<<16 | uint32(pcm[frame*8+7])<<24)
		if len(output) == 1 {
			output[0][frame] = clampStudioAudioSample((left + right) * 0.5)
		} else {
			output[0][frame] = clampStudioAudioSample(left)
			output[1][frame] = clampStudioAudioSample(right)
		}
		if monitor.Muted || monitor.Gain == 0 || len(input) == 0 {
			continue
		}
		var inLeft, inRight float32
		switch monitor.Mode {
		case "mono1":
			if len(input[0]) > frame {
				inLeft, inRight = input[0][frame], input[0][frame]
			}
		case "mono2":
			if len(input) > 1 && len(input[1]) > frame {
				inLeft, inRight = input[1][frame], input[1][frame]
			}
		default:
			if len(input[0]) > frame {
				inLeft = input[0][frame]
			}
			if len(input) > 1 && len(input[1]) > frame {
				inRight = input[1][frame]
			} else {
				inRight = inLeft
			}
		}
		if len(output) == 1 {
			output[0][frame] = clampStudioAudioSample(output[0][frame] + (inLeft+inRight)*0.5*monitor.Gain)
		} else {
			output[0][frame] = clampStudioAudioSample(output[0][frame] + inLeft*monitor.Gain)
			output[1][frame] = clampStudioAudioSample(output[1][frame] + inRight*monitor.Gain)
		}
	}
}

func measureStudioAudioInput(input [][]float32) (peakL, peakR, rmsL, rmsR float32) {
	if len(input) == 0 {
		return 0, 0, 0, 0
	}
	frames := len(input[0])
	if frames == 0 {
		return 0, 0, 0, 0
	}
	var sumL, sumR float64
	for frame := 0; frame < frames; frame++ {
		left := input[0][frame]
		right := left
		if len(input) > 1 && len(input[1]) > frame {
			right = input[1][frame]
		}
		if abs := float32(math.Abs(float64(left))); abs > peakL {
			peakL = abs
		}
		if abs := float32(math.Abs(float64(right))); abs > peakR {
			peakR = abs
		}
		sumL += float64(left) * float64(left)
		sumR += float64(right) * float64(right)
	}
	rmsL = float32(math.Sqrt(sumL / float64(frames)))
	rmsR = float32(math.Sqrt(sumR / float64(frames)))
	return peakL, peakR, rmsL, rmsR
}
