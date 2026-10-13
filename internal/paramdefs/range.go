package paramdefs

import (
	"math"
	"strconv"
	"strings"
)

// HostOnly lists device parameters that have no live kernel address, so they
// are not registry rows and do not take a ParamID. They keep the same
// descriptor shape, so range messages treat them like registry rows. The master
// transient, width and limiter stages are fixed until a score is recompiled.
// The values mirror the kernel limits in kernel/fx/pro; the agreement test in
// package project compares each row with the kernel.
var HostOnly = []Descriptor{
	{ID: "fx.transient.attack", Path: "transient.attack", Source: "attack", Scope: "global", Unit: "dB", Min: -18, Max: 18, Curve: "linear"},
	{ID: "fx.transient.sustain", Path: "transient.sustain", Source: "sustain", Scope: "global", Unit: "dB", Min: -18, Max: 18, Curve: "linear"},
	{ID: "fx.transient.fast", Path: "transient.fast", Source: "fast", Scope: "global", Unit: "ms", Min: .1, Max: 10, Curve: "log"},
	{ID: "fx.transient.slow", Path: "transient.slow", Source: "slow", Scope: "global", Unit: "ms", Min: 10, Max: 100, Curve: "log"},
	{ID: "fx.transient.release", Path: "transient.release", Source: "release", Scope: "global", Unit: "ms", Min: 20, Max: 1000, Curve: "log"},
	{ID: "fx.width.amount", Path: "width.amount", Source: "amount", Scope: "global", Unit: "ratio", Min: 0, Max: 2, Curve: "linear"},
	{ID: "fx.width.bass_mono", Path: "width.bass_mono", Source: "bass_mono", Scope: "global", Unit: "Hz", Min: 20, Max: 500, Curve: "log"},
	{ID: "fx.limiter.ceiling", Path: "limiter.ceiling", Source: "ceiling", Scope: "global", Unit: "dBTP", Min: -12, Max: 0, Curve: "linear"},
	{ID: "fx.limiter.lookahead", Path: "limiter.lookahead", Source: "lookahead", Scope: "global", Unit: "ms", Min: 1, Max: 10, Curve: "log"},
	{ID: "fx.limiter.release", Path: "limiter.release", Source: "release", Scope: "global", Unit: "ms", Min: 20, Max: 1000, Curve: "log"},
}

// DeviceParam finds the descriptor for one setting of a built-in effect, such
// as ("delay", "feedback"). It reads the registry first, then HostOnly.
func DeviceParam(kind, name string) (Descriptor, bool) {
	id := "fx." + kind + "." + name
	if d, ok := Lookup(id); ok {
		return d, true
	}
	for _, d := range HostOnly {
		if d.ID == id {
			return d, true
		}
	}
	return Descriptor{}, false
}

// TrackParam finds the descriptor for a track setting by its source spelling,
// such as "cutoff" on an acid track or "oh_decay" on a drum track. Mixer
// settings (level, pan, sends) are left to the mixer compiler.
func TrackParam(voice, name string) (Descriptor, bool) {
	for _, d := range Registry {
		if d.Scope == "track" && d.Path == name && !strings.HasPrefix(d.ID, "mix.") && HasVoice(d, voice) {
			return d, true
		}
	}
	return Descriptor{}, false
}

// RangeMessage reports a value that lies outside the descriptor's range, or a
// word outside its allowed set. It returns "" for an acceptable value and also
// for a value of the wrong kind or unit, which other checks report. label
// names the parameter as the author wrote it, and literal is the value as
// written, so the message reads "delay feedback 1.4 is above the maximum 0.95
// (range 0 to 0.95)".
func RangeMessage(d Descriptor, label, literal string) string {
	if d.Off && literal == "off" {
		return ""
	}
	for _, word := range d.Values {
		if literal == word {
			return ""
		}
	}
	number, unit, ok := rangeNumber(literal)
	if !ok {
		if d.Curve == "enum" && !numericWords(d.Values) && isWord(literal) {
			return label + " " + literal + " is not one of " + strings.Join(allowedWords(d), ", ")
		}
		return ""
	}
	if d.Curve == "enum" || d.Curve == "toggle" || unit != wantUnit(d) {
		return ""
	}
	const slack = 1e-9
	low := number < d.Min-slack*math.Max(1, math.Abs(d.Min))
	high := number > d.Max+slack*math.Max(1, math.Abs(d.Max))
	if !low && !high {
		return ""
	}
	span := "(range " + FormatBound(d, d.Min, false) + " to " + FormatBound(d, d.Max, true) + rangeSuffix(d) + ")"
	if low {
		return label + " " + literal + " is below the minimum " + FormatBound(d, d.Min, false) + " " + span
	}
	return label + " " + literal + " is above the maximum " + FormatBound(d, d.Max, true) + " " + span
}

// FormatBound writes one range limit (upper marks a maximum) with the parameter's unit, such as 150ms,
// 1.2s, 8000Hz or -60dB. Four significant digits are enough to tell the limits
// apart.
func FormatBound(d Descriptor, value float64, upper bool) string {
	rounded := roundBound(value, upper)
	text := func(v float64) string {
		clean, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 6, 64), 64)
		return strconv.FormatFloat(clean, 'f', -1, 64)
	}
	switch strings.ToLower(d.Unit) {
	case "hz":
		return text(rounded) + "Hz"
	case "ms":
		if math.Abs(rounded) >= 1000 {
			return text(rounded/1000) + "s"
		}
		return text(rounded) + "ms"
	case "db":
		return text(rounded) + "dB"
	case "dbtp":
		return text(rounded) + "dBTP"
	}
	return text(rounded)
}

// roundBound keeps four significant digits and rounds toward the inside of the
// range, so a value that matches the printed limit is never reported as outside.
func roundBound(value float64, upper bool) float64 {
	nearest, err := strconv.ParseFloat(strconv.FormatFloat(value, 'g', 4, 64), 64)
	if err != nil || math.Abs(nearest-value) <= 1e-12*math.Abs(value) {
		return value
	}
	if upper == (nearest < value) {
		return nearest
	}
	// Step one unit in the fourth digit toward the inside.
	unit := math.Pow(10, math.Floor(math.Log10(math.Abs(value)))-3)
	if upper {
		return nearest - unit
	}
	return nearest + unit
}

// FormatValue writes a base-unit number the way a score writes it, such as
// 90000Hz or 1.7, for messages about values that have no source text.
func FormatValue(d Descriptor, value float64) string {
	clean, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'g', 6, 64), 64)
	text := strconv.FormatFloat(clean, 'f', -1, 64)
	switch strings.ToLower(d.Unit) {
	case "hz":
		return text + "Hz"
	case "ms":
		return text + "ms"
	case "db":
		return text + "dB"
	case "dbtp":
		return text + "dBTP"
	}
	return text
}

func rangeSuffix(d Descriptor) string {
	switch strings.ToLower(d.Unit) {
	case "semitone":
		return " semitones"
	case "cent":
		return " cents"
	}
	return ""
}

func wantUnit(d Descriptor) string {
	switch u := strings.ToLower(d.Unit); u {
	case "", "ratio", "semitone", "cent":
		return "unit"
	default:
		return u
	}
}

func allowedWords(d Descriptor) []string {
	words := append([]string(nil), d.Values...)
	if d.Off {
		words = append(words, "off")
	}
	return words
}

func numericWords(words []string) bool {
	for _, word := range words {
		if _, err := strconv.ParseFloat(word, 64); err != nil {
			return false
		}
	}
	return true
}

func isWord(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// rangeNumber is LiteralNumber plus the dBTP suffix used by the limiter.
func rangeNumber(literal string) (float64, string, bool) {
	if lower := strings.ToLower(literal); strings.HasSuffix(lower, "dbtp") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(lower, "dbtp"), 64)
		return n, "dbtp", err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return LiteralNumber(literal)
}
