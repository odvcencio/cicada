package main

import "m31labs.dev/cicada/internal/audiobackend"

func studioAudioPreferencesPath(backend audiobackend.Name) (string, error) {
	return studioAudioSettingsPath(backend)
}

func loadStudioAudioPreferences(backend audiobackend.Name) (studioAudioOptions, error) {
	defaults := defaultStudioAudioOptionsFor(backend)
	path, err := studioAudioPreferencesPath(backend)
	if err != nil {
		return defaults, err
	}
	return loadStudioAudioSettings(path, defaults)
}

func saveStudioAudioPreferences(backend audiobackend.Name, options studioAudioOptions) error {
	path, err := studioAudioPreferencesPath(backend)
	if err != nil {
		return err
	}
	return saveStudioAudioSettings(path, options)
}
