package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/keyboard"
	kernelkeys "m31labs.dev/cicada/kernel/voice/keyboard"
)

const (
	modelRate      = 192000
	outputRate     = 48000
	maxPCMBytes    = instrumentpack.MaxPCMBytes
	releaseSeconds = .2
)

type options struct {
	Out, Patch                                 string
	Step, Layers, RoundRobins, Low, High, Rate int
	Duration                                   float64
	EstimateOnly                               bool
}

func defaults() options {
	return options{Patch: "all", Step: 3, Layers: 5, RoundRobins: 2, Low: 21, High: 108, Rate: outputRate}
}

type velocityLayer struct{ Center, Low, High int }
type rootZone struct{ Note, Low, High int }
type plan struct {
	Patch                                             string
	Spec                                              kernelkeys.Spec
	Roots                                             []rootZone
	Layers                                            []velocityLayer
	Channels, Frames, ReleaseFrames, MinimumLoopStart int
	Loop                                              bool
	PCMBytes                                          int64
}

func velocityLayers(count int) []velocityLayer {
	layers := make([]velocityLayer, count)
	for i := range layers {
		layers[i].Center = int(math.Round(127 * float64(i+1) / float64(count)))
	}
	for i := range layers {
		layers[i].Low, layers[i].High = 1, 127
		if i > 0 {
			layers[i].Low = (layers[i-1].Center+layers[i].Center)/2 + 1
		}
		if i+1 < count {
			layers[i].High = (layers[i].Center + layers[i+1].Center) / 2
		}
	}
	return layers
}

func rootZones(low, high, step int) []rootZone {
	var roots []rootZone
	for note := low; note <= high; note += step {
		roots = append(roots, rootZone{Note: note})
	}
	if roots[len(roots)-1].Note != high {
		roots = append(roots, rootZone{Note: high})
	}
	for i := range roots {
		roots[i].Low, roots[i].High = low, high
		if i > 0 {
			roots[i].Low = (roots[i-1].Note+roots[i].Note)/2 + 1
		}
		if i+1 < len(roots) {
			roots[i].High = (roots[i].Note + roots[i+1].Note) / 2
		}
	}
	return roots
}

func planPacks(o options) ([]plan, error) {
	if o.Rate != outputRate || (o.Step != 1 && o.Step != 3) || o.Layers < 5 || o.Layers > 16 || o.RoundRobins < 2 || o.RoundRobins > 32 || o.Low < 21 || o.High > 108 || o.Low > o.High || math.IsNaN(o.Duration) || math.IsInf(o.Duration, 0) || o.Duration < 0 || o.Duration > 120 {
		return nil, fmt.Errorf("use rate 48000, step 1 or 3, 5..16 layers, 2..32 round robins, notes 21..108, and a finite duration 0..120 seconds")
	}
	names := []string{o.Patch}
	if o.Patch == "all" {
		names = append([]string(nil), keyboard.Names[:]...)
	}
	plans := make([]plan, 0, len(names))
	for _, name := range names {
		spec, err := keyboard.DefaultSpec(name)
		if err != nil {
			return nil, err
		}
		if spec.Patch <= 6 {
			spec.Controls[11] = 4
		}
		spec.Controls[127] = 8
		p := plan{Patch: name, Spec: spec, Roots: rootZones(o.Low, o.High, o.Step), Layers: velocityLayers(o.Layers), Channels: 2}
		mono, err := dryMono(spec)
		if err != nil {
			return nil, err
		}
		if mono {
			p.Channels = 1
		}
		duration := o.Duration
		if duration == 0 {
			duration = 2
			if mono {
				duration = 4
			}
		}
		p.Frames = int(math.Round(duration * float64(o.Rate)))
		if p.Frames < o.Rate/20 {
			return nil, fmt.Errorf("%s capture must be at least 0.05 seconds", name)
		}
		if spec.Patch <= 9 {
			p.ReleaseFrames = int(releaseSeconds * float64(o.Rate))
		}
		p.Loop = spec.Patch >= 10 && spec.Patch <= 13 || name == "soft_pad" || name == "string_machine"
		if p.Loop {
			start := .2
			if spec.Patch >= 10 && spec.Patch <= 13 && spec.Controls[10] != 0 {
				// Keep the initial percussion out of the repeated sustain.
				start = .75
				if spec.Controls[11] == 0 {
					start = 1.4
				}
			}
			if name == "soft_pad" {
				start = max(start, float64(spec.Controls[15])*1.5+.1)
			}
			if name == "string_machine" {
				start = max(start, float64(spec.Controls[4])*1.5+.1)
			}
			p.MinimumLoopStart = int(math.Ceil(start * float64(o.Rate)))
			if p.Frames-p.MinimumLoopStart < o.Rate/5 {
				return nil, fmt.Errorf("%s duration must retain its %.3f-second attack and at least 0.2 seconds for a sustain loop", name, start)
			}
		}
		takes := int64(len(p.Roots)) * int64(len(p.Layers)) * int64(o.RoundRobins)
		assets := takes
		if p.ReleaseFrames > 0 {
			assets *= 2
		}
		if assets > 4096 {
			return nil, fmt.Errorf("%s would require %d assets/zones; pack format admits at most 4096", name, assets)
		}
		p.PCMBytes = takes * int64(p.Frames+p.ReleaseFrames) * int64(p.Channels) * 4
		if p.PCMBytes > maxPCMBytes {
			return nil, fmt.Errorf("%s needs %d decoded PCM bytes (%.1f MiB), exceeding %d bytes (256 MiB); reduce duration, roots, layers, or round robins", name, p.PCMBytes, float64(p.PCMBytes)/(1<<20), maxPCMBytes)
		}
		plans = append(plans, p)
	}
	return plans, nil
}

func dryMono(spec kernelkeys.Spec) (bool, error) {
	// Other families retain stereo even when one probe happens to cancel.
	if spec.Patch > 9 {
		return false, nil
	}
	v, err := keyboard.New(modelRate, &spec)
	if err != nil {
		return false, err
	}
	if err = v.NoteOn(60, 102); err != nil {
		return false, err
	}
	for range modelRate / 40 {
		l, r := v.NextStereo()
		if l != r {
			return false, nil
		}
	}
	return true, nil
}

func preflightOutput(out string, plans []plan) error {
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	for _, p := range plans {
		name := filepath.Join(out, p.Patch)
		_, err := os.Lstat(name)
		if err == nil {
			return fmt.Errorf("output patch directory already exists: %s; choose a fresh output directory", p.Patch)
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
