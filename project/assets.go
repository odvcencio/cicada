package project

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/notation"
)

// Asset is immutable source data. Rate and frames refer to the source file.
type Asset struct {
	Name     string `json:"name" cicada:"Asset name"`
	Path     string `json:"path" cicada:"Project-relative audio path"`
	SHA256   string `json:"sha256" cicada:"SHA-256 of exact file bytes"`
	Format   string `json:"format" cicada:"Audio container format"`
	Frames   int64  `json:"frames" cicada:"Source frame count" unit:"frames" range:"1..9007199254740991"`
	RateHz   int    `json:"rate_hz" cicada:"Source sample rate" unit:"Hz" range:"8000..384000"`
	Channels int    `json:"channels" cicada:"Source channels" range:"1..2"`
	Source   string `json:"source,omitempty" cicada:"Asset provenance" introduced:"cicada.project/2"`
}
type Clip struct {
	Name          string  `json:"name" cicada:"Clip name"`
	Asset         string  `json:"asset" cicada:"Referenced asset name"`
	StartFrame    int64   `json:"start_frame" cicada:"Inclusive region start" unit:"frames"`
	EndFrame      int64   `json:"end_frame" cicada:"Exclusive region end" unit:"frames"`
	GainDB        float64 `json:"gain_db" cicada:"Clip gain" unit:"dB" range:"-60..24"`
	FadeInFrames  int64   `json:"fade_in_frames" cicada:"Fade-in length" unit:"frames"`
	FadeOutFrames int64   `json:"fade_out_frames" cicada:"Fade-out length" unit:"frames"`
}
type Sampler struct {
	Name     string `json:"name" cicada:"Sampler instrument name"`
	Asset    string `json:"asset" cicada:"Single full-asset region"`
	RootMIDI int    `json:"root_midi" cicada:"Root MIDI note" range:"12..95"`
	Mode     string `json:"mode" cicada:"oneshot or full-region loop"`
	Voices   int    `json:"voices" cicada:"Maximum simultaneous voices" range:"1..32"`
}

func (p *Project) HasAudio() bool {
	return len(p.Assets) > 0 || len(p.Clips) > 0 || len(p.Samplers) > 0 || projectHasAudioTrack(p)
}
func projectHasAudioTrack(p *Project) bool {
	for _, t := range p.Tracks {
		if p.Edition == 2 && t.Kind == "audio" {
			return true
		}
	}
	return false
}
func sourceHasAudio(score *notation.Score) bool {
	if len(score.Assets) > 0 || len(score.Clips) > 0 || len(score.Samplers) > 0 {
		return true
	}
	for _, t := range score.Tracks {
		if score.Version == 2 && t.Kind == "audio" {
			return true
		}
	}
	return false
}

// VerifyAssets performs filesystem work only in the host/compiler. OpenRoot
// confines both relative paths and symlink traversal to the project directory.
// The same open file is hashed and inspected; no decoder or engine is involved.
func VerifyAssets(score *notation.Score, projectDir string) []notation.Diagnostic {
	if score == nil || len(score.Assets) == 0 {
		return nil
	}
	var ds []notation.Diagnostic
	add := func(a notation.Asset, field, code, expected, actual string) {
		position := a.Position
		for _, p := range a.Params {
			if p.Name == field {
				position = p.ValuePosition
			}
		}
		ds = append(ds, notation.Diagnostic{Code: code, Severity: "error", Message: fmt.Sprintf("asset %s: expected %s; actual %s", a.Name, expected, actual), Position: position})
	}
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		for _, a := range score.Assets {
			add(a, "", "CICADA-ASSET-MISSING", "readable project directory", err.Error())
		}
		return ds
	}
	defer root.Close()
	for _, a := range score.Assets {
		if !notation.ValidAssetPath(a.Path) {
			add(a, "", "CICADA-ASSET-PATH", "path inside project", strconv.Quote(a.Path))
			continue
		}
		file, err := root.Open(a.Path)
		if err != nil {
			code := "CICADA-ASSET-MISSING"
			if strings.Contains(err.Error(), "escapes") {
				code = "CICADA-ASSET-PATH"
			}
			add(a, "", code, "readable regular file inside project", err.Error())
			continue
		}
		func() {
			defer file.Close()
			stat, err := file.Stat()
			if err != nil || !stat.Mode().IsRegular() {
				add(a, "", "CICADA-ASSET-FORMAT", "regular WAV file", a.Path)
				return
			}
			hash, err := audioasset.SHA256(file)
			if err != nil {
				add(a, "", "CICADA-ASSET-MISSING", "readable bytes", err.Error())
				return
			}
			if hash != a.SHA256 {
				add(a, "sha256", "CICADA-ASSET-HASH", a.SHA256, hash)
			}
			if a.Format != "wav" {
				add(a, "format", "CICADA-ASSET-FORMAT", "wav", a.Format)
				return
			}
			if _, err = file.Seek(0, io.SeekStart); err != nil {
				add(a, "", "CICADA-ASSET-FORMAT", "seekable WAV", err.Error())
				return
			}
			h, err := audioasset.ReadWAVHeader(file)
			if err != nil {
				add(a, "format", "CICADA-ASSET-FORMAT", "PCM 16/24/32 or float32 mono/stereo WAV", err.Error())
				return
			}
			for _, v := range []struct {
				field        string
				want, actual int64
			}{{"frames", a.Frames, h.Frames}, {"rate", int64(a.RateHz), int64(h.RateHz)}, {"channels", int64(a.Channels), int64(h.Channels)}} {
				if v.want != v.actual {
					add(a, v.field, "CICADA-ASSET-FORMAT", fmt.Sprintf("%s=%d", v.field, v.want), fmt.Sprintf("%s=%d", v.field, v.actual))
				}
			}
		}()
	}
	return ds
}

func lowerAudio(p *Project, s *notation.Score) {
	for _, a := range s.Assets {
		p.Assets = append(p.Assets, Asset{a.Name, a.Path, a.SHA256, a.Format, a.Frames, a.RateHz, a.Channels, a.Source})
	}
	for _, c := range s.Clips {
		p.Clips = append(p.Clips, Clip{c.Name, c.Asset, c.StartFrame, c.EndFrame, c.GainDB, c.FadeInFrames, c.FadeOutFrames})
	}
	for _, v := range s.Samplers {
		p.Samplers = append(p.Samplers, Sampler{v.Name, v.Asset, v.RootMIDI, v.Mode, v.Voices})
	}
}
func validateAudioProject(p *Project) error {
	if p.HasAudio() && (p.Edition != 2 || p.Format != FormatID2) {
		return fmt.Errorf("CICADA-VERSION: audio requires edition 2 and cicada.project/2")
	}
	s := &notation.Score{Version: p.Edition}
	for _, a := range p.Assets {
		if !validID(a.Name) {
			return fmt.Errorf("invalid asset name %q", a.Name)
		}
		s.Assets = append(s.Assets, notation.Asset{Name: a.Name, Path: a.Path, SHA256: a.SHA256, Format: a.Format, Source: a.Source, Frames: a.Frames, RateHz: a.RateHz, Channels: a.Channels})
	}
	for _, c := range p.Clips {
		if !validID(c.Name) {
			return fmt.Errorf("invalid clip name %q", c.Name)
		}
		s.Clips = append(s.Clips, notation.Clip{Name: c.Name, Asset: c.Asset, StartFrame: c.StartFrame, EndFrame: c.EndFrame, GainDB: c.GainDB, FadeInFrames: c.FadeInFrames, FadeOutFrames: c.FadeOutFrames})
	}
	for _, v := range p.Samplers {
		if !validID(v.Name) {
			return fmt.Errorf("invalid sampler name %q", v.Name)
		}
		s.Samplers = append(s.Samplers, notation.Sampler{Name: v.Name, Asset: v.Asset, RootMIDI: v.RootMIDI, Mode: v.Mode, Voices: v.Voices})
	}
	if ds := notation.ValidateAudio(s); len(ds) > 0 {
		return fmt.Errorf("%s: %s", ds[0].Code, ds[0].Message)
	}
	for _, sampler := range p.Samplers {
		for _, inst := range p.Instruments {
			if inst.ID == sampler.Name {
				return fmt.Errorf("duplicate instrument %s", sampler.Name)
			}
		}
		for _, kit := range p.Kits {
			if kit.ID == sampler.Name {
				return fmt.Errorf("duplicate instrument %s", sampler.Name)
			}
		}
	}
	for _, clip := range p.Clips {
		if clip.Name == "off" || clip.Name == "keep" || clip.Name == "stop" {
			return fmt.Errorf("reserved clip name %s", clip.Name)
		}
		for _, pattern := range p.Patterns {
			if pattern.ID == clip.Name {
				return fmt.Errorf("duplicate clip/pattern %s", clip.Name)
			}
		}
	}
	return nil
}
func audioSource(p *Project) []string {
	var sections []string
	for _, a := range p.Assets {
		source := ""
		if a.Source != "" {
			source = "\n  source = " + a.Source
		}
		sections = append(sections, fmt.Sprintf("asset %s %s {\n  sha256 = %s\n  format = %s\n  frames = %d\n  rate = %dHz\n  channels = %d%s\n}", a.Name, strconv.Quote(a.Path), strconv.Quote(a.SHA256), a.Format, a.Frames, a.RateHz, a.Channels, source))
	}
	for _, c := range p.Clips {
		sections = append(sections, fmt.Sprintf("clip %s %s {\n  start = %dframes\n  end = %dframes\n  gain = %sdB\n  fade_in = %dframes\n  fade_out = %dframes\n}", c.Name, c.Asset, c.StartFrame, c.EndFrame, decimal(c.GainDB), c.FadeInFrames, c.FadeOutFrames))
	}
	for _, v := range p.Samplers {
		root := pitchNames[v.RootMIDI%12] + strconv.Itoa(v.RootMIDI/12-1)
		sections = append(sections, fmt.Sprintf("sampler %s {\n  asset = %s\n  root = %s\n  mode = %s\n  voices = %d\n}", v.Name, v.Asset, root, v.Mode, v.Voices))
	}
	return sections
}
