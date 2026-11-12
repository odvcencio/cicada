package main

import (
	"fmt"
	"sort"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Feature kinds that portableFeatureRefusals lists and the image encoder
// refuses.
const (
	featureTape          = "tape echo"
	featureHumanize      = "humanize"
	featureSamplerChords = "sampler chords"
	featureKitLanes      = "sampler kit lanes"
	featureReverbReturns = "reverb returns"
)

// portableFeatureRefusals lists every use of a feature that portable (browser
// and WAM2) images refuse: the tape echo, reverb returns after the first,
// pattern humanize, sampler kit lanes and sampler chords. The image encoder
// stops at the first, so check reads the project itself to report them all at
// once. kinds holds the features found.
func portableFeatureRefusals(p *project.Project) (diagnostics []notation.Diagnostic, kinds map[string]bool) {
	kinds = map[string]bool{}
	var messages []string
	var reverbs []string // reverb returns, in slot order
	for _, effect := range p.Effects {
		if effect.Kind == "tape" {
			kinds[featureTape] = true
			messages = append(messages, fmt.Sprintf("effect %s is a tape echo, which portable images do not have; use a delay", effect.ID))
		}
		if (effect.Kind == "reverb" || effect.Kind == "" && effect.ID == "reverb") && !project.MasterHasInsert(p, effect.ID) {
			reverbs = append(reverbs, effect.ID)
		}
	}
	for i := 1; i < len(reverbs); i++ {
		kinds[featureReverbReturns] = true
		messages = append(messages, fmt.Sprintf("effect %s is reverb return %d of %d; portable images hold one reverb return until image version 19 (native playback and render play them all)", reverbs[i], i+1, len(reverbs)))
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
	trackKinds := map[string]string{}
	played := map[string]bool{} // kinds that some track plays, such as a kit
	for _, track := range p.Tracks {
		trackKinds[track.ID] = track.Kind
		played[track.Kind] = true
	}
	for _, pattern := range p.Patterns {
		tracks, ok := used[pattern.ID]
		if !ok {
			continue
		}
		if pattern.Humanize != 0 {
			kinds[featureHumanize] = true
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
			if samplers[trackKinds[track]] && !seen[track] {
				seen[track] = true
				kinds[featureSamplerChords] = true
				messages = append(messages, fmt.Sprintf("track %s plays chords from pattern %s on sampler %s, which portable images do not support", track, pattern.ID, trackKinds[track]))
			}
		}
	}
	for _, kit := range p.Kits {
		if !played[kit.ID] {
			continue // the image holds only the kits that tracks play
		}
		lanes, err := project.KitSamplerLanes(kit, nil, p.SamplerPackSet())
		if err != nil || len(lanes) == 0 {
			continue
		}
		kinds[featureKitLanes] = true
		messages = append(messages, fmt.Sprintf("kit %s binds %d lane(s) to a sample pack, which portable images do not support", kit.ID, len(lanes)))
	}
	sort.Strings(messages)
	diagnostics = make([]notation.Diagnostic, len(messages))
	for i, message := range messages {
		diagnostics[i] = notation.Diagnostic{Code: "CICADA-UNSUPPORTED", Severity: "error", Message: "browser profile: " + message, Position: notation.Position{Line: 1, Column: 1}}
	}
	return diagnostics, kinds
}

// imageFeatureKind names the feature behind an image encoder refusal, or ""
// when the refusal is about something else, such as the image size.
func imageFeatureKind(message string) string {
	switch {
	case strings.Contains(message, "portable images have no tape echo"):
		return featureTape
	case strings.Contains(message, "portable images hold one reverb return"):
		return featureReverbReturns
	case strings.Contains(message, "portable images have no pattern humanize"):
		return featureHumanize
	case strings.Contains(message, "portable images have no sampler kit lanes"):
		return featureKitLanes
	case strings.Contains(message, "chord payload requires an active polyphonic step"):
		return featureSamplerChords
	}
	return ""
}
