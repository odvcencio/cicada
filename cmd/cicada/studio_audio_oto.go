//go:build !windows

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/ebitengine/oto/v3"
)

type otoStudioAudio struct {
	device *oto.Context
	player *oto.Player
}

func defaultStudioAudioOptions() studioAudioOptions {
	return studioAudioOptions{MonitorMuted: true, MonitorGain: 0.5, MonitorMode: "stereo"}
}

func enumerateStudioAudio() (studioAudioInventory, error) {
	return studioAudioInventory{
		Backend: "Oto",
		Message: "Native device selection and input monitoring are available in the Windows Studio build; this build uses Oto's system-default output.",
	}, nil
}

func studioAudioSampleRate(options studioAudioOptions) (int, error) {
	if err := validateStudioAudioOptions(normalizeStudioAudioOptions(options)); err != nil {
		return 0, err
	}
	if options.InputEnabled || options.InputDevice != "" || options.OutputDevice != "" {
		return 0, fmt.Errorf("this audio backend uses the system default output and does not expose selectable input devices")
	}
	return liveSampleRate, nil
}

func openStudioAudio(source io.Reader, options studioAudioOptions) (studioAudioDevice, error) {
	if _, err := studioAudioSampleRate(options); err != nil {
		return nil, err
	}
	device, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate: liveSampleRate, ChannelCount: 2, Format: oto.FormatFloat32LE,
		BufferSize: 20 * time.Millisecond, ApplicationName: "Cicada Studio",
	})
	if err != nil {
		return nil, err
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("audio device did not become ready within 10 seconds")
	}
	if err := device.Err(); err != nil {
		return nil, err
	}
	player := device.NewPlayer(source)
	player.SetBufferSize(liveBlockFrames * 8 * 4)
	return &otoStudioAudio{device: device, player: player}, nil
}

func (a *otoStudioAudio) Play() error {
	if err := a.Err(); err != nil {
		return err
	}
	a.player.Play()
	return nil
}

func (a *otoStudioAudio) Pause() {
	if a != nil && a.player != nil {
		a.player.PauseAndStopReading()
	}
}

func (*otoStudioAudio) StopClosesDevice() bool { return false }

func (a *otoStudioAudio) Err() error {
	if a == nil {
		return nil
	}
	if a.device != nil {
		if err := a.device.Err(); err != nil {
			return err
		}
	}
	if a.player != nil {
		return a.player.Err()
	}
	return nil
}

func (a *otoStudioAudio) Close() error {
	if a == nil || a.player == nil {
		return nil
	}
	a.Pause()
	return a.player.Close()
}

func (*otoStudioAudio) SetMonitor(studioAudioMonitor) {}

func (a *otoStudioAudio) Snapshot() studioAudioSnapshot {
	return studioAudioSnapshot{Backend: "Oto", OutputName: "System default", OutputChannels: 2, SampleRate: liveSampleRate}
}
