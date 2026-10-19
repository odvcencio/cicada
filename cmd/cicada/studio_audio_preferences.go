package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/internal/appdirs"
	"m31labs.dev/cicada/internal/audiobackend"
)

type studioAudioPreferences struct {
	Version int                `json:"version"`
	Options studioAudioOptions `json:"options"`
}

func studioAudioPreferencesPath(backend audiobackend.Name) (string, error) {
	switch backend {
	case audiobackend.Tymbal, audiobackend.Oto, audiobackend.Null:
	default:
		return "", fmt.Errorf("unknown audio backend %q", backend)
	}
	dir, err := appdirs.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "audio-"+string(backend)+".json"), nil
}

func loadStudioAudioPreferences(backend audiobackend.Name) (studioAudioOptions, error) {
	defaults := defaultStudioAudioOptionsFor(backend)
	path, err := studioAudioPreferencesPath(backend)
	if err != nil {
		return defaults, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	var prefs studioAudioPreferences
	if err := json.Unmarshal(data, &prefs); err != nil {
		return defaults, err
	}
	if prefs.Version != 1 {
		return defaults, fmt.Errorf("unsupported audio settings version %d", prefs.Version)
	}
	options := normalizeStudioAudioOptions(prefs.Options)
	if err := validateStudioAudioOptions(options); err != nil {
		return defaults, err
	}
	// Keep a disconnected route selected so it can be reconnected or changed
	// explicitly, rather than unexpectedly moving recording to another input.
	return options, nil
}

func saveStudioAudioPreferences(backend audiobackend.Name, options studioAudioOptions) error {
	options = normalizeStudioAudioOptions(options)
	if err := validateStudioAudioOptions(options); err != nil {
		return err
	}
	path, err := studioAudioPreferencesPath(backend)
	if err != nil {
		return err
	}
	data, err := json.Marshal(studioAudioPreferences{Version: 1, Options: options})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".audio-settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
