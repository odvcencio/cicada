package sampleasset

import (
	"m31labs.dev/cicada/kernel/dsp/detmath"
	"m31labs.dev/cicada/project"
)

// OutputRates are the output rates at which the sample voice plays
// (kernel/voice/sample admits 44100, 48000 and 96000 Hz). A host plays at
// one of them: render and stems at --rate, Studio and the browser at their
// device's rate.
var OutputRates = []int{44_100, 48_000, 96_000}

// The sample voice reads its source at the playback ratio, the source rate
// over the output rate times 2^((note-root)/12), and refuses a note whose
// ratio is below minPlaybackRatio or above maxPlaybackRatio
// (kernel/voice/sample.Voice.playbackRatio). Sources play at 8000 to
// 192000 Hz (sample.Region.Validate).
const (
	minPlaybackRatio = 0.125
	maxPlaybackRatio = 8
	MinSourceRate    = 8_000
	MaxSourceRate    = 192_000
)

// PlaybackRatio is the ratio at which the sample voice plays note from a
// source recorded at sourceRate with root as its unpitched key, at
// outputRate. It computes what the voice computes, in the same order, so the
// two agree at the bounds.
func PlaybackRatio(sourceRate, outputRate, root, note int) float64 {
	ratio := float64(sourceRate) / float64(outputRate)
	if note != root {
		ratio *= detmath.Exp2((float64(note) - float64(root)) / 12)
	}
	return ratio
}

// Playable reports whether the sample voice plays note from such a source at
// outputRate.
func Playable(sourceRate, outputRate, root, note int) bool {
	ratio := PlaybackRatio(sourceRate, outputRate, root, note)
	return ratio >= minPlaybackRatio && ratio <= maxPlaybackRatio
}

// PlayableRange is the lowest and highest MIDI note that the sample voice
// plays from such a source at every rate in rates. ok is false when it plays
// no note at all of them.
func PlayableRange(sourceRate, root int, rates []int) (low, high int, ok bool) {
	low, high = -1, -1
	for note := 0; note <= 127; note++ {
		playable := true
		for _, rate := range rates {
			playable = playable && Playable(sourceRate, rate, root, note)
		}
		if playable {
			if low < 0 {
				low = note
			}
			high = note
		}
	}
	return low, high, low >= 0
}

// RangeFinding is a note that a track of a single-asset sampler plays and
// that the sample voice cannot pitch its asset to at one or more output
// rates. Render refuses it at those rates before it writes audio
// (ValidatePatterns), and the engine faults when it plays it.
type RangeFinding struct {
	Track, Pattern string
	// Step is the index of the note's step in Pattern's data, and Chord the
	// index of the pitch in the step's chord (0 for a single note). For a
	// note of a performed pattern, Performed is true and Step is the index in
	// the performance's notes.
	Step, Chord int
	Performed   bool
	// Note is the MIDI note the voice receives: the written pitch plus the
	// pattern's transpose, held to 0..127 as render holds it.
	Note int
	// Sampler, Asset, AssetRate and Root describe the source. Low and High
	// are the notes it plays at every rate checked; Low > High when it plays
	// none.
	Sampler, Asset string
	AssetRate      int
	Root           int
	Low, High      int
	// Rates are the checked output rates at which Note does not play, in the
	// order given, and Ratios its playback ratio at each.
	Rates  []int
	Ratios []float64
}

// SamplerRangeFindings lists the notes that tracks of single-asset samplers
// play and that the sample voice cannot play at some rate in rates. It reads
// what render's ValidatePatterns reads, every note of every pattern in a
// track's slots (whatever scene, placement, chain or stinger plays it, ties
// excepted), and the notes of performed patterns, which the engine plays. It
// reads only the score's declarations, never audio, so it answers before
// anything is decoded. Findings are in track, slot and step order.
func SamplerRangeFindings(p *project.Project, rates []int) []RangeFinding {
	if p == nil || len(rates) == 0 {
		return nil
	}
	assets := map[string]project.Asset{}
	for _, asset := range p.Assets {
		assets[asset.Name] = asset
	}
	samplers := map[string]project.Sampler{}
	for _, sampler := range p.Samplers {
		if sampler.Pack == "" {
			samplers[sampler.Name] = sampler
		}
	}
	patterns := map[string]project.Pattern{}
	for _, pattern := range p.Patterns {
		patterns[pattern.ID] = pattern
	}
	var findings []RangeFinding
	for _, track := range p.Tracks {
		sampler, ok := samplers[track.Kind]
		if !ok || p.Edition == 2 && track.Kind == "audio" {
			continue
		}
		asset, ok := assets[sampler.Asset]
		if !ok || asset.RateHz < MinSourceRate || asset.RateHz > MaxSourceRate {
			continue // the asset itself cannot play; that is another refusal
		}
		low, high, playable := PlayableRange(asset.RateHz, sampler.RootMIDI, rates)
		if !playable {
			low, high = 1, 0
		}
		check := func(finding RangeFinding, pitch int) {
			note := max(0, min(127, pitch))
			for _, rate := range rates {
				if !Playable(asset.RateHz, rate, sampler.RootMIDI, note) {
					finding.Rates = append(finding.Rates, rate)
					finding.Ratios = append(finding.Ratios, PlaybackRatio(asset.RateHz, rate, sampler.RootMIDI, note))
				}
			}
			if len(finding.Rates) == 0 {
				return
			}
			finding.Note = note
			findings = append(findings, finding)
		}
		seen := map[string]bool{}
		for _, id := range track.Slots {
			if id == nil || seen[*id] {
				continue
			}
			seen[*id] = true
			pattern, ok := patterns[*id]
			if !ok {
				continue
			}
			base := RangeFinding{Track: track.ID, Pattern: pattern.ID, Sampler: sampler.Name, Asset: asset.Name, AssetRate: asset.RateHz, Root: sampler.RootMIDI, Low: low, High: high}
			for i, step := range pattern.Data {
				if step == nil || step.Tie {
					continue
				}
				if len(step.Notes) == 0 {
					finding := base
					finding.Step = i
					check(finding, int(step.Note)+int(pattern.Transpose))
					continue
				}
				for c, pitch := range step.Notes {
					finding := base
					finding.Step, finding.Chord = i, c
					check(finding, pitch+int(pattern.Transpose))
				}
			}
			if pattern.Performance != nil {
				for i, note := range pattern.Performance.Notes {
					finding := base
					finding.Step, finding.Performed = i, true
					check(finding, int(note.Note)+int(pattern.Transpose))
				}
			}
		}
	}
	return findings
}

// AssetFinding is a sampler or clip whose asset the sample voice cannot read
// at one or more output rates. A sampler reads assets recorded at
// MinSourceRate to MaxSourceRate and pitches them (SamplerRangeFindings
// checks its notes); a clip plays its asset unpitched, at the source rate
// over the output rate, which must lie within the voice's ratios too.
type AssetFinding struct {
	// Sampler or Clip names what plays the asset; one of them is set.
	Sampler, Clip string
	Asset         string
	AssetRate     int
	// Low and High are the asset rates that play at every rate checked.
	Low, High int
	// Rates are the checked output rates at which the asset does not play,
	// in the order given.
	Rates []int
}

// AssetRateFindings lists the samplers that a track plays and the clips that
// an audio track plays whose asset the sample voice cannot read at some rate
// in rates, in declaration order. It reads only the declared asset rates.
func AssetRateFindings(p *project.Project, rates []int) []AssetFinding {
	if p == nil || len(rates) == 0 {
		return nil
	}
	assets := map[string]project.Asset{}
	for _, asset := range p.Assets {
		assets[asset.Name] = asset
	}
	played := map[string]bool{}
	for _, track := range p.Tracks {
		played[track.Kind] = true
		if p.Edition == 2 && track.Kind == "audio" {
			slots, err := project.ClipSlots(p, track)
			if err != nil {
				continue // the compile refuses the track for its own reason
			}
			for _, clip := range slots {
				if clip != nil {
					played["clip\x00"+*clip] = true
				}
			}
		}
	}
	var findings []AssetFinding
	for _, sampler := range p.Samplers {
		asset, ok := assets[sampler.Asset]
		if sampler.Pack != "" || !played[sampler.Name] || !ok || asset.RateHz >= MinSourceRate && asset.RateHz <= MaxSourceRate {
			continue
		}
		// A sampler reads any source rate the voice admits and pitches it.
		findings = append(findings, AssetFinding{Sampler: sampler.Name, Asset: asset.Name, AssetRate: asset.RateHz, Low: MinSourceRate, High: MaxSourceRate, Rates: rates})
	}
	for _, clip := range p.Clips {
		asset, ok := assets[clip.Asset]
		if !played["clip\x00"+clip.Name] || !ok {
			continue
		}
		finding := AssetFinding{Clip: clip.Name, Asset: asset.Name, AssetRate: asset.RateHz}
		for _, output := range rates {
			if !clipPlayable(asset.RateHz, output) {
				finding.Rates = append(finding.Rates, output)
			}
		}
		if len(finding.Rates) > 0 {
			finding.Low, finding.High = clipRates(rates)
			findings = append(findings, finding)
		}
	}
	return findings
}

// clipPlayable reports whether the voice plays a clip's asset, unpitched, at
// output.
func clipPlayable(sourceRate, output int) bool {
	return sourceRate >= MinSourceRate && sourceRate <= MaxSourceRate && Playable(sourceRate, output, 60, 60)
}

// clipRates are the lowest and highest asset rates that a clip plays at every
// rate in rates: the ratio bounds at the highest and lowest output rate, held
// to the rates the voice reads.
func clipRates(rates []int) (low, high int) {
	low, high = MinSourceRate, MaxSourceRate
	for _, output := range rates {
		for !clipPlayable(low, output) && low <= high {
			low++
		}
		for !clipPlayable(high, output) && high >= low {
			high--
		}
	}
	return low, high
}
