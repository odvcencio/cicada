package paramdefs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ValidateLiteral checks host source values without extending the kernel ABI.
// code identifies whether the error is a type, unit, or range mismatch.
func ValidateLiteral(d Descriptor, literal string) (code string, err error) {
	fail := func(code, message string) (string, error) { return code, fmt.Errorf("%s: %s", d.Source, message) }
	if d.Off && literal == "off" {
		return "", nil
	}
	for _, value := range d.Values {
		if literal == value {
			return "", nil
		}
	}
	if d.Curve == "enum" {
		return fail("CICADA-PRESET-TYPE", "expected one of "+strings.Join(d.Values, ", "))
	}
	if d.Curve == "toggle" {
		if literal == "on" || literal == "off" || literal == "true" || literal == "false" {
			return "", nil
		}
		return fail("CICADA-PRESET-TYPE", "expected on or off")
	}
	number, unit, ok := LiteralNumber(literal)
	if !ok {
		return fail("CICADA-PRESET-TYPE", "expected a finite number")
	}
	want := strings.ToLower(d.Unit)
	if want == "" || want == "ratio" || want == "semitone" {
		want = "unit"
	}
	if unit != want {
		return fail("CICADA-PRESET-UNIT", "expected unit "+d.Unit)
	}
	if number < d.Min || number > d.Max {
		return fail("CICADA-PRESET-RANGE", fmt.Sprintf("value is outside %g..%g", d.Min, d.Max))
	}
	if d.Curve == "integer" && number != math.Trunc(number) {
		return fail("CICADA-PRESET-TYPE", "expected an integer")
	}
	return "", nil
}

// LiteralNumber normalizes the source units shared by registry parameters.
func LiteralNumber(literal string) (float64, string, bool) {
	text, unit, scale := strings.ToLower(literal), "unit", 1.0
	for _, suffix := range []struct {
		text, unit string
		scale      float64
	}{{"khz", "hz", 1000}, {"hz", "hz", 1}, {"ms", "ms", 1}, {"db", "db", 1}, {"s", "ms", 1000}, {"%", "unit", .01}} {
		if strings.HasSuffix(text, suffix.text) {
			text = strings.TrimSuffix(text, suffix.text)
			unit, scale = suffix.unit, suffix.scale
			break
		}
	}
	n, err := strconv.ParseFloat(text, 64)
	n *= scale
	return n, unit, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

// Authored supplies host-only descriptors for declared numeric parameters.
// Authored parameters have no declared bounds; their range is finite float32.
func Authored(name, unit, literal string) Descriptor {
	n, inferred, _ := LiteralNumber(literal)
	if unit == "" {
		unit = inferred
	}
	return Descriptor{ID: "instrument." + name, Path: name, Source: name, Scope: "track", Voices: []string{"instrument"}, Unit: unit, Min: -math.MaxFloat32, Max: math.MaxFloat32, Default: n, Curve: "linear"}
}

// SamplerSettings are host-only values. The asset binding is DSP structure and
// is deliberately absent. Root values use the existing absolute pitch spelling.
var SamplerSettings = []Descriptor{
	{ID: "sampler.mode", Path: "mode", Source: "mode", Scope: "sampler", Curve: "enum", Values: []string{"oneshot", "loop"}},
	{ID: "sampler.voices", Path: "voices", Source: "voices", Scope: "sampler", Unit: "unit", Min: 1, Max: 32, Default: 1, Curve: "integer"},
	{ID: "sampler.root", Path: "root", Source: "root", Scope: "sampler", Curve: "enum", Values: samplerRoots()},
}

func samplerRoots() []string {
	var values []string
	for octave := 0; octave <= 6; octave++ {
		for _, note := range []string{"c", "c#", "db", "d", "d#", "eb", "e", "f", "f#", "gb", "g", "g#", "ab", "a", "a#", "bb", "b"} {
			values = append(values, fmt.Sprintf("%s%d", note, octave))
		}
	}
	return values
}
