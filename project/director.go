package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/sdk/director"
)

func validateDirectorProject(p *Project, tracks []notation.Track) error {
	for i := range tracks {
		tracks[i].Kind = p.Tracks[i].Kind
	}
	var patterns []notation.Pattern
	for _, v := range p.Patterns {
		patterns = append(patterns, notation.Pattern{Name: v.ID, Kind: v.Kind})
	}
	var scenes []notation.Scene
	for _, v := range p.Scenes {
		scene := notation.Scene{Name: v.ID}
		for track, pattern := range v.Bindings {
			scene.Bindings = append(scene.Bindings, notation.Binding{Track: track, Pattern: pattern})
		}
		scenes = append(scenes, scene)
	}
	var kits []notation.Kit
	for _, v := range p.Kits {
		kits = append(kits, notation.Kit{Name: v.ID})
	}
	live := liveToScore(p.Live)
	if ds := notation.ValidateDirector(live, tracks, patterns, scenes, kits); len(ds) > 0 {
		return fmt.Errorf("%s: %s", ds[0].Code, ds[0].Message)
	}
	for _, v := range p.Live.States {
		if !validID(v.Name) || !validID(v.Scene) {
			return fmt.Errorf("CICADA-DIRECTOR: invalid state identifier")
		}
	}
	for _, v := range p.Live.Stingers {
		if !validID(v.Name) || !validID(v.Track) || !validID(v.Pattern) {
			return fmt.Errorf("CICADA-DIRECTOR: invalid stinger identifier")
		}
		found := false
		for _, track := range p.Tracks {
			if track.ID == v.Track {
				for _, slot := range track.Slots {
					if slot != nil && *slot == v.Pattern {
						found = true
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("CICADA-DIRECTOR: stinger pattern has no track slot")
		}
	}
	return nil
}

// DirectorSurfaceOf resolves the game's named controls into immutable kernel IDs.
// Serialize this manifest for JS; both SDKs then emit identical commands.
func DirectorSurfaceOf(p *Project, sampleRate int) (director.Surface, error) {
	var surface director.Surface
	if err := ValidateProject(p); err != nil {
		return surface, err
	}
	if p.Live == nil {
		return surface, fmt.Errorf("score has no live block")
	}
	surface = director.Surface{Version: 1, SampleRate: sampleRate, Tracks: uint8(len(p.Tracks)), Land: p.Live.Land, PhraseBars: p.Live.PhraseBars,
		Macros: []director.Macro{}, States: []director.State{}, Stingers: []director.Stinger{}, Transitions: []director.Transition{}, Setup: append([]cmd.Command{}, LiveCommands(p)...)}
	for i, v := range p.Live.Macros {
		surface.Macros = append(surface.Macros, director.Macro{Name: v.Name, ID: uint16(i), SmoothFrames: v.SmoothingFrames(sampleRate)})
	}
	for i, v := range p.Live.States {
		for j, scene := range p.Scenes {
			if scene.ID == v.Scene {
				surface.States = append(surface.States, director.State{Name: v.Name, ID: uint16(i), Scene: uint16(j)})
			}
		}
	}
	for _, v := range p.Live.Stingers {
		for i, track := range p.Tracks {
			if track.ID != v.Track {
				continue
			}
			for j, slot := range track.Slots {
				if slot != nil && *slot == v.Pattern {
					surface.Stingers = append(surface.Stingers, director.Stinger{Name: v.Name, Track: uint8(i), Slot: uint16(j), Quantize: v.Quantize, CrossfadeFrames: director.Frames(v.CrossfadeMS, sampleRate)})
				}
			}
		}
	}
	for _, v := range p.Live.Transitions {
		surface.Transitions = append(surface.Transitions, director.Transition{From: v.From, To: v.To, Quantize: v.Quantize, CrossfadeFrames: director.Frames(v.CrossfadeMS, sampleRate)})
	}
	return surface, surface.Validate()
}
