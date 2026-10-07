package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
)

const sourceURL = "https://github.com/odvcencio/cicada"
const licenseURL = "https://creativecommons.org/publicdomain/zero/1.0/"

type packSummary struct {
	Patch                              string `json:"patch"`
	Assets, Zones                      int
	DecodedPCMBytes                    int64
	ManifestSHA256                     string `json:"manifest_sha256"`
	ModelRate, OutputRate              int
	FilterTaps                         int
	LoopSeamErrorMin, LoopSeamErrorMax float64
	RoundRobinRule                     string
	ReleaseRule                        string
}

func packConfig(p plan) sample.InstrumentConfig {
	c := sample.DefaultInstrumentConfig()
	c.Voices, c.Gain = 8, 1
	c.Amp = sample.Envelope{Sustain: 1, Release: 40}
	switch {
	case p.Spec.Patch <= 6:
		c.Amp.Release = float64(p.Spec.Controls[5]) * 1000 / 3
	case p.Spec.Patch <= 9:
		c.Amp.Release = 35. / 3
	case p.Spec.Patch <= 13:
		c.Amp.Release = 12
	case p.Spec.Patch <= 16:
		c.Amp.Release = float64(p.Spec.Controls[13]) * 1000 / 3
	case p.Spec.Patch <= 20:
		c.Amp.Release = float64(p.Spec.Controls[18]) * 1000 / 3
	default:
		c.Amp.Release = float64(p.Spec.Controls[5]) * 1000 / 3
	}
	return c
}

func buildPack(o options, p plan, filter decimator, output io.Writer) (packSummary, error) {
	summary := packSummary{
		Patch: p.Patch, DecodedPCMBytes: p.PCMBytes, ModelRate: modelRate, OutputRate: outputRate, FilterTaps: filterTaps,
		RoundRobinRule: "Take 0 preserves the named patch with maximum offline quality; later takes apply deterministic small pickup/felt, tangent, oscillator detune, ensemble rate, or free-running phase variations.",
		ReleaseRule:    "EP/Clav release state is captured after 0.125 seconds or the shorter requested duration. Its residual subtracts the matched held signal with nominal exponential damper decay; contact noise and nonlinear damping residual remain. The sampler's linear gate approximates damping.",
	}
	temporary, err := os.MkdirTemp(o.Out, "."+p.Patch+"-")
	if err != nil {
		return summary, err
	}
	defer os.RemoveAll(temporary)
	if err = os.Mkdir(filepath.Join(temporary, "samples"), 0755); err != nil {
		return summary, err
	}
	manifest := instrumentpack.Manifest{Format: instrumentpack.Format, ID: "cicada-keys-" + p.Patch, Description: "Owned multisamples of the original " + p.Patch + " modeled keyboard; rendered at 192 kHz and downsampled to 48 kHz.", Config: packConfig(p)}
	var pcmBytes int64
	seamMinimum, seamMaximum := math.Inf(1), 0.
	for group, root := range p.Roots {
		for _, layer := range p.Layers {
			for position := 0; position < o.RoundRobins; position++ {
				attack, release, err := captureTake(p, root.Note, layer.Center, position, &filter)
				if err != nil {
					return summary, err
				}
				loop := loopPoints{}
				if p.Loop {
					loop, err = findLoop(attack, p.MinimumLoopStart)
					if err != nil {
						return summary, fmt.Errorf("%s root %d: %w", p.Patch, root.Note, err)
					}
					seamMinimum, seamMaximum = min(seamMinimum, loop.NormalizedError), max(seamMaximum, loop.NormalizedError)
				}
				for kind, pcm := range []capture{attack, release} {
					if len(pcm.Left) == 0 {
						continue
					}
					id := fmt.Sprintf("n%03d-v%03d-r%02d-on", root.Note, layer.Center, position)
					if kind == 1 {
						id = fmt.Sprintf("n%03d-v%03d-r%02d-off", root.Note, layer.Center, position)
					}
					wav, scale, err := wav24(pcm, p.Channels)
					if err != nil {
						return summary, err
					}
					gain := 127 / float64(layer.Center) / scale
					if gain >= 16 || math.IsNaN(gain) || math.IsInf(gain, 0) {
						return summary, fmt.Errorf("%s needs zone gain %.6f; format limit remains below 16", id, gain)
					}
					compressed, err := deterministicGzip(wav)
					if err != nil {
						return summary, err
					}
					path := "samples/" + id + ".wav.gz"
					if err = os.WriteFile(filepath.Join(temporary, filepath.FromSlash(path)), compressed, 0644); err != nil {
						return summary, err
					}
					asset := instrumentpack.Asset{ID: id, Path: path, SHA256: digest(compressed), Bytes: int64(len(compressed)), WAVSHA256: digest(wav), WAVBytes: int64(len(wav)), Frames: len(pcm.Left), Rate: outputRate, Channels: p.Channels, SourceURL: sourceURL, SourceSHA256: pcm.SourceSHA256, License: "CC0-1.0", LicenseURL: licenseURL}
					zone := instrumentpack.Zone{Asset: id, Root: root.Note, KeyLow: root.Low, KeyHigh: root.High, VelocityLow: layer.Low, VelocityHigh: layer.High, Layer: layer.Center, Group: group, Position: position, Count: o.RoundRobins, Release: kind == 1, Gain: gain, End: len(pcm.Left)}
					if p.Loop && kind == 0 {
						zone.Loop, zone.LoopStart, zone.LoopEnd, zone.Crossfade = true, loop.Start, loop.End, loop.Crossfade
					}
					manifest.Assets = append(manifest.Assets, asset)
					manifest.Zones = append(manifest.Zones, zone)
					pcmBytes += int64(asset.Frames) * int64(asset.Channels) * 4
					if pcmBytes > maxPCMBytes {
						return summary, fmt.Errorf("decoded PCM exceeds the unchanged %d-byte limit", maxPCMBytes)
					}
				}
			}
		}
		fmt.Fprintf(output, "%s: captured root %d (%d/%d)\n", p.Patch, root.Note, group+1, len(p.Roots))
	}
	if pcmBytes != p.PCMBytes {
		return summary, fmt.Errorf("decoded PCM estimate changed: got %d, planned %d", pcmBytes, p.PCMBytes)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return summary, err
	}
	data = append(data, '\n')
	if err = validateOutput(manifest, data); err != nil {
		return summary, err
	}
	summary.Assets, summary.Zones, summary.ManifestSHA256 = len(manifest.Assets), len(manifest.Zones), digest(data)
	if p.Loop {
		summary.LoopSeamErrorMin, summary.LoopSeamErrorMax = seamMinimum, seamMaximum
	}
	if err = os.WriteFile(filepath.Join(temporary, "manifest.json"), data, 0644); err != nil {
		return summary, err
	}
	if err = os.WriteFile(filepath.Join(temporary, "manifest.sha256"), []byte(summary.ManifestSHA256+"\n"), 0644); err != nil {
		return summary, err
	}
	report, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return summary, err
	}
	if err = os.WriteFile(filepath.Join(temporary, "render-report.json"), append(report, '\n'), 0644); err != nil {
		return summary, err
	}
	recipe := struct {
		Format                                                                 string `json:"format"`
		Patch                                                                  string `json:"patch"`
		RootStep, Low, High, Layers, RoundRobins                               int
		CaptureFrames, ReleaseFrames, ReleaseHoldModelFrames, MinimumLoopStart int
		Controls                                                               [128]float32
	}{Format: "cicada.keys-render-recipe/1", Patch: p.Patch, RootStep: o.Step, Low: o.Low, High: o.High, Layers: o.Layers, RoundRobins: o.RoundRobins, CaptureFrames: p.Frames, ReleaseFrames: p.ReleaseFrames, ReleaseHoldModelFrames: min(p.Frames*4, modelRate/8), MinimumLoopStart: p.MinimumLoopStart, Controls: p.Spec.Controls}
	encodedRecipe, err := json.MarshalIndent(recipe, "", "  ")
	if err != nil {
		return summary, err
	}
	if err = os.WriteFile(filepath.Join(temporary, "render-recipe.json"), append(encodedRecipe, '\n'), 0644); err != nil {
		return summary, err
	}
	readme := fmt.Sprintf("Cicada owned modeled keyboard multisamples: %s\n\nAudio license: CC0-1.0 (%s). No third-party recordings or patch data.\nSource: %s\nManifest SHA256: %s\n\nRendered at 192000 Hz; EPs use 4x internal oversampling. A centered %d-tap windowed sinc low-pass at 21 kHz decimates to 48000 Hz. PCM24 WAV and gzip files have deterministic headers. SourceSHA256 hashes generated 192 kHz stereo float32 PCM, interleaved little-endian, including FIR guard frames, before decimation and end fades.\n\nThe first round robin preserves the named patch at maximum offline quality. Later takes use small deterministic physical parameter or free-running phase variations. Nonoverlapping key and velocity zones select the nearest captured root and layer. Zone gain compensates the sampler's velocity multiplier and any PCM headroom scaling. Integer root notes at velocity centers give the closest waveform match; other notes or velocities transpose or scale the recorded take.\n\nFinite captures finish after %.3f seconds unless a sustain loop is present. Loops retain the attack, find a lower-error sustain seam, and crossfade; they approximate evolving rotary, ensemble, and pad motion. Sampled pedal and note-off timing follow the sampler envelope, not the modeled mechanical state. EP/Clav release state is captured after 0.125 seconds or the shorter requested capture, before short-decay voices expire. Release samples retain contact noise and damping residual through matched held-signal subtraction; the linear sampler damping gate and variable-length held notes make release playback an approximation. Shared percussion rearming and chord interactions through common rotary/drive stages remain part of the modeled engine. The modeled engine remains available for expression.\n", p.Patch, licenseURL, sourceURL, summary.ManifestSHA256, filterTaps, float64(p.Frames)/outputRate)
	if err = os.WriteFile(filepath.Join(temporary, "README.txt"), []byte(readme), 0644); err != nil {
		return summary, err
	}
	if err = os.Rename(temporary, filepath.Join(o.Out, p.Patch)); err != nil {
		return summary, err
	}
	return summary, nil
}

func validateOutput(manifest instrumentpack.Manifest, data []byte) error {
	if _, err := instrumentpack.DecodeManifest(data); err != nil {
		return err
	}
	// Validate complete zones without keeping an entire decoded pack in memory.
	// Codec/hash/actual PCM loading is covered by the generated-pack tests.
	maximum := 0
	assets := make(map[string]instrumentpack.Asset, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		assets[asset.ID] = asset
		maximum = max(maximum, asset.Frames)
	}
	left, right := make([]float32, maximum), make([]float32, maximum)
	zones := make([]sample.Zone, 0, len(manifest.Zones))
	for _, zone := range manifest.Zones {
		a := assets[zone.Asset]
		region := sample.Region{Left: left[:a.Frames], SampleRate: a.Rate, RootKey: uint8(zone.Root), Start: zone.Start, End: zone.End, Loop: zone.Loop, LoopStart: zone.LoopStart, LoopEnd: zone.LoopEnd, Crossfade: zone.Crossfade}
		if a.Channels == 2 {
			region.Right = right[:a.Frames]
		}
		zones = append(zones, sample.Zone{Region: region, KeyLow: uint8(zone.KeyLow), KeyHigh: uint8(zone.KeyHigh), VelocityLow: uint8(zone.VelocityLow), VelocityHigh: uint8(zone.VelocityHigh), Layer: uint8(zone.Layer), Group: uint8(zone.Group), Position: uint8(zone.Position), Count: uint8(zone.Count), Release: zone.Release, Gain: zone.Gain})
	}
	_, err := sample.NewInstrument(outputRate, zones, manifest.Config)
	return err
}

// Avoid accidental dependence on a gzip wrapper's ambient time or filename.
func canonicalGzipHeader(data []byte) bool {
	return len(data) >= 10 && bytes.Equal(data[:3], []byte{0x1f, 0x8b, 8}) && data[3] == 0 && bytes.Equal(data[4:8], []byte{0, 0, 0, 0}) && data[9] == 255
}
