package measure

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

// graphModulationCases time graph voices whose filter cutoff or comb period
// moves on every sample. The graph_filters entry holds its cutoff still, so it
// computes each coefficient once per note. In a score an envelope sweeps the
// cutoff, or vibrato moves the pitch that a comb is tuned to, and then the
// voice recomputes the coefficient on every sample: the state-variable filter
// its tangent, the ladder and diode TanPi32, the comb an arctangent, a sine, a
// cosine and two tangents. Each instrument is copied from the example named in
// its description and compiled by the score compiler, as a score would be.
func graphModulationCases() []voiceCase {
	const aliasNA = "this entry times coefficients that move on every sample; oscillator aliasing is measured by the graph_saw and graph_quality entries"
	const responseNA = "the cutoff moves on every sample by design; fixed-cutoff filter curves are measured by the graph_filters entry"
	return []voiceCase{
		{
			Name:        "graph_svf_sweep",
			Description: "warm-pad from examples/expressive-instruments.cicada with its track's brightness of 900 Hz: an ADSR sweeps the quality-path SVF's cutoff through exp2, so the filter recomputes its tangent at every step of the 2x path.",
			New:         compiledGraphVoice(warmPadSource, map[string]string{"brightness": "900Hz"}, expression.Params{}),
			AliasNA:     aliasNA,
			ResponseNA:  responseNA,
		},
		{
			Name:        "graph_ladder_sweep",
			Description: "nightbass from examples/cicada-chorus.cicada: an envelope sweeps the ladder's and the diode's cutoff through exp2 on every sample, then a fixed lowpass and highpass.",
			New:         compiledGraphVoice(nightbassSource, nil, expression.Params{}),
			AliasNA:     aliasNA,
			ResponseNA:  responseNA,
		},
		{
			Name:        "graph_comb_vibrato",
			Description: "plucked from examples/pluck.cicada with per-note vibrato of 14 cents at 7 Hz (examples/shared-gestures/main.cicada): the comb's period follows the pitch, so it retunes its allpass on every sample.",
			New:         compiledGraphVoice(pluckedSource, nil, expression.Params{VibratoRateHz: 7, VibratoDepthCents: 14}),
			AliasNA:     aliasNA,
			ResponseNA:  "the comb is a resonator tuned to the note; it has no cutoff to measure",
		},
	}
}

// From examples/expressive-instruments.cicada.
const warmPadSource = `instrument warm-pad {
  param brightness = 1100Hz
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
}`

// From examples/cicada-chorus.cicada (its octave line moves the score's
// notes, not the graph, and is left out).
const nightbassSource = `instrument nightbass {
  param cutoff = 480Hz
  param decay = 0.3s
  param bite = 60%
  voice mono {
    let body = mix(saw(pitch), square(pitch / 2), 0.3)
    let shape = env(gate, decay)
    let sweep = cutoff * exp2(shape * 2.5)
    let dark = lowpass(ladder(body, sweep, bite), sample_rate / 4)
    out = highpass(diode(dark, sweep + 120Hz, 0.2), 30Hz) * (shape * velocity)
  }
}`

// From examples/pluck.cicada.
const pluckedSource = `instrument plucked {
  param feedback: unit = 0.995
  param damping: unit = 0.5
  voice mono {
    let burst = noise() * env(gate, 1ms) * velocity
    let string = comb(burst, 1 / pitch, feedback, damping)
    out = string * env(gate, 1600ms) * 0.3
  }
}`

// compiledGraphVoice compiles one instrument as a score would and plays it
// with the given per-note expression, which NoteOn would otherwise reset.
func compiledGraphVoice(source string, overrides map[string]string, note expression.Params) func(int) (block, error) {
	return func(key int) (block, error) {
		p, err := compileGraphInstrument(source, overrides)
		if err != nil {
			return nil, err
		}
		v, err := graph.NewVoice(p, sampleRate)
		if err != nil {
			return nil, err
		}
		if key >= 0 {
			v.NoteOn(uint8(key), velocity, false)
			if note != (expression.Params{}) {
				v.SetExpression(note)
			}
		}
		return monoBlock(v.Next), nil
	}
}

func compileGraphInstrument(source string, overrides map[string]string) (graph.Program, error) {
	fields := strings.Fields(source) // "instrument" <name> "{" ...
	if len(fields) < 2 {
		return graph.Program{}, fmt.Errorf("no instrument name")
	}
	score, diagnostics := notation.ParseEdition([]byte(source+"\ntrack t "+fields[1]+" {}\npattern p { 1 }\nscene s { t = p }\nsong { s }\n"), 2)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return graph.Program{}, fmt.Errorf("parse: %v", d)
		}
	}
	if len(score.Instruments) != 1 {
		return graph.Program{}, fmt.Errorf("parse: %d instruments, want 1", len(score.Instruments))
	}
	compiled, diagnostics := instrument.Compile(score.Instruments[0])
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return graph.Program{}, fmt.Errorf("compile: %v", d)
		}
	}
	return instrument.Lower(compiled, overrides)
}
