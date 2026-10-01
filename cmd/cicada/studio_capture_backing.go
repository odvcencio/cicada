package main

import (
	"maps"
	"strings"

	"m31labs.dev/cicada/project"
)

// captureBacking prepares synth accompaniment while audio tracks are recording.
// Track/scene indices remain stable. Clip sequencing is not yet a kernel feature;
// stored audio is played by the host sampler audition instead.
func captureBacking(p *project.Project) *project.Project {
	if !p.HasAudio() {
		return p
	}
	backing := *p
	backing.Assets, backing.Clips, backing.Samplers = nil, nil, nil
	backing.Tracks = nil
	silent := map[string]bool{}
	for _, track := range p.Tracks {
		isAudio := track.Kind == "audio"
		for _, sampler := range p.Samplers {
			isAudio = isAudio || track.Kind == sampler.Name
		}
		if isAudio {
			silent[track.ID] = true
			track = project.Track{ID: track.ID, Kind: "acid", Mixer: track.Mixer, Params: map[string]project.Value{}}
		}
		backing.Tracks = append(backing.Tracks, track)
	}
	// Allocate independent slices; never change the Studio project's tables.
	backing.Scenes = nil
	for _, scene := range p.Scenes {
		scene.Bindings = maps.Clone(scene.Bindings)
		for id := range silent {
			if _, exists := scene.Bindings[id]; exists {
				scene.Bindings[id] = "off"
			}
		}
		settings := scene.Settings
		scene.Settings = nil
		for _, setting := range settings {
			muted := false
			for id := range silent {
				muted = muted || strings.HasPrefix(setting.Path, id+".")
			}
			if !muted {
				scene.Settings = append(scene.Settings, setting)
			}
		}
		backing.Scenes = append(backing.Scenes, scene)
	}
	return &backing
}
