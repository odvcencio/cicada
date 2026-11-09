package measure

import (
	"math"

	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/voice/acid"
	"m31labs.dev/cicada/kernel/voice/clav"
	"m31labs.dev/cicada/kernel/voice/ddsp"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/ep"
	"m31labs.dev/cicada/kernel/voice/fm"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
	"m31labs.dev/cicada/kernel/voice/organ"
	"m31labs.dev/cicada/kernel/voice/piano"
	"m31labs.dev/cicada/kernel/voice/polykeys"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/kernel/voice/strings"
)

const velocity = 100

// The notes of a keyboard-style instrument are all triggered the same way.
type keyed interface {
	NoteOn(note, velocity uint8) error
	NextStereo() (float32, float32)
}

func keyedBlock(i keyed, note int) (block, error) {
	if note >= 0 {
		if err := i.NoteOn(uint8(note), velocity); err != nil {
			return nil, err
		}
	}
	return stereoBlock(i.NextStereo), nil
}

func voiceCases() []voiceCase {
	return []voiceCase{
		{
			Name:        "acid",
			Description: "TB-style monophonic bass voice (kernel/voice/acid). Alias stress: sawtooth, cutoff 8 kHz with full envelope amount and 3 s decay (open), no resonance or drive.",
			New: func(note int) (block, error) {
				p := acid.DefaultParams()
				p.Cutoff, p.Resonance, p.EnvMod, p.Decay, p.Drive, p.Gate = 8_000, 0, 1, 3, 0, 100
				v, err := acid.New(sampleRate)
				if err != nil {
					return nil, err
				}
				if err := v.SetParams(p); err != nil {
					return nil, err
				}
				if note >= 0 {
					v.NoteOn(uint8(note), false, false, velocity)
				}
				return func(l, r []float32) { v.Render(l); copy(r, l) }, nil
			},
			ResponseNA: "the ladder and diode filters are unexported; their -3 dB cutoff is gated inside the package by TestDecision0002AcidFilterCutoffGate and TestLadderLinearResponse",
		},
		{
			Name:        "clav",
			Description: "Clavinet string model (kernel/voice/clav), default patch.",
			New: func(note int) (block, error) {
				p, err := clav.Patch("clav")
				if err != nil {
					return nil, err
				}
				i, err := clav.New(sampleRate, p)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
		},
		{
			Name:        "ddsp",
			Description: "Quantized harmonic-plus-noise neural voice (kernel/voice/ddsp), loudness 0.5.",
			New: func(note int) (block, error) {
				s, err := ddsp.New(sampleRate)
				if err != nil {
					return nil, err
				}
				hz, level := float32(0), float32(0)
				if note >= 0 {
					hz, level = float32(midiHz(note)), .5
				}
				return monoBlock(func() float32 { return s.NextFloat(hz, level) }), nil
			},
			AliasNA: "the voice adds a filtered noise component by design, so the off-harmonic energy is the noise gain the network predicts, not aliasing; the eight-harmonic oscillator is integer-phase and cannot fold below 0.49 of the sample rate",
		},
		{
			Name:        "drum",
			Description: "Eleven-lane drum machine (kernel/voice/drum), seed 1, kick lane for CPU and silence.",
			New: func(note int) (block, error) {
				k, err := drum.New(sampleRate, 1)
				if err != nil {
					return nil, err
				}
				if note >= 0 {
					k.Hit(drum.BD, velocity, false)
				}
				return stereoBlock(k.NextStereo), nil
			},
			AliasNA: "drum lanes are noise and inharmonic resonances, with no pitch to hold above C7",
		},
		{
			Name:        "ep",
			Description: "Electric piano, tine model (kernel/voice/ep), tine_ep patch.",
			New: func(note int) (block, error) {
				p, err := ep.Patch("tine_ep")
				if err != nil {
					return nil, err
				}
				p.Tremolo = 0
				i, err := ep.New(sampleRate, p)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
		},
		{
			Name:        "fm",
			Description: "Six-operator FM (kernel/voice/fm), fm_ep patch (operator ratios 1 and 14).",
			New: func(note int) (block, error) {
				p, err := fm.Patch("fm_ep")
				if err != nil {
					return nil, err
				}
				i, err := fm.New(sampleRate, p)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
		},
		{
			Name:        "guitar",
			Description: "Physical string with pickup (kernel/voice/guitar), default controls. The model clamps pitch at about 2 kHz, so notes are C6 and C7 (C7 sounds at 2000 Hz).",
			Notes:       []int{84, 96},
			New: func(note int) (block, error) {
				v, err := guitar.New(sampleRate, guitar.DefaultParams())
				if err != nil {
					return nil, err
				}
				if note >= 0 {
					v.NoteOn(uint8(note), velocity, false, false)
				}
				return monoBlock(v.Next), nil
			},
		},
		{
			Name:        "modal",
			Description: "Struck-bar and pan resonators (kernel/voice/modal), marimba profile.",
			New: func(note int) (block, error) {
				v, err := modal.NewVoice(modal.Profile(1), sampleRate)
				if err != nil {
					return nil, err
				}
				if note >= 0 {
					v.NoteOn(uint8(note), velocity, false)
				}
				return monoBlock(v.Next), nil
			},
			// Upper bar modes sit beyond the Nyquist frequency or far below the fundamental at C7 and above.
		},
		{
			Name:        "modeledkit",
			Description: "Modeled drum and cymbal voices (kernel/voice/modeledkit), snare profile, seed 1.",
			New: func(note int) (block, error) {
				v, err := modeledkit.NewVoice(modeledkit.Snare, sampleRate, 1)
				if err != nil {
					return nil, err
				}
				if note >= 0 {
					v.Hit(velocity)
				}
				return monoBlock(v.Next), nil
			},
			AliasNA: "drum voices are noise and inharmonic resonances, with no pitch to hold above C7",
		},
		{
			Name:        "organ",
			Description: "Tonewheel organ (kernel/voice/organ), default drawbars. Alias stress: scanner vibrato, rotary, key click, leakage, crosstalk and drive off.",
			New: func(note int) (block, error) {
				p := organ.DefaultParams()
				p.Percussion, p.Scanner, p.KeyClick, p.Leakage, p.Crosstalk, p.Drive, p.RotaryMix = organ.PercussionOff, organ.ScannerOff, 0, 0, 0, 0, 0
				i, err := organ.New(sampleRate, p)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
			// Drawbars sit at equal-tempered intervals above the key, not at integer
			// multiples: -12, +7, 0, +12, +19, +24, +28, +31 and +36 semitones. The
			// wheel foldback moves a pitch by whole octaves, so each is listed at
			// -3 to +3 octaves.
			Partials: func(_ int, f0 float64) []float64 {
				var out []float64
				for _, semitones := range []float64{-12, 7, 0, 12, 19, 24, 28, 31, 36} {
					for octave := -3.0; octave <= 3; octave++ {
						if f := f0 * math.Exp2(semitones/12+octave); f > 20 && f < sampleRate/2 {
							out = append(out, f)
						}
					}
				}
				return out
			},
		},
		{
			Name:        "piano",
			Description: "Modal grand piano (kernel/voice/piano). Expected partials follow the stiff-string law in piano.PartialFrequency.",
			New: func(note int) (block, error) {
				i, err := piano.New(sampleRate)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
			Partials: func(note int, _ float64) []float64 {
				var out []float64
				for n := 1; n <= piano.MaxModes; n++ {
					if f := piano.PartialFrequency(uint8(note), n); f > 0 && f < sampleRate/2 {
						out = append(out, f)
					}
				}
				return out
			},
		},
		{
			Name:        "polykeys",
			Description: "Polyphonic analog-style keys (kernel/voice/polykeys). Alias stress: sawtooth only, filter open, no detune, drift, PWM, sync, sub or chorus.",
			New: func(note int) (block, error) {
				p := polykeys.DefaultParams()
				p.Saw, p.Pulse, p.Sub, p.Detune, p.Drift, p.PWM, p.Chorus = 1, 0, 0, 0, 0, 0, 0
				p.Cutoff, p.Resonance, p.FilterEnv, p.Drive, p.KeyTrack = 18_000, 0, 0, 0, 0
				p.Attack, p.Sustain = .001, 1
				i, err := polykeys.New(sampleRate, p)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
		},
		{
			Name:        "sample",
			Description: "Sample playback with sample-rate conversion (kernel/voice/sample). Source: three-harmonic tone at 261.63 Hz played from root 60, so C7 reads at 8x, the largest ratio the voice accepts.",
			Notes:       []int{96},
			New: func(note int) (block, error) {
				const frames = 1 << 20
				left := make([]float32, frames)
				f := midiHz(60)
				for i := range left {
					t := 2 * math.Pi * f * float64(i) / sampleRate
					left[i] = float32(.4*math.Sin(t) + .2*math.Sin(2*t+.3) + .1*math.Sin(3*t+.7))
				}
				v, err := sample.New(sampleRate, sample.Region{Left: left, SampleRate: sampleRate, RootKey: 60, End: frames})
				if err != nil {
					return nil, err
				}
				if note >= 0 {
					if err := v.NoteOn(uint8(note), velocity); err != nil {
						return nil, err
					}
				}
				return stereoBlock(v.NextStereo), nil
			},
		},
		{
			Name:        "strings",
			Description: "String machine (kernel/voice/strings). Alias stress: ensemble off, filter at 16 kHz, 8 foot only.",
			New: func(note int) (block, error) {
				p := strings.DefaultParams()
				p.Octave16, p.Octave8, p.Octave4, p.Ensemble, p.Cutoff, p.Attack = 0, 1, 0, 0, 16_000, .002
				i, err := strings.New(sampleRate, p)
				if err != nil {
					return nil, err
				}
				return keyedBlock(i, note)
			},
		},

		// Authored graph instruments.
		{
			Name:        "graph_saw",
			Description: "Graph polyBLEP sawtooth (kernel/graph Saw, legacy path), pitch to Saw.",
			New:         graphVoice(sawProgram(graph.Saw)),
		},
		{
			Name:        "graph_square",
			Description: "Graph polyBLEP square (kernel/graph Square, legacy path), pitch to Square.",
			New:         graphVoice(sawProgram(graph.Square)),
		},
		{
			Name:        "graph_quality_saw",
			Description: "Graph quality path (kernel/graph/quality.go): table sawtooth at 2x with ADSR and halfband decimation.",
			New:         graphVoice(qualityProgram(graph.Saw)),
		},
		{
			Name:        "graph_quality_pulse",
			Description: "Graph quality path pulse, width 0.23, through ADSR and halfband decimation.",
			New:         graphVoice(qualityProgram(graph.Pulse)),
		},
		{
			Name:        "graph_filters",
			Description: "Graph filter nodes driven by a sine: SVF (quality path), Ladder, Diode, Lowpass, Highpass. Noise and CPU use a sawtooth through the ladder.",
			New:         graphVoice(ladderProgram()),
			AliasNA:     "this entry measures filter curves; oscillator aliasing is measured in the graph_saw and graph_quality entries",
			Responses:   graphFilterResponses(),
		},
	}
}

func sawProgram(op graph.Op) graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Pitch}
	p.Nodes[1] = graph.Node{Op: op, A: 0, B: 2}
	p.Nodes[2] = graph.Node{Op: graph.Constant, Value: .5}
	p.Len, p.Output = 3, 1
	return p
}

func qualityProgram(op graph.Op) graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Pitch}
	p.Nodes[1] = graph.Node{Op: graph.Gate}
	p.Nodes[2] = graph.Node{Op: graph.Constant, Value: 1}   // attack ms
	p.Nodes[3] = graph.Node{Op: graph.Constant, Value: 1}   // decay ms
	p.Nodes[4] = graph.Node{Op: graph.Constant, Value: 1}   // sustain
	p.Nodes[5] = graph.Node{Op: graph.Constant, Value: 50}  // release ms
	p.Nodes[6] = graph.Node{Op: graph.Constant, Value: .23} // pulse width
	p.Nodes[7] = graph.Node{Op: op, A: 0, B: 6}
	p.Nodes[8] = graph.Node{Op: graph.ADSR, A: 1, B: 2, C: 3, D: 4, E: 5}
	p.Nodes[9] = graph.Node{Op: graph.Multiply, A: 7, B: 8}
	p.Len, p.Output = 10, 9
	return p
}

func ladderProgram() graph.Program {
	var p graph.Program
	p.Nodes[0] = graph.Node{Op: graph.Pitch}
	p.Nodes[1] = graph.Node{Op: graph.Saw, A: 0}
	p.Nodes[2] = graph.Node{Op: graph.Constant, Value: 2000}
	p.Nodes[3] = graph.Node{Op: graph.Constant, Value: .3}
	p.Nodes[4] = graph.Node{Op: graph.Ladder, A: 1, B: 2, C: 3}
	p.Len, p.Output = 5, 4
	return p
}

func graphVoice(p graph.Program) func(int) (block, error) {
	return func(note int) (block, error) {
		v, err := graph.NewVoice(p, sampleRate)
		if err != nil {
			return nil, err
		}
		if note >= 0 {
			v.NoteOn(uint8(note), velocity, false)
		}
		return monoBlock(v.Next), nil
	}
}
