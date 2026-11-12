package controls

import (
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// LiteralSuffix is the unit a declaration writes after a number for the
// control, such as ms or Hz; plain numbers (ratios, cents of a keyboard
// detune, semitones) take none.
func LiteralSuffix(c Control) string {
	switch strings.ToLower(c.descriptor.Unit) {
	case "hz":
		return "Hz"
	case "ms":
		return "ms"
	case "db":
		return "dB"
	case "dbtp":
		return "dBTP"
	case "ct":
		return "ct"
	case "frames":
		return "frames"
	}
	return ""
}

// Literal writes a number in the control's source form, such as 250ms.
func Literal(c Control, v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + LiteralSuffix(c)
}

// Example is a value the control's declaration accepts: its default in source
// form, or a representative value when it has no single default.
func Example(c Control) string {
	switch v := c.Default.(type) {
	case float64:
		return Literal(c, v)
	case string:
		return v
	}
	switch c.ID {
	case "sampler.root":
		return "c3"
	case "comp.sidechain":
		return "music"
	}
	if c.Min != nil {
		return Literal(c, *c.Min)
	}
	if len(c.Values) > 0 {
		return c.Values[0]
	}
	return ""
}

// CatalogNumber reads a source literal as a number in the control's catalog
// unit. It reports false for a word or a malformed value.
func CatalogNumber(c Control, literal string) (float64, bool) {
	n, unit, err := paramdefs.ParseBase(strings.TrimSpace(literal))
	if err != nil || !finite(n) {
		return 0, false
	}
	if unit == "unit" && c.secondsPlain {
		n *= 1000
	}
	return n, true
}

// AcceptsWord reports whether literal is one of the words the control takes.
func AcceptsWord(c Control, literal string) bool {
	for _, word := range c.Values {
		if word == literal {
			return true
		}
	}
	return false
}

// Validate checks a source literal's form and range for the control as its
// validator does: the unit, the type and the bounds.
func Validate(c Control, literal string) error {
	d := c.descriptor
	if strings.ToLower(d.Unit) == "ct" {
		_, err := notation.VibratoSetting(c.Name, literal)
		return err
	}
	// Cents, like semitones and ratios, are plain source numbers.
	if d.Unit == "cent" {
		d.Unit = ""
	}
	if d.Type == "integer" && d.Curve != "enum" && d.Curve != "toggle" {
		d.Curve = "integer"
	}
	_, err := paramdefs.ValidateLiteral(d, literal)
	return err
}
