// Package keyboard defines the optional keyboard module boundary. It does not
// import the keyboard engines, so the core kernel can reject them without
// linking their DSP. The host registers Prepare before loading a project.
package keyboard

import "math"

const (
	MinNote        = 21
	MaxNote        = 108
	MaxVoices      = 8
	SustainControl = 0
)

type Error string

func (e Error) Error() string { return string(e) }

// Spec is immutable prepared-project data. Patch IDs 1..32 are resolved by the
// host factory. Controls[0] supplies initial sustain; other controls belong to
// the selected patch and are validated by its factory. Controls[127] selects
// the track's one-to-eight voice limit.
type Spec struct {
	Patch    uint8
	Controls [128]float32
}

// Validate checks the transport contract. Patch-specific ranges and supported
// controls are checked by Prepare, before the audio callback starts.
func (s *Spec) Validate() error {
	if s == nil || s.Patch < 1 || s.Patch > 32 {
		return Error("invalid keyboard patch")
	}
	for _, value := range s.Controls {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return Error("keyboard controls must be finite")
		}
	}
	return nil
}

// Voice has one audio owner. All methods must allocate nothing and all DSP
// preparation must finish before Prepare returns.
type Voice interface {
	NoteOn(note, velocity uint8) error
	NoteOff(note uint8)
	AllNotesOff()
	NextStereo() (float32, float32)
	SetSustain(float32) error
	Reset()
}

// Prepare is registered once by the native host or separately loaded keys
// module. A nil factory is an explicit unsupported capability, never silence.
var Prepare func(rate int, spec *Spec) (Voice, error)

// Controller is implemented by voices whose registry controls scenes and
// automation can change during playback. Index is a Spec.Controls slot. A
// glide coefficient of 1 applies the value at once; any smaller coefficient is
// the per-sample one-pole step toward it. Whole-number controls such as organ
// drawbars ignore the glide because their setters smooth the change. Unknown
// slots and out-of-range values return an error and change nothing.
type Controller interface {
	SetControl(index int, value, glide float32) error
}

// Family names the kinds of keyboard that share live controls.
type Family uint8

const (
	NoFamily Family = iota
	EP
	Clav
	Organ
	FM
	Analog
	Strings
)

// FamilyOf maps a patch ID (1..21) to its family.
func FamilyOf(patch uint8) Family {
	switch {
	case patch == 0:
		return NoFamily
	case patch <= 6:
		return EP
	case patch <= 9:
		return Clav
	case patch <= 13:
		return Organ
	case patch <= 16:
		return FM
	case patch <= 20:
		return Analog
	case patch == 21:
		return Strings
	}
	return NoFamily
}

var familyNames = [...]string{"", "ep", "clav", "organ", "fm", "analog", "strings"}

// String names the family as the parameter registry lists it in "voices".
func (f Family) String() string {
	if int(f) >= len(familyNames) {
		return ""
	}
	return familyNames[f]
}
