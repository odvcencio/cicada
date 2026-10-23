package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/internal/appdirs"
	"m31labs.dev/cicada/internal/audiobackend"
)

var errStudioAudioSettings = errors.New("audio setup could not be saved")

type studioAudioSettings struct {
	Version int                 `json:"version"`
	Options *studioAudioOptions `json:"options"`
}

// Routing belongs to this computer, not to a song. Separate backend files keep
// a temporary fallback from erasing the native device choices.
func studioAudioSettingsPath(backend audiobackend.Name) (string, error) {
	switch backend {
	case audiobackend.Tymbal, audiobackend.Oto, audiobackend.Null:
	default:
		return "", fmt.Errorf("unknown audio backend %q", backend)
	}
	if dir := os.Getenv("CICADA_AUDIO_SETTINGS_DIR"); dir != "" {
		return filepath.Abs(filepath.Join(dir, string(backend)+".json"))
	}
	dir, err := appdirs.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "audio-"+string(backend)+".json"), nil
}

func loadStudioAudioSettings(path string, defaults studioAudioOptions) (studioAudioOptions, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	defer file.Close()
	const limit = 16 << 10
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return defaults, err
	}
	if len(data) > limit {
		return defaults, fmt.Errorf("saved audio setup is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var saved studioAudioSettings
	if err := decoder.Decode(&saved); err != nil {
		return defaults, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return defaults, fmt.Errorf("saved audio setup contains trailing data")
	}
	if saved.Version != 1 {
		return defaults, fmt.Errorf("unsupported audio setup version %d", saved.Version)
	}
	if saved.Options == nil {
		return defaults, fmt.Errorf("saved audio setup has no options")
	}
	options := normalizeStudioAudioOptions(*saved.Options)
	if err := validateStudioAudioOptions(options); err != nil {
		return defaults, err
	}
	// An enabled input is a routing preference, not an instruction to record.
	// Transport and capture are never restored; monitoring starts muted.
	options.MonitorMuted = true
	return options, nil
}

func saveStudioAudioSettings(path string, options studioAudioOptions) error {
	options = normalizeStudioAudioOptions(options)
	if err := validateStudioAudioOptions(options); err != nil {
		return err
	}
	options.MonitorMuted = true
	data, err := json.MarshalIndent(studioAudioSettings{Version: 1, Options: &options}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".audio-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(append(data, '\n')); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}

// Only the production entry point loads user preferences. Test and embedded
// transports stay isolated unless they explicitly opt into a settings path.
func (t *studioTransport) initializeAudio(backend audiobackend.Name) error {
	t.audioBackend, t.audioNull = string(backend), backend == audiobackend.Null
	t.audioOptions = defaultStudioAudioOptionsFor(backend)
	t.audioSettingsWarning = ""
	path, err := studioAudioSettingsPath(backend)
	t.audioSettingsPath, t.audioSettingsErr = path, err
	if err == nil {
		t.audioOptions, err = loadStudioAudioSettings(path, t.audioOptions)
	}
	if err != nil {
		t.audioSettingsWarning = "Saved audio setup could not be read. Defaults are in use; the saved file is unchanged. Apply your choices to save a new setup."
	}
	if driver, err := audiobackend.New(backend); err == nil && !driver.CaptureSupported() {
		t.audioOptions.InputEnabled = false
	}
	return err
}
