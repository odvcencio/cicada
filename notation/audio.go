package notation

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Asset identifies immutable WAV bytes. Dimensions describe source frames,
// not interleaved samples. Params retain authored units and diagnostic positions.
type Asset struct {
	Name, Path, SHA256, Format, Source string
	Frames                             int64
	RateHz, Channels                   int
	Params                             []Param
	Position                           Position
}

// Clip is a half-open source-frame region; fades are measured in source frames.
type Clip struct {
	Name, Asset                                       string
	StartFrame, EndFrame, FadeInFrames, FadeOutFrames int64
	GainDB                                            float64
	Params                                            []Param
	Position                                          Position
}

type Sampler struct {
	Name, Asset, Mode string
	RootMIDI, Voices  int
	Params            []Param
	Position          Position
}

func audioDiagnostic(code, expected, actual string, p Position) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: fmt.Sprintf("expected %s; actual %s", expected, actual), Position: p}
}

// AudioReferenceMessage includes a deterministic spelling suggestion.
func AudioReferenceMessage(kind, actual string, names []string) string {
	sort.Strings(names)
	message := fmt.Sprintf("expected declared %s; actual %q", kind, actual)
	best, distance := "", 3
	for _, name := range names {
		d := editDistance(actual, name)
		if d < distance {
			best, distance = name, d
		}
	}
	if best != "" {
		message += "; did you mean " + strconv.Quote(best) + "?"
	}
	return message
}
func editDistance(a, b string) int {
	row := make([]int, len(b)+1)
	for i := range row {
		row[i] = i
	}
	for i := range a {
		prev := row[0]
		row[0] = i + 1
		for j := range b {
			old := row[j+1]
			cost := 0
			if a[i] != b[j] {
				cost = 1
			}
			row[j+1] = min(row[j]+1, row[j+1]+1, prev+cost)
			prev = old
		}
	}
	return row[len(b)]
}

// SourceFrames accepts exact frames, seconds, or milliseconds. Fractional
// source frames are errors rather than implicit rounding.
func SourceFrames(value string, rate int) (int64, error) {
	multiplier := int64(1)
	number := ""
	switch {
	case strings.HasSuffix(value, "frames"):
		number = strings.TrimSuffix(value, "frames")
	case strings.HasSuffix(value, "ms"):
		number = strings.TrimSuffix(value, "ms")
		multiplier = int64(rate)
	case strings.HasSuffix(value, "s"):
		number = strings.TrimSuffix(value, "s")
		multiplier = int64(rate)
	default:
		return 0, fmt.Errorf("expected frames, s, or ms; actual %q", value)
	}
	r, ok := new(big.Rat).SetString(number)
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	r.Mul(r, big.NewRat(multiplier, 1))
	if strings.HasSuffix(value, "ms") {
		r.Quo(r, big.NewRat(1000, 1))
	}
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, fmt.Errorf("expected exact integer source frames; actual %q", value)
	}
	return r.Num().Int64(), nil
}

func ValidAssetPath(path string) bool {
	// Reject Windows absolute paths too, so projects stay portable.
	return path != "" && filepath.IsLocal(path) && !strings.ContainsAny(path, "\\:\x00")
}
func ValidSHA256(hash string) bool {
	bytes, err := hex.DecodeString(hash)
	return err == nil && len(bytes) == 32 && hash == strings.ToLower(hash)
}

func resolveAudio(s *Score) ([]Asset, []Clip, []Sampler, []Diagnostic) {
	var ds []Diagnostic
	add := func(code, expected, actual string, p Position) {
		ds = append(ds, audioDiagnostic(code, expected, actual, p))
	}
	fields := func(params []Param, required, allowed []string, code string, position Position) map[string]Param {
		result := map[string]Param{}
		for _, p := range params {
			if _, exists := result[p.Name]; exists {
				add("CICADA-DUPLICATE", "one "+p.Name+" field", "duplicate", p.Position)
			}
			known := false
			for _, name := range allowed {
				known = known || name == p.Name
			}
			if !known {
				ds = append(ds, Diagnostic{Code: code, Severity: "error", Message: AudioReferenceMessage("field", p.Name, append([]string(nil), allowed...)), Position: p.Position})
			}
			result[p.Name] = p
		}
		for _, name := range required {
			if _, ok := result[name]; !ok {
				add(code, name+" field", "missing", position)
			}
		}
		return result
	}
	integer := func(p Param, low, high int64, code string) int64 {
		value, err := strconv.ParseInt(p.Value, 10, 64)
		if err != nil || value < low || value > high {
			add(code, fmt.Sprintf("integer %d..%d", low, high), strconv.Quote(p.Value), p.ValuePosition)
		}
		return value
	}
	var assets []Asset
	table := map[string]Asset{}
	names := []string{}
	seen := map[string]bool{}
	declaration := func(kind, name string, p Position) {
		if s.Version != 2 {
			add("CICADA-VERSION", "edition 2 for "+kind, strconv.Itoa(s.Version), p)
		}
		if len(name) == 0 || len(name) > 64 {
			add("CICADA-LIMIT", "identifier 1..64 bytes", strconv.Quote(name), p)
		}
		key := kind + ":" + name
		if seen[key] {
			add("CICADA-DUPLICATE", "unique "+kind+" name", strconv.Quote(name), p)
		}
		seen[key] = true
	}
	for _, a := range s.Assets {
		declaration("asset", a.Name, a.Position)
		if a.Params != nil {
			f := fields(a.Params, []string{"sha256", "format", "frames", "rate", "channels"}, []string{"sha256", "format", "frames", "rate", "channels", "source"}, "CICADA-ASSET-FORMAT", a.Position)
			a.SHA256, _ = strconv.Unquote(f["sha256"].Value)
			a.Format = f["format"].Value
			a.Source = f["source"].Value
			a.Frames = integer(f["frames"], 1, 1<<53-1, "CICADA-ASSET-FORMAT")
			a.Channels = int(integer(f["channels"], 1, 2, "CICADA-ASSET-FORMAT"))
			value := f["rate"].Value
			a.RateHz = 0
			if strings.HasSuffix(value, "Hz") {
				rate, err := strconv.ParseInt(strings.TrimSuffix(value, "Hz"), 10, 32)
				if err == nil {
					a.RateHz = int(rate)
				}
			}
			if a.RateHz < 8000 || a.RateHz > 384000 {
				add("CICADA-ASSET-FORMAT", "integer 8000..384000Hz", strconv.Quote(value), f["rate"].ValuePosition)
			}
		}
		if !ValidSHA256(a.SHA256) {
			add("CICADA-ASSET-HASH", "64 lowercase SHA-256 hex digits", strconv.Quote(a.SHA256), audioFieldPosition(a.Params, "sha256", a.Position))
		}
		if !ValidAssetPath(a.Path) {
			add("CICADA-ASSET-PATH", "project-relative path inside project", strconv.Quote(a.Path), a.Position)
		}
		if a.Format != "wav" {
			add("CICADA-ASSET-FORMAT", "wav (PCM 16/24/32 or float32)", strconv.Quote(a.Format), audioFieldPosition(a.Params, "format", a.Position))
		}
		if a.Frames < 1 || a.Frames > 1<<53-1 || a.Channels < 1 || a.Channels > 2 || a.RateHz < 8000 || a.RateHz > 384000 {
			add("CICADA-ASSET-FORMAT", "positive frames, 1..2 channels, 8000..384000Hz", fmt.Sprintf("frames=%d channels=%d rate=%dHz", a.Frames, a.Channels, a.RateHz), a.Position)
		}
		if a.Source != "" && a.Source != "recorded" && a.Source != "imported" && a.Source != "generated" {
			add("CICADA-ASSET-FORMAT", "source recorded, imported, or generated", a.Source, a.Position)
		}
		assets = append(assets, a)
		table[a.Name] = a
		names = append(names, a.Name)
	}
	reference := func(name string, p Position) (Asset, bool) {
		a, ok := table[name]
		if !ok {
			ds = append(ds, Diagnostic{Code: "CICADA-REFERENCE", Severity: "error", Message: AudioReferenceMessage("asset", name, append([]string(nil), names...)), Position: p})
		}
		return a, ok
	}
	var clips []Clip
	for _, c := range s.Clips {
		declaration("clip", c.Name, c.Position)
		if c.Name == "off" || c.Name == "keep" || c.Name == "stop" {
			add("CICADA-DUPLICATE", "unreserved clip name", c.Name, c.Position)
		}
		for _, pattern := range s.Patterns {
			if pattern.Name == c.Name {
				add("CICADA-DUPLICATE", "distinct clip and pattern names", c.Name, c.Position)
			}
		}
		a, ok := reference(c.Asset, c.Position)
		if c.Params != nil {
			f := fields(c.Params, nil, []string{"start", "end", "gain", "fade_in", "fade_out"}, "CICADA-CLIP-RANGE", c.Position)
			c.EndFrame = a.Frames
			for _, field := range []struct {
				name   string
				target *int64
			}{{"start", &c.StartFrame}, {"end", &c.EndFrame}, {"fade_in", &c.FadeInFrames}, {"fade_out", &c.FadeOutFrames}} {
				if p, exists := f[field.name]; exists && ok {
					value, err := SourceFrames(p.Value, a.RateHz)
					*field.target = value
					if err != nil {
						add("CICADA-CLIP-RANGE", "exact source frames in frames, s, or ms", strconv.Quote(p.Value), p.ValuePosition)
					}
				}
			}
			if p, exists := f["gain"]; exists {
				value, err := strconv.ParseFloat(strings.TrimSuffix(p.Value, "dB"), 64)
				c.GainDB = value
				if err != nil || !strings.HasSuffix(p.Value, "dB") {
					add("CICADA-CLIP-RANGE", "gain -60..24dB", p.Value, p.ValuePosition)
				}
			}
		}
		if ok && (c.StartFrame < 0 || c.EndFrame <= c.StartFrame || c.EndFrame > a.Frames || c.FadeInFrames < 0 || c.FadeOutFrames < 0 || c.FadeInFrames > c.EndFrame-c.StartFrame || c.FadeOutFrames > c.EndFrame-c.StartFrame || !finiteMixerNumber(c.GainDB) || c.GainDB < -60 || c.GainDB > 24) {
			add("CICADA-CLIP-RANGE", fmt.Sprintf("0 <= start < end <= %dframes; fades <= region; gain -60..24dB", a.Frames), fmt.Sprintf("[%d,%d) fades=%d,%d gain=%gdB", c.StartFrame, c.EndFrame, c.FadeInFrames, c.FadeOutFrames, c.GainDB), c.Position)
		}
		clips = append(clips, c)
	}
	var samplers []Sampler
	for _, sampler := range s.Samplers {
		declaration("sampler", sampler.Name, sampler.Position)
		if sampler.Params != nil {
			f := fields(sampler.Params, []string{"asset", "root", "mode", "voices"}, []string{"asset", "root", "mode", "voices"}, "CICADA-SAMPLER-PARAM", sampler.Position)
			sampler.Asset = f["asset"].Value
			sampler.Mode = f["mode"].Value
			sampler.Voices = int(integer(f["voices"], 1, 32, "CICADA-SAMPLER-PARAM"))
			sampler.RootMIDI = -1
			root := f["root"].Value
			if len(root) >= 2 && len(root) <= 3 && root[0] >= 'a' && root[0] <= 'g' && root[len(root)-1] >= '0' && root[len(root)-1] <= '6' {
				pitch := map[byte]int{'c': 0, 'd': 2, 'e': 4, 'f': 5, 'g': 7, 'a': 9, 'b': 11}[root[0]]
				if len(root) == 3 {
					if root[1] == '#' {
						pitch++
					} else if root[1] == 'b' {
						pitch--
					} else {
						pitch = -1000
					}
				}
				sampler.RootMIDI = 12*(int(root[len(root)-1]-'0')+1) + pitch
			}
		}
		reference(sampler.Asset, audioFieldPosition(sampler.Params, "asset", sampler.Position))
		if sampler.RootMIDI < 12 || sampler.RootMIDI > 95 || sampler.Mode != "oneshot" && sampler.Mode != "loop" || sampler.Voices < 1 || sampler.Voices > 32 {
			add("CICADA-SAMPLER-PARAM", "absolute root c0..b6, mode oneshot or loop, voices 1..32", fmt.Sprintf("root MIDI=%d mode=%q voices=%d", sampler.RootMIDI, sampler.Mode, sampler.Voices), sampler.Position)
		}
		for _, inst := range s.Instruments {
			if inst.Name == sampler.Name {
				add("CICADA-DUPLICATE", "unique instrument name", sampler.Name, sampler.Position)
			}
		}
		for _, kit := range s.Kits {
			if kit.Name == sampler.Name {
				add("CICADA-DUPLICATE", "unique instrument name", sampler.Name, sampler.Position)
			}
		}
		if sampler.Name == "acid" || sampler.Name == "drums" || sampler.Name == "audio" {
			add("CICADA-DUPLICATE", "unreserved sampler name", sampler.Name, sampler.Position)
		}
		samplers = append(samplers, sampler)
	}
	return assets, clips, samplers, ds
}
func audioFieldPosition(params []Param, name string, fallback Position) Position {
	for _, p := range params {
		if p.Name == name {
			return p.ValuePosition
		}
	}
	return fallback
}
