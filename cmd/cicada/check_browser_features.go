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
	featureEchoHighpass  = "echo highpass"
)

// portableFeatureRefusals lists every use of a feature that portable (browser
// and WAM2) images refuse: the tape echo, the echo highpass, pattern humanize,
// sampler kit lanes and sampler chords. The image encoder stops at the first, so check reads the
// project itself to report them all at once. kinds holds the features found.
func portableFeatureRefusals(p *project.Project) (diagnostics []notation.Diagnostic, kinds map[string]bool) {
	kinds = map[string]bool{}
	var messages []string
	for _, effect := range p.Effects {
		if effect.Kind == "tape" {
			kinds[featureTape] = true
			messages = append(messages, fmt.Sprintf("effect %s is a tape echo, which portable images do not have; use a delay", effect.ID))
		}
		if effect.Kind != "delay" {
			continue
		}
		path := effect.ID + ".highpass"
		if value, ok := effect.Params["highpass"]; ok && !(value.Unit == "enum" && value.Text == "off") {
			kinds[featureEchoHighpass] = true
			messages = append(messages, fmt.Sprintf("effect %s sets highpass, which portable images do not have", effect.ID))
		}
		for _, scene := range p.Scenes {
			for _, setting := range scene.Settings {
				if setting.Path == path {
					kinds[featureEchoHighpass] = true
					messages = append(messages, fmt.Sprintf("scene %s sets %s, which portable images do not have", scene.ID, path))
				}
			}
		}
		for _, lane := range p.Automation {
			if lane.Path == path {
				kinds[featureEchoHighpass] = true
				messages = append(messages, fmt.Sprintf("an automate lane moves %s, which portable images do not have", path))
			}
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
	case strings.Contains(message, "portable images have no echo highpass"):
		return featureEchoHighpass
	case strings.Contains(message, "portable images have no pattern humanize"):
		return featureHumanize
	case strings.Contains(message, "portable images have no sampler kit lanes"):
		return featureKitLanes
	case strings.Contains(message, "chord payload requires an active polyphonic step"):
		return featureSamplerChords
	}
	return ""
}
