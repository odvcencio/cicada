package instrument

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Patch is an authored instrument graph. Adding a patch copies its source into
// the score so its parameters and sound remain editable and versioned there.
type Patch struct {
	ID          string
	Name        string
	Description string
	Mode        string
	body        string
}

var patchName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,63}$`)

// Source emits a declaration using the requested score identifier. Every patch
// has a fixed output gain; changing polyphony never changes a held note's gain.
func (p Patch) Source(name string) (string, error) {
	if !patchName.MatchString(name) || name == "acid" || name == "drums" || name == "audio" {
		return "", fmt.Errorf("choose a lowercase instrument name, excluding acid, drums, and audio")
	}
	if p.body == "" {
		return "", fmt.Errorf("unknown instrument patch")
	}
	return "instrument " + name + " {\n" + p.body + "}\n", nil
}

// Patches returns a copy of the built-in patch library in display order.
func Patches() []Patch {
	return append([]Patch(nil), patches[:]...)
}

func FindPatch(id string) (Patch, bool) {
	for _, p := range patches {
		if p.ID == id {
			return p, true
		}
	}
	return Patch{}, false
}

// ParameterLiteral accepts a finite number in a declared parameter's unit or
// an explicit compatible Cicada literal. Primitive-specific limits belong to
// the DSP implementation; this adapter does not invent a universal range.
func ParameterLiteral(program *Program, name, value string) (string, error) {
	if program == nil {
		return "", fmt.Errorf("instrument graph is required")
	}
	var parameter *Node
	for i := range program.Nodes {
		if program.Nodes[i].Op == "param" && program.Nodes[i].Name == name {
			parameter = &program.Nodes[i]
			break
		}
	}
	if parameter == nil {
		return "", fmt.Errorf("instrument does not declare parameter %q", name)
	}
	value = strings.TrimSpace(value)
	if number, err := strconv.ParseFloat(value, 64); err == nil {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return "", fmt.Errorf("instrument parameter must be finite")
		}
		switch parameter.Type {
		case Hz:
			value += "Hz"
		case MS:
			value += "ms"
		case DB:
			value += "dB"
		}
	}
	typ, err := literalType(value)
	if err != nil || typ != parameter.Type {
		return "", fmt.Errorf("parameter %s must use %s", name, parameter.Type)
	}
	number, err := numericLiteral(value)
	if err != nil || math.IsNaN(float64(number)) || math.IsInf(float64(number), 0) {
		return "", fmt.Errorf("instrument parameter must be finite and representable")
	}
	return value, nil
}

// Detune is in octaves (0.005 = 6 cents). ADSR times are milliseconds;
// brightness is the resting filter cutoff, before its separate envelope.
var patches = [...]Patch{
	{ID: "warm-pad", Name: "Warm pad", Description: "Two softly detuned saws, a slow filter bloom, and a long release.", Mode: "poly", body: `  param brightness = 1100Hz
  param resonance = 0.16
  param detune = 0.005
  param attack = 320ms
  param decay = 900ms
  param sustain = 0.72
  param release = 1400ms
  voice poly {
    let tone = (saw(pitch / exp2(detune)) + saw(pitch * exp2(detune))) * 0.5
    let amp = adsr(gate, attack, decay, sustain, release)
    let bloom = adsr(gate, 480ms, 1200ms, 0.28, 1700ms)
    out = svf(tone, brightness * exp2(bloom * 2), resonance) * amp * (0.65 + velocity * 0.35) * 0.065
  }
`},
	{ID: "wide-strings", Name: "Wide strings", Description: "Three detuned saws with a gentle bow attack and sustained, airy harmonics.", Mode: "poly", body: `  param brightness = 2400Hz
  param resonance = 0.1
  param detune = 0.0085
  param attack = 180ms
  param decay = 850ms
  param sustain = 0.82
  param release = 1100ms
  voice poly {
    let tone = (saw(pitch / exp2(detune)) + saw(pitch) + saw(pitch * exp2(detune))) / 3
    let amp = adsr(gate, attack, decay, sustain, release)
    let color = adsr(gate, 280ms, 1100ms, 0.45, 1200ms)
    out = svf(tone, brightness * exp2(color * 1.2), resonance) * amp * (0.7 + velocity * 0.3) * 0.06
  }
`},
	{ID: "poly-brass", Name: "Poly brass", Description: "A saw and pulse blend with a quick, expressive filter attack for brass and keys.", Mode: "poly", body: `  param brightness = 850Hz
  param resonance = 0.2
  param width = 0.43
  param attack = 12ms
  param decay = 550ms
  param sustain = 0.65
  param release = 180ms
  voice poly {
    let tone = (saw(pitch) + pulse(pitch * 1.003, width)) * 0.5
    let amp = adsr(gate, attack, decay, sustain, release)
    let bite = adsr(gate, 9ms, 320ms, 0.18, 160ms)
    out = svf(tone, brightness * exp2(bite * (1.7 + velocity)), resonance) * amp * (0.45 + velocity * 0.55) * 0.065
  }
`},
	{ID: "silk-pluck", Name: "Silk pluck", Description: "A narrow pulse and sine with a bright transient and short, rounded decay.", Mode: "poly", body: `  param brightness = 720Hz
  param resonance = 0.17
  param width = 0.32
  param attack = 2ms
  param decay = 320ms
  param release = 90ms
  voice poly {
    let tone = pulse(pitch, width) * 0.7 + sine(pitch) * 0.3
    let amp = adsr(gate, attack, decay, 0, release)
    let snap = adsr(gate, 1ms, 210ms, 0, 80ms)
    out = svf(tone, brightness * exp2(snap * 3.7), resonance) * amp * (0.35 + velocity * 0.65) * 0.08
  }
`},
	{ID: "round-bass", Name: "Round bass", Description: "A monophonic sine, saw, and sub blend with a warm, restrained filter punch.", Mode: "mono", body: `  param brightness = 260Hz
  param resonance = 0.12
  param attack = 4ms
  param decay = 240ms
  param sustain = 0.78
  param release = 85ms
  voice mono {
    let tone = sine(pitch) * 0.5 + saw(pitch) * 0.25 + sine(pitch / 2) * 0.25
    let amp = adsr(gate, attack, decay, sustain, release)
    let punch = adsr(gate, 2ms, 190ms, 0.1, 80ms)
    out = svf(tone, brightness * exp2(punch * 1.8), resonance) * amp * (0.55 + velocity * 0.45) * 0.25
  }
`},
	{ID: "soft-bell", Name: "Soft bell", Description: "Three gently inharmonic sine partials with a soft strike and lingering decay.", Mode: "poly", body: `  param brightness = 6500Hz
  param resonance = 0.05
  param color = 0.32
  param attack = 3ms
  param decay = 1700ms
  param release = 700ms
  voice poly {
    let body = sine(pitch) * 0.68
    let partials = sine(pitch * 2.005) * 0.22 + sine(pitch * 3.99) * 0.1
    let amp = adsr(gate, attack, decay, 0, release)
    let shimmer = adsr(gate, 2ms, 950ms, 0, 500ms)
    let tone = body + partials * (color + shimmer * 0.68)
    out = svf(tone, brightness, resonance) * amp * (0.3 + velocity * 0.7) * 0.075
  }
`},
}
