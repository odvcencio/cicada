package project

import (
	"fmt"
	"m31labs.dev/cicada/host/keyboard"
	keyvoice "m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/notation"
	"math"
)

func isModeledKeys(p *Project, kind string) bool {
	if p == nil || keyboard.ID(kind) == 0 {
		return false
	}
	for _, i := range p.Instruments {
		if i.ID == kind {
			return false
		}
	}
	for _, k := range p.Kits {
		if k.ID == kind {
			return false
		}
	}
	for _, s := range p.Samplers {
		if s.Name == kind {
			return false
		}
	}
	return true
}
func (p *Project) NeedsKeysEngine() bool {
	for _, t := range p.Tracks {
		if isModeledKeys(p, t.Kind) {
			return true
		}
	}
	return false
}

// KeysSpecFromValues resolves the original patch and all authored controls.
// Hosts prepare the resulting instrument before taking ownership of audio.
func KeysSpecFromValues(kind string, values map[string]Value) (keyvoice.Spec, error) {
	s, err := keyboard.DefaultSpec(kind)
	if err != nil {
		return s, err
	}
	params := keyboard.Parameters(kind)
	for name, v := range values {
		if name == "octave" {
			if err := validateOctaveValue(v); err != nil {
				return s, err
			}
			continue
		}
		found := false
		for _, p := range params {
			if p.Name != name {
				continue
			}
			found = true
			if v.Unit == "enum" {
				n, ok := keysEnum(name, v.Text)
				if !ok {
					return s, fmt.Errorf("invalid keys value %s=%s", name, v.Text)
				}
				s.Controls[p.Index] = n
				break
			}
			if v.Number == nil || math.IsNaN(*v.Number) || math.IsInf(*v.Number, 0) {
				return s, fmt.Errorf("keys parameter %s needs a finite number", name)
			}
			n := *v.Number
			unit := v.Unit
			if p.Unit == "s" && unit == "ms" {
				n /= 1000
				unit = "s"
			}
			if p.Unit == "s" && unit == "unit" {
				unit = "s"
			}
			if p.Unit == "cents" && unit == "unit" {
				unit = "cents"
			}
			want := p.Unit
			if want == "" {
				want = "unit"
			}
			if unit != want {
				return s, fmt.Errorf("keys parameter %s requires %s", name, want)
			}
			if n < float64(p.Min)-1e-7 || n > float64(p.Max)+1e-7 || p.Integer && n != math.Trunc(n) {
				return s, fmt.Errorf("keys parameter %s outside %g..%g", name, p.Min, p.Max)
			}
			s.Controls[p.Index] = float32(n)
			break
		}
		if !found {
			return s, fmt.Errorf("unknown %s parameter %s", kind, name)
		}
	}
	if err := keyboard.Validate(&s); err != nil {
		return s, err
	}
	return s, nil
}
func keysEnum(name, value string) (float32, bool) {
	var names []string
	switch name {
	case "pickup":
		names = []string{"bridge", "neck", "both", "difference"}
	case "percussion":
		names = []string{"off", "second", "third"}
	case "scanner":
		names = []string{"off", "v1", "v2", "v3", "c1", "c2", "c3"}
	case "filter":
		names = []string{"ladder", "state_variable"}
	case "percussion_fast", "rotary_fast":
		names = []string{"slow", "fast"}
	case "percussion_soft":
		names = []string{"normal", "soft"}
	case "sustain":
		names = []string{"off", "on"}
	default:
		return 0, false
	}
	for n, x := range names {
		if value == x {
			return float32(n), true
		}
	}
	return 0, false
}
func CompileKeysSpec(track notation.Track) (keyvoice.Spec, error) {
	values := map[string]Value{}
	for _, p := range track.Params {
		if IsMixerSourceParam(p.Name) {
			continue
		}
		v, err := projectValue(p.Value)
		if err != nil {
			return keyvoice.Spec{}, err
		}
		values[p.Name] = v
	}
	return KeysSpecFromValues(track.Kind, values)
}

func notationKeysBuiltin(s *notation.Score, kind string) bool {
	if keyboard.ID(kind) == 0 {
		return false
	}
	for _, i := range s.Instruments {
		if i.Name == kind {
			return false
		}
	}
	for _, k := range s.Kits {
		if k.Name == kind {
			return false
		}
	}
	for _, x := range s.Samplers {
		if x.Name == kind {
			return false
		}
	}
	return true
}
