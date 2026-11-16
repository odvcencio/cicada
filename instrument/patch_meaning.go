//go:build !tinygo

package instrument

// ParameterMeaning says in one line what a parameter of the built-in patch
// library does, for the control catalog (cicada params and cicada explain).
// The patches share names, so one table covers them; a test fails when a
// patch declares a parameter that has no meaning here. ADSR times are
// milliseconds, as the patches declare them.
func ParameterMeaning(name string) string { return patchParameterMeanings[name] }

var patchParameterMeanings = map[string]string{
	"brightness": "resting cutoff of the patch's filter, before its own envelope opens it; higher values pass more upper harmonics",
	"resonance":  "emphasis at the filter cutoff; high values ring",
	"detune":     "spread between the detuned saws, in octaves (0.005 is about 6 cents)",
	"width":      "pulse width of the pulse oscillator; 0.5 is square",
	"color":      "level of the inharmonic upper partials over the bell's body",
	"attack":     "time the amplitude envelope takes to reach full level",
	"decay":      "time the amplitude envelope takes to fall to the sustain level",
	"sustain":    "level the amplitude envelope holds while the note is down, as a fraction of full level",
	"release":    "time a note takes to fade after it ends",
}

// ParameterMeaningNames lists the parameter names the table describes.
func ParameterMeaningNames() []string {
	var names []string
	for name := range patchParameterMeanings {
		names = append(names, name)
	}
	return names
}
