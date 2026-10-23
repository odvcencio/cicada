package main

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/internal/audiobackend"
)

func TestAudioSetupRestoresPreferencesWithoutStartingTransport(t *testing.T) {
	t.Setenv("CICADA_AUDIO_SETTINGS_DIR", t.TempDir())
	path, err := studioAudioSettingsPath(audiobackend.Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	want := studioAudioOptions{InputDevice: "saved-interface-input", OutputDevice: "saved-headphones", InputEnabled: true, MonitorGain: .7, MonitorMode: "mono2", RecordingOffsetMS: 17.5, PeriodFrames: 128}
	if err := saveStudioAudioSettings(path, want); err != nil {
		t.Fatal(err)
	}
	tx := newStudioTransport("unused.cicada")
	tx.initializeAudio(audiobackend.Tymbal)
	got := tx.audioOptions
	if got.InputDevice != want.InputDevice || got.OutputDevice != want.OutputDevice || got.PeriodFrames != want.PeriodFrames || got.RecordingOffsetMS != want.RecordingOffsetMS || got.MonitorGain != want.MonitorGain || got.MonitorMode != want.MonitorMode || !got.MonitorMuted {
		t.Fatalf("saved routing or timing lost: %+v", got)
	}
	driver, err := audiobackend.New(audiobackend.Tymbal)
	if err != nil {
		t.Fatal(err)
	}
	if got.InputEnabled != driver.CaptureSupported() {
		t.Fatalf("restored input ignored capture capability: %+v", got)
	}
	if tx.audio != nil || tx.stream != nil || tx.playing || tx.pendingCapture != nil {
		t.Fatal("restoring routing opened or started audio")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("settings permissions: %v %v", info, err)
		}
	}
}

func TestAudioSetupPersistsOnlySuccessfulChanges(t *testing.T) {
	t.Setenv("CICADA_AUDIO_SETTINGS_DIR", t.TempDir())
	tx := newStudioTransport("unused.cicada")
	tx.initializeAudio(audiobackend.Null)
	options := tx.audioOptions
	options.PeriodFrames, options.MonitorGain, options.MonitorMuted = 512, .8, false
	if err := tx.configureAudio(options); err != nil {
		t.Fatal(err)
	}
	reopened := newStudioTransport("other-song.cicada")
	reopened.initializeAudio(audiobackend.Null)
	if reopened.audioOptions.PeriodFrames != 512 || reopened.audioOptions.MonitorGain != .8 || !reopened.audioOptions.MonitorMuted || reopened.playing || reopened.audio != nil {
		t.Fatalf("restart lost preferences or resumed audio: %+v", reopened.audioOptions)
	}
	before, err := os.ReadFile(tx.audioSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	tx.playing = true
	options.PeriodFrames = 64
	if err := tx.configureAudio(options); err == nil {
		t.Fatal("routing changed during playback")
	}
	tx.playing = false
	options.MonitorGain = 3
	if err := tx.configureAudio(options); err == nil {
		t.Fatal("invalid settings were saved")
	}
	after, _ := os.ReadFile(tx.audioSettingsPath)
	if !bytes.Equal(before, after) {
		t.Fatal("rejected configuration changed the saved setup")
	}

	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	tx.audioSettingsPath = filepath.Join(blocked, "null.json")
	options = tx.audioOptions
	options.PeriodFrames = 1024
	if err := tx.configureAudio(options); !errors.Is(err, errStudioAudioSettings) || tx.audioOptions.PeriodFrames != 512 {
		t.Fatalf("failed save changed active settings: %v %+v", err, tx.audioOptions)
	}
	response := studioCall(t, http.HandlerFunc((&studio{transport: tx}).audioConfig), "/api/audio/config", options)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "could not be saved") {
		t.Fatalf("failed save reported success: %d %s", response.Code, response.Body)
	}
}

func TestAudioSetupCorruptionIsVisibleAndPreservedUntilApply(t *testing.T) {
	for _, invalid := range []string{`{broken`, `{"version":1,"options":null}`, `{"version":2,"options":{}}`, `{"version":1,"options":{"periodFrames":3}}`, `{"version":1,"options":{}} {}`, strings.Repeat("x", 16385)} {
		t.Run(invalid[:min(len(invalid), 20)], func(t *testing.T) {
			t.Setenv("CICADA_AUDIO_SETTINGS_DIR", t.TempDir())
			path, _ := studioAudioSettingsPath(audiobackend.Null)
			if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
				t.Fatal(err)
			}
			tx := newStudioTransport("unused.cicada")
			tx.initializeAudio(audiobackend.Null)
			if tx.audioSettingsWarning == "" || tx.audioState().SettingsWarning == "" || tx.audioOptions.PeriodFrames != 256 {
				t.Fatal("unreadable setup was silently accepted")
			}
			data, _ := os.ReadFile(path)
			if string(data) != invalid {
				t.Fatal("startup overwrote the unreadable setup")
			}
			if err := tx.configureAudio(tx.audioOptions); err != nil || tx.audioSettingsWarning != "" {
				t.Fatalf("explicit apply did not replace the broken setup: %v", err)
			}
		})
	}
}

func TestAudioSetupBackendIsolationAndConcurrentReaders(t *testing.T) {
	t.Setenv("CICADA_AUDIO_SETTINGS_DIR", t.TempDir())
	path, _ := studioAudioSettingsPath(audiobackend.Tymbal)
	want := defaultStudioAudioOptionsFor(audiobackend.Tymbal)
	want.OutputDevice, want.PeriodFrames = "keep-native-headphones", 128
	if err := saveStudioAudioSettings(path, want); err != nil {
		t.Fatal(err)
	}
	other, _ := studioAudioSettingsPath(audiobackend.Oto)
	if err := saveStudioAudioSettings(other, defaultStudioAudioOptionsFor(audiobackend.Oto)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for j := 0; j < 10; j++ {
				if err := saveStudioAudioSettings(path, want); err != nil {
					t.Error(err)
				}
			}
		})
	}
	for i := 0; i < 80; i++ {
		got, err := loadStudioAudioSettings(path, studioAudioOptions{})
		if err != nil || got.OutputDevice != want.OutputDevice || got.PeriodFrames != want.PeriodFrames {
			t.Fatalf("reader saw a partial or unrelated setup: %+v %v", got, err)
		}
	}
	wg.Wait()
}
