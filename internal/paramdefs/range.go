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
	{ID: "fx.width.bass_mono", Path: "width.bass_mono", Source: "bass_mono", Scope: "global", Unit: "Hz", Min: 20, Max: 500, Curve: "log", Isolated: []float64{0}, IsolatedNote: "bypass"},
	// A master EQ is built by pro.New at 48 kHz, so its frequency limit is
	// 0.45 * 48000. A track EQ keeps the registry row, which stops at 19845 Hz.
	{ID: "fx.master.eq.frequency", Path: "master.eq.frequency", Source: "frequency", Scope: "global", Unit: "Hz", Min: 10, Max: 21600, Curve: "log"},
	{ID: "fx.limiter.ceiling", Path: "limiter.ceiling", Source: "ceiling", Scope: "global", Unit: "dBTP", Min: -12, Max: 0, Curve: "linear"},
	{ID: "fx.limiter.lookahead", Path: "limiter.lookahead", Source: "lookahead", Scope: "global", Unit: "ms", Min: 1, Max: 10, Curve: "log"},
	{ID: "fx.limiter.release", Path: "limiter.release", Source: "release", Scope: "global", Unit: "ms", Min: 20, Max: 1000, Curve: "log"},
}

// DeviceParam finds the descriptor for one setting of a built-in effect, such
// as ("delay", "feedback"). It reads the registry first, then HostOnly.
//
// Pass master for an effect inserted on the master chain. A device can take
// different limits on different routes; today only the EQ frequency does. Track
// inserts and bus inserts share the registry rows.
func DeviceParam(kind, name string, master bool) (Descriptor, bool) {
	if master {
		for _, d := range HostOnly {
			if d.ID == "fx.master."+kind+"."+name {
				return d, true
			}
		}
	}
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
	return rangeMessage(d, label, literal, math.NaN())
}

// RangeMessageValue is RangeMessage for a value that has no source text, such
// as an automation point in a typed project. The float64 is compared with the
// bounds as it is; the text is only formatted for the message.
func RangeMessageValue(d Descriptor, label string, value float64) string {
	return rangeMessage(d, label, FormatValue(d, value), value)
}

func rangeMessage(d Descriptor, label, literal string, exact float64) string {
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
	if !math.IsNaN(exact) {
		number = exact
	}
	// Only a literal that was scaled (kHz, s, %) can carry multiplication error;
	// the project compilers convert those with exact decimal shifts. Values in
	// the base unit are compared exactly.
	slack := 0.0
	if lower := strings.ToLower(literal); math.IsNaN(exact) && (strings.HasSuffix(lower, "khz") || strings.HasSuffix(lower, "s") && !strings.HasSuffix(lower, "ms") || strings.HasSuffix(lower, "%")) {
		slack = 1e-9
	}
	low, high := outside(d, number, slack)
	if !low && !high {
		return ""
	}
	for _, v := range d.Isolated {
		if number == v {
			return ""
		}
	}
	lowText, highText := FormatBound(d, d.Min, false), FormatBound(d, d.Max, true)
	// Never print a value equal to the bound it is said to break.
	if literal == lowText {
		lowText = FormatValue(d, d.Min)
	}
	if literal == highText {
		highText = FormatValue(d, d.Max)
	}
	extra := ""
	for _, v := range d.Isolated {
		extra += ", or " + strconv.FormatFloat(v, 'f', -1, 64) + " to " + d.IsolatedNote
	}
	span := "(range " + lowText + " to " + highText + rangeSuffix(d) + extra + ")"
	if low {
		return label + " " + literal + " is below the minimum " + lowText + " " + span
	}
	return label + " " + literal + " is above the maximum " + highText + " " + span
}

// KeyboardTolerance is the absolute slack project/keys.go allows around a
// keyboard parameter's float32 limits.
const KeyboardTolerance = 1e-7

// Tolerance is the absolute slack the validator that owns the row allows
// around its limits: KeyboardTolerance for modeled keyboards, none elsewhere.
func Tolerance(d Descriptor) float64 {
	if strings.HasPrefix(d.ID, "keys.") {
		return KeyboardTolerance
	}
	return 0
}

// outside applies the comparison of the validator that owns the row. Most rows
// compare the float64 exactly (slack is only for scaled literals). A keyboard
// value must lie within KeyboardTolerance of its float32 limits and, after
// conversion to float32, within the limits themselves, as project/keys.go and
// host/keyboard.Validate do in turn.
func outside(d Descriptor, number, slack float64) (low, high bool) {
	if Tolerance(d) == 0 {
		return number < d.Min-slack*math.Max(1, math.Abs(d.Min)), number > d.Max+slack*math.Max(1, math.Abs(d.Max))
	}
	min32, max32 := float64(float32(d.Min)), float64(float32(d.Max))
	narrowed := float64(float32(number))
	return number < min32-KeyboardTolerance || narrowed < min32, number > max32+KeyboardTolerance || narrowed > max32
}

// FormatBound writes one range limit (upper marks a maximum) with the parameter's unit, such as 150ms,
// 1.2s, 8000Hz or -60dB. Six significant digits keep every limit exact except the tom tune limits.
func FormatBound(d Descriptor, value float64, upper bool) string {
	rounded := roundBound(value, upper)
	text := func(v float64) string {
		clean, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 8, 64), 64)
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

// roundBound keeps six significant digits and rounds toward the inside of the
// range, so a value that matches the printed limit is never reported as outside.
func roundBound(value float64, upper bool) float64 {
	nearest, err := strconv.ParseFloat(strconv.FormatFloat(value, 'g', 6, 64), 64)
	if err != nil || math.Abs(nearest-value) <= 1e-12*math.Abs(value) {
		return value
	}
	if upper == (nearest < value) {
		return nearest
	}
	// Step one unit in the sixth digit toward the inside.
	unit := math.Pow(10, math.Floor(math.Log10(math.Abs(value)))-5)
	if upper {
		return nearest - unit
	}
	return nearest + unit
}

// FormatValue writes a base-unit number the way a score writes it, such as
// 90000Hz or 1.7, for messages about values that have no source text.
func FormatValue(d Descriptor, value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
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
