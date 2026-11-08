package main

import (
	"fmt"
	"sort"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// portableFeatureRefusals lists every use of a feature that portable (browser
// and WAM2) images refuse: the tape echo, pattern humanize, sampler kit lanes
// and sampler chords. The image encoder stops at the first, so check reads the
// project itself to report them all at once.
func portableFeatureRefusals(p *project.Project) []notation.Diagnostic {
	var messages []string
	for _, effect := range p.Effects {
		if effect.Kind == "tape" {
			messages = append(messages, fmt.Sprintf("effect %s is a tape echo, which portable images do not have; use a delay", effect.ID))
		}
	}
	used := map[string][]string{} // pattern ID -> tracks that play it
	for _, track := range p.Tracks {
		for _, slot := range track.Slots {
			if slot != nil {
				used[*slot] = append(used[*slot], track.ID)
			}
		}
	}
	if p.Arrange != nil {
		for _, placement := range p.Arrange.Placements {
			used[placement.Content] = append(used[placement.Content], placement.Track)
		}
	}
	samplers := map[string]bool{}
	for _, sampler := range p.Samplers {
		samplers[sampler.Name] = true
	}
	kinds := map[string]string{}
	for _, track := range p.Tracks {
		kinds[track.ID] = track.Kind
	}
	for _, pattern := range p.Patterns {
		tracks, ok := used[pattern.ID]
		if !ok {
			continue
		}
		if pattern.Humanize != 0 {
			messages = append(messages, fmt.Sprintf("pattern %s uses humanize = %d, which portable images do not have", pattern.ID, pattern.Humanize))
		}
		chord := false
		for _, step := range pattern.Data {
			chord = chord || step != nil && len(step.Notes) > 1
		}
		if !chord {
			continue
		}
		seen := map[string]bool{}
		for _, track := range tracks {
			if samplers[kinds[track]] && !seen[track] {
				seen[track] = true
				messages = append(messages, fmt.Sprintf("track %s plays chords from pattern %s on sampler %s, which portable images do not support", track, pattern.ID, kinds[track]))
			}
		}
	}
	for _, kit := range p.Kits {
		lanes, err := project.KitSamplerLanes(kit, nil, p.SamplerPackSet())
		if err != nil || len(lanes) == 0 {
			continue
		}
		messages = append(messages, fmt.Sprintf("kit %s binds %d lane(s) to a sample pack, which portable images do not support", kit.ID, len(lanes)))
	}
	sort.Strings(messages)
	diagnostics := make([]notation.Diagnostic, len(messages))
	for i, message := range messages {
		diagnostics[i] = notation.Diagnostic{Code: "CICADA-UNSUPPORTED", Severity: "error", Message: "browser profile: " + message, Position: notation.Position{Line: 1, Column: 1}}
	}
	return diagnostics
}
