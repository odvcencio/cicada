package main

import (
	"net/http"
	"os"
	"runtime"
	"testing"

	"m31labs.dev/cicada/internal/audiobackend"
)

func TestStudioAudioConfigRemembersOnlyAcceptedChanges(t *testing.T) {
	isolateState(t)
	s := newTakeStudio(t, t.TempDir())
	s.transport.audioNull = true
	options := defaultStudioAudioOptionsFor(audiobackend.Null)
	options.PeriodFrames = 128
	for _, period := range []int{128, 64} {
		options.PeriodFrames = period
		response := studioCall(t, s.routes(), "/api/audio/config", options)
		if response.Code != http.StatusOK {
			t.Fatalf("apply settings: %d %s", response.Code, response.Body)
		}
		loaded, err := loadStudioAudioPreferences(audiobackend.Null)
		if err != nil || loaded != options {
			t.Fatalf("accepted settings not remembered: %+v, %v", loaded, err)
		}
	}
	s.transport.playing = true
	changed := options
	changed.PeriodFrames = 256
	response := studioCall(t, s.routes(), "/api/audio/config", changed)
	if response.Code != http.StatusConflict {
		t.Fatalf("changed buffer during playback: %d %s", response.Code, response.Body)
	}
	loaded, err := loadStudioAudioPreferences(audiobackend.Null)
	if err != nil || loaded != options {
		t.Fatalf("refused changes replaced saved settings: %+v, %v", loaded, err)
	}
}

func TestStudioAudioPreferencesSurviveReopenAndKeepDisconnectedRoutes(t *testing.T) {
	isolateState(t)
	options := normalizeStudioAudioOptions(studioAudioOptions{
		InputDevice: "usb-microphone", OutputDevice: "headphones", InputEnabled: true,
		MonitorMuted: true, MonitorMode: "mono1", MonitorGain: 0.7,
		PeriodFrames: 128, RecordingOffsetMS: 12.5,
	})
	if err := saveStudioAudioPreferences(audiobackend.Tymbal, options); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadStudioAudioPreferences(audiobackend.Tymbal)
	if err != nil || loaded != options {
		t.Fatalf("reopened audio settings=%+v, err=%v; want %+v", loaded, err, options)
	}
	// Running a silent test or using the fallback output must not change the
	// native recording setup the next real session restores.
	if err := saveStudioAudioPreferences(audiobackend.Null, defaultStudioAudioOptionsFor(audiobackend.Null)); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadStudioAudioPreferences(audiobackend.Tymbal)
	if err != nil || loaded != options {
		t.Fatalf("silent backend changed native preferences: %+v, %v", loaded, err)
	}
	path, _ := studioAudioPreferencesPath(audiobackend.Tymbal)
	info, err := os.Stat(path)
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("audio settings permissions=%v, %v", info, err)
	}
}

func TestStudioAudioPreferencesRejectDamagedSettingsWithoutOverwritingThem(t *testing.T) {
	isolateState(t)
	options := defaultStudioAudioOptionsFor(audiobackend.Tymbal)
	if err := saveStudioAudioPreferences(audiobackend.Tymbal, options); err != nil {
		t.Fatal(err)
	}
	path, _ := studioAudioPreferencesPath(audiobackend.Tymbal)
	for _, data := range []string{
		`{"version":1,"options":{"periodFrames":3}}`,
		`{"version":50,"options":{}}`,
		`{"version":1`,
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := loadStudioAudioPreferences(audiobackend.Tymbal)
		if err == nil || got != options {
			t.Fatalf("invalid settings accepted: %+v, %v", got, err)
		}
		unchanged, _ := os.ReadFile(path)
		if string(unchanged) != data {
			t.Fatal("damaged settings were overwritten")
		}
	}
}
