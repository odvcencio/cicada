package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/internal/audiobackend"
)

func TestInputAuthorizationWaitDoesNotLockTransportAndRechecksPlayback(t *testing.T) {
	for _, outcome := range []string{"denied", "canceled", "playback-started", "granted"} {
		t.Run(outcome, func(t *testing.T) {
			options := normalizeStudioAudioOptions(studioAudioOptions{MonitorMuted: true})
			tx := &studioTransport{audioOptions: options}
			changed := options
			changed.InputEnabled = true
			inventory := func(string) (studioAudioInventory, error) {
				return studioAudioInventory{Supported: true, CaptureSupported: true}, nil
			}
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			denied := errors.New("microphone access denied")
			request := func(ctx context.Context, _ string) error {
				close(entered)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-release:
					if outcome == "denied" {
						return denied
					}
					return nil
				}
			}
			done := make(chan error, 1)
			go func() { done <- tx.configureAudioAuthorized(ctx, changed, inventory, request) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("permission was not requested")
			}
			// Runtime polling remains responsive while macOS awaits a human.
			runtime := make(chan studioAudioRuntimeState, 1)
			go func() { runtime <- tx.audioRuntimeState() }()
			select {
			case state := <-runtime:
				if state.Options != options || state.Playing {
					t.Fatalf("pending permission changed transport: %+v", state)
				}
			case <-time.After(time.Second):
				t.Fatal("permission prompt locked transport")
			}
			if outcome == "canceled" {
				cancel()
			} else {
				if outcome == "playback-started" {
					tx.mu.Lock()
					tx.playing = true
					tx.mu.Unlock()
				}
				release <- struct{}{}
			}
			select {
			case err := <-done:
				switch outcome {
				case "denied":
					if !errors.Is(err, denied) {
						t.Fatal(err)
					}
				case "canceled":
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case "playback-started":
					if err == nil || !strings.Contains(err.Error(), "stop playback") {
						t.Fatal(err)
					}
				case "granted":
					if err != nil {
						t.Fatal(err)
					}
				}
			case <-time.After(time.Second):
				t.Fatal("authorization did not finish")
			}
			if outcome == "granted" {
				if tx.audioOptions != changed || tx.audio != nil || tx.playing || tx.pendingCapture != nil {
					t.Fatal("permission opened or armed capture")
				}
			} else if tx.audioOptions != options {
				t.Fatal("failed permission changed saved routing")
			}
		})
	}
}

type permissionPlaybackStream struct {
	simulatedCaptureStream
	format audiobackend.Format
}

func (s *permissionPlaybackStream) Format() audiobackend.Format { return s.format }

type permissionPlaybackBackend struct {
	audiobackend.Backend
	access   audiobackend.InputAccess
	config   audiobackend.Config
	requests int
	stream   permissionPlaybackStream
}

func (*permissionPlaybackBackend) CaptureSupported() bool { return true }
func (b *permissionPlaybackBackend) InputAccess() (audiobackend.InputAccess, error) {
	return b.access, nil
}
func (b *permissionPlaybackBackend) RequestInputAccess(context.Context) error {
	b.requests++
	return nil
}
func (b *permissionPlaybackBackend) Open(config audiobackend.Config, _ audiobackend.Callback) (audiobackend.Stream, error) {
	b.config = config
	b.stream.format = audiobackend.Format{SampleRate: config.SampleRate, FramesPerPeriod: config.FramesPerPeriod, Channels: 2, CaptureChannels: config.CaptureChannels}
	return &b.stream, nil
}

func TestMissingInputPermissionKeepsPlaybackUsableAndNeverArmsASilentTake(t *testing.T) {
	for _, access := range []audiobackend.InputAccess{audiobackend.InputAccessPrompt, audiobackend.InputAccessDenied, audiobackend.InputAccessRestricted, audiobackend.InputAccessUnavailable} {
		t.Run(string(access), func(t *testing.T) {
			backend := &permissionPlaybackBackend{access: access}
			options := normalizeStudioAudioOptions(studioAudioOptions{InputEnabled: true, MonitorMuted: true, MonitorMode: "mono1"})
			audio, err := openStudioAudioBackend(zeroAudioSource{}, options, "tymbal", 48000, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer audio.Close()
			if backend.config.CaptureChannels != 0 || backend.requests != 0 || backend.stream.starts != 0 || audio.Snapshot().Warning == "" || audio.Snapshot().Error != "" || audio.Snapshot().InputChannels != 0 {
				t.Fatalf("permission opened input or hid fallback: %+v %+v", backend, audio.Snapshot())
			}
			if err := audio.Play(); err != nil || backend.stream.starts != 1 {
				t.Fatalf("permission blocked playback: %v", err)
			}
			recorder, err := capture.NewRecorder(4, 256, 1, func(capture.RecordedBlock, [][]float32) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer recorder.Close()
			if err := audio.ArmCapture(recorder); err == nil || audio.Armed() || backend.stream.starts != 1 {
				t.Fatalf("missing input armed a silent take: %v", err)
			}
		})
	}
}
