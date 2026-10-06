package modalfit

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/kernel/voice/modal"
)

func (m Model) Voice(rate int) (*modal.FittedVoice, error) {
	hash, err := hex.DecodeString(m.SourceSHA256)
	hit, hitErr := hex.DecodeString(m.HitSHA256)
	if err != nil || len(hash) != 32 || m.SourceSHA256 != strings.ToLower(m.SourceSHA256) || hitErr != nil || len(hit) != 32 || m.RootMIDI < 12 || m.RootMIDI > 95 || m.Format != Format || (m.License != "user recording" && m.License != "owner recording") || len(m.Modes) < 1 || len(m.Modes) > modal.MaxModes {
		return nil, fmt.Errorf("invalid recorded modal model")
	}
	model := modal.Model{Count: len(m.Modes), NoiseMix: m.NoiseMix}
	for i, mode := range m.Modes {
		model.Modes[i] = modal.Mode{Ratio: mode.Ratio, Weight: mode.Weight, T60: mode.T60}
	}
	return modal.NewFittedVoice(model, rate)
}

// Render prepares an audition outside the audio callback. NoteOn and Next on
// the returned modal voice are allocation-free at every MIDI pitch.
func (m Model) Render(note, velocity, variation, rate int) ([]float32, error) {
	if note < 0 || note > 127 || velocity < 1 || velocity > 127 || variation < 0 {
		return nil, fmt.Errorf("invalid modeled note")
	}
	v, err := m.Voice(rate)
	if err != nil {
		return nil, err
	}
	duration := .05
	for _, mode := range m.Modes {
		duration = max(duration, mode.T60*1.35)
	}
	pcm := make([]float32, int(math.Ceil(duration*float64(rate))))
	v.ResetVariation(uint32(variation))
	v.NoteOn(uint8(note), uint8(velocity), false)
	for i := range pcm {
		pcm[i] = v.Next()
	}
	return pcm, nil
}

// Build bakes five roots and three dynamics through the same fitted modal
// engine, so existing pinned sampler scores work without another kernel ABI.
// The manifest pins both rendered audio and the exact measured model JSON.
func (m Model) Build(name string) (*recording.Pack, error) {
	var hits []recording.Hit
	for _, root := range []int{12, 36, 60, 84, 108} {
		for _, velocity := range []int{32, 80, 127} {
			pcm, err := m.Render(root, velocity, 0, 48000)
			if err != nil {
				return nil, err
			}
			peak, energy := 0.0, 0.0
			for _, x := range pcm {
				peak = max(peak, math.Abs(float64(x)))
				energy += float64(x) * float64(x)
			}
			if peak < 1e-12 {
				return nil, fmt.Errorf("model has no audible passband at root %d", root)
			}
			for i := range pcm {
				pcm[i] = float32(float64(pcm[i]) * .9 / peak)
			}
			hits = append(hits, recording.Hit{Input: 0, Start: 0, End: len(pcm), Rate: 48000, Root: root, Peak: peak, LoudnessDB: 10 * math.Log10(energy/float64(len(pcm))), SourceSHA256: m.SourceSHA256, PCM: pcm})
		}
	}
	pack, err := recording.Build(name, hits, 3)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	pack.Files["model.json"] = data
	pack.Manifest.Description = "User recording; fitted modal engine; model.json sha256=" + recording.Digest(data)
	manifest, err := json.MarshalIndent(pack.Manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	manifest = append(manifest, '\n')
	pack.Files["manifest.json"] = manifest
	pack.Pin = recording.Digest(manifest)
	pack.Files["instrument.cicada"] = []byte(pack.Starter("manifest.json", m.RootMIDI))
	return pack, nil
}
