package main

import (
	"maps"
	"strings"

	"m31labs.dev/cicada/project"
)

// captureBacking prepares the synth-only portable qualification image. Native
// Tymbal playback and capture use host-prepared clip and sampler voices instead.
// Track/scene indices remain stable for the legacy browser capture fixtures.
func captureBacking(p *project.Project) *project.Project {
	if !p.HasAudio() {
		return p
	}
	backing := *p
	backing.Assets, backing.Clips, backing.Samplers = nil, nil, nil
	backing.Tracks = nil
	// Audio-only projects lose their last playable table when clips are removed.
	// Keep an unused rest pattern so validation and engine preparation stay valid.
	if len(backing.Patterns) == 0 {
		backing.Patterns = []project.Pattern{{
			ID: "capture-silence", Kind: "acid", Steps: 1,
			SwingPercent100: 5000, GatePercent: 50,
			Data: []*project.Step{nil}, Lanes: map[string][]*project.Step{},
		}}
	}
	silent := map[string]bool{}
	for _, track := range p.Tracks {
		isAudio := p.Edition == 2 && track.Kind == "audio"
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
