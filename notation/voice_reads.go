package notation

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/modal"
)

// The musical inputs a pattern can carry beyond its pitches and rhythm, by
// the names refusals use. PatternSyntax classifies every piece of pattern
// syntax the grammar has as one of these or as structure, and a test keeps
// that classification complete.
const (
	InputAccent            = "accent"             // a step's ^
	InputSlide             = "slide"              // a step's ~
	InputRatchet           = "ratchet"            // a step's or hit's *N
	InputChance            = "chance"             // a step's or hit's ?N
	InputBrackets          = "brackets"           // [a2 c3 e3]: a tuplet on acid, a chord elsewhere
	InputVelocityRow       = "velocity row"       // velocity:
	InputPressureRow       = "pressure row"       // pressure:
	InputTimbreRow         = "timbre row"         // timbre:
	InputBendRow           = "bend row"           // bend:
	InputVibratoRow        = "vibrato row"        // vibrato:
	InputSwing             = "swing"              // swing =
	InputGate              = "gate"               // gate =
	InputTranspose         = "transpose"          // transpose =
	InputHumanize          = "humanize"           // humanize =
	InputPerformedVelocity = "performed velocity" // a performed note's velocity =
	InputGestureBend       = "gesture bend"       // a gesture's bend
	InputGesturePressure   = "gesture pressure"   // a gesture's pressure
	InputGestureTimbre     = "gesture timbre"     // a gesture's timbre
	InputGestureVibrato    = "gesture vibrato"    // a gesture's vibrato and rate
	InputHitAccent         = "accented hit"       // a drum hit X
	InputHitVelocity       = "hit velocity"       // a drum hit x1 to x9
)

// MelodicInputs, PerformedInputs and DrumInputs list the inputs of each kind
// of pattern.
var (
	MelodicInputs   = []string{InputAccent, InputSlide, InputRatchet, InputChance, InputBrackets, InputVelocityRow, InputPressureRow, InputTimbreRow, InputBendRow, InputVibratoRow, InputSwing, InputGate, InputTranspose, InputHumanize}
	PerformedInputs = []string{InputPerformedVelocity, InputGestureBend, InputGesturePressure, InputGestureTimbre, InputGestureVibrato}
	DrumInputs      = []string{InputHitAccent, InputHitVelocity, InputRatchet, InputChance, InputSwing, InputHumanize}
)

// What a voice does with an input.
const (
	EffectPlays   = "plays"
	EffectIgnores = "ignores"
	EffectRefuses = "refuses"
	EffectTuplet  = "plays as a tuplet"
	EffectChord   = "plays as a chord"
)

// PatternSyntax classifies every rule, keyword, punctuation and field the
// grammar allows inside a pattern (language/grammar/grammar.go): as the
// input it carries, or as structure, which every voice plays the same way
// (pitch, rhythm, names, layout). TestPatternSyntaxIsClassified fails when
// the grammar gains a piece this map does not name.
var PatternSyntax = map[string]string{
	// rules
	"acid_pattern": "structure", "note_pattern": "structure", "drum_pattern": "structure", "performance_pattern": "structure",
	"pattern_attr": "structure", "acid_step": "structure", "acid_note": "structure", "pitch": "structure", "degree": "structure",
	"letter_pitch": "structure", "octave_shift": "structure", "modifier": "structure", "ratchet": InputRatchet, "probability": InputChance,
	"chord_note": InputBrackets, "chord_pitch": InputBrackets, "phrase_use": "structure", "velocity_row": InputVelocityRow,
	"expression_row": "structure", "expression_row_label": "structure", "expression_cell": "structure", "expression_number": "structure",
	"drum_lane": "structure", "drum_lane_label": "structure", "drum_hit": "structure", "hit": "structure",
	"accent_hit": InputHitAccent, "velocity_hit": InputHitVelocity, "performed_note": "structure", "performed_group": "structure",
	"performed_expression": "structure", "_name": "structure", "identifier": "structure", "qualified_name": "structure",
	"number": "structure", "integer": "structure", "fraction": "structure", "tick_count": "structure", "comment": "structure",
	// keywords and punctuation
	"pattern": "structure", "acid": "structure", "notes": "structure", "drums": "structure", "performance": "structure",
	"{": "structure", "}": "structure", "=": "structure", ":": "structure", ";": "structure", ".": "structure", "-": "structure",
	"|": "structure", "[": InputBrackets, "]": InputBrackets, "'": "structure", ",": "structure", "^": InputAccent, "~": InputSlide,
	"*": InputRatchet, "?": InputChance, "%": InputChance, "use": "structure", "transpose": InputTranspose, "+": InputTranspose,
	"velocity": InputPerformedVelocity, "gesture-group": "structure", "gesture": "structure", "note": "structure", "at": "structure",
	"length": "structure", "group": "structure", "bend": InputGestureBend, "pressure": InputGesturePressure, "timbre": InputGestureTimbre,
	"vibrato": InputGestureVibrato, "rate": InputGestureVibrato,
	// fields
	"name": "structure", "value": "structure", "repeat": "structure",
	// the expression rows the label allows
	"row bend": InputBendRow, "row vibrato": InputVibratoRow, "row pressure": InputPressureRow, "row timbre": InputTimbreRow,
}

// patternAttrInputs classifies a pattern's settings.
var patternAttrInputs = map[string]string{
	"swing": InputSwing, "gate": InputGate, "transpose": InputTranspose, "humanize": InputHumanize,
	"length": "structure", "step": "structure", "steps": "structure", "slot": "structure", "seed": "structure",
}

// UnmodeledError names a piece of a pattern the voice-change check does not
// know how voices treat; the change is refused rather than let through.
type UnmodeledError struct{ Pattern, What string }

func (e *UnmodeledError) Error() string {
	return fmt.Sprintf("pattern %s carries %s, which the voice check does not model", e.Pattern, e.What)
}

// PatternInputs lists the inputs a pattern carries, in the order of the
// pattern kind's list. A piece it cannot classify comes back as an
// UnmodeledError.
func PatternInputs(p Pattern) ([]string, error) {
	carried := map[string]bool{}
	steps := func(tokens []StepToken, drums bool) error {
		for _, token := range tokens {
			text := token.Text
			for i := 0; i < len(text); i++ {
				c := text[i]
				switch {
				case c == '^':
					carried[InputAccent] = true
				case c == '~':
					carried[InputSlide] = true
				case c == '*':
					carried[InputRatchet] = true
				case c == '?' || c == '%':
					carried[InputChance] = true
				case c == '[' || c == ']':
					carried[InputBrackets] = true
				case drums && c == 'X':
					carried[InputHitAccent] = true
				case drums && c == 'x':
					if i+1 < len(text) && text[i+1] >= '1' && text[i+1] <= '9' {
						carried[InputHitVelocity] = true
						i++
					}
				case c >= '0' && c <= '9', !drums && c >= 'a' && c <= 'g', c == '#', c == 'b', c == '\'', c == ',', c == '.', c == '-', c == '|', c == ' ', c == '+':
				default:
					return &UnmodeledError{Pattern: p.Name, What: fmt.Sprintf("the step mark %q", string(c))}
				}
			}
		}
		return nil
	}
	if err := steps(p.Steps, false); err != nil {
		return nil, err
	}
	for _, lane := range p.Lanes {
		if err := steps(lane.Hits, true); err != nil {
			return nil, err
		}
	}
	if len(p.Velocity) > 0 {
		carried[InputVelocityRow] = true
	}
	for _, row := range p.Expression {
		input, ok := PatternSyntax["row "+row.Name]
		if !ok {
			return nil, &UnmodeledError{Pattern: p.Name, What: "the row " + row.Name}
		}
		carried[input] = true
	}
	for _, attr := range p.Attrs {
		input, ok := patternAttrInputs[attr.Name]
		if !ok {
			return nil, &UnmodeledError{Pattern: p.Name, What: "the setting " + attr.Name}
		}
		if input != "structure" {
			carried[input] = true
		}
	}
	if performance := p.Performance; performance != nil {
		for _, note := range performance.Notes {
			carried[InputPerformedVelocity] = true
			for _, point := range note.Expressions {
				gesture(carried, point.Params.PitchCents, point.Params.VibratoDepthCents)
			}
		}
		for _, group := range performance.Groups {
			for _, point := range group.Points {
				gesture(carried, point.Value.PitchCents, 0)
			}
		}
	}
	order := MelodicInputs
	switch {
	case p.Kind == "drums":
		order = DrumInputs
	case p.Performance != nil:
		order = PerformedInputs
	}
	var inputs []string
	for _, input := range order {
		if carried[input] {
			inputs = append(inputs, input)
			delete(carried, input)
		}
	}
	for input := range carried {
		return nil, &UnmodeledError{Pattern: p.Name, What: input + " in a " + patternKindName(p) + " pattern"}
	}
	return inputs, nil
}

// gesture records a gesture point's inputs: pressure and timbre are always
// written, bend and vibrato when they are not zero.
func gesture(carried map[string]bool, bend, vibrato float32) {
	carried[InputGesturePressure], carried[InputGestureTimbre] = true, true
	if bend != 0 {
		carried[InputGestureBend] = true
	}
	if vibrato != 0 {
		carried[InputGestureVibrato] = true
	}
}

func patternKindName(p Pattern) string {
	switch {
	case p.Kind == "drums":
		return "drum"
	case p.Performance != nil:
		return "performance"
	}
	return "melodic"
}

// VoiceInputs describes what a voice does with each input a pattern can
// carry: EffectPlays, EffectIgnores (accepted, no effect), EffectRefuses
// (validation or the compiler refuses it), or how it plays brackets. An input
// it does not name is one the check does not model, and a change to that
// voice is refused. A voice reads an input when either renderer, the direct
// mixer or the scheduled engine, plays it; render/voice_reads_test.go renders
// every entry both ways. Nil means a voice the score does not declare.
func VoiceInputs(s *Score, kind string) map[string]string {
	p, i, r := EffectPlays, EffectIgnores, EffectRefuses
	with := func(base map[string]string, extra map[string]string) map[string]string {
		for k, v := range extra {
			base[k] = v
		}
		return base
	}
	kind = ResolveVoice(s, kind)
	if VoiceLanes(s, kind) != nil {
		return map[string]string{InputHitAccent: p, InputHitVelocity: p, InputRatchet: p, InputChance: p, InputSwing: p, InputHumanize: p}
	}
	common := func() map[string]string {
		return map[string]string{InputSlide: p, InputRatchet: p, InputChance: p, InputSwing: p, InputGate: p, InputTranspose: p}
	}
	gestures := func(effect string) map[string]string {
		return map[string]string{InputPerformedVelocity: effect, InputGestureBend: effect, InputGesturePressure: effect, InputGestureTimbre: effect, InputGestureVibrato: effect}
	}
	_, isModal := modal.ParseTrackKind(kind)
	switch {
	case kind == "acid":
		return with(common(), map[string]string{InputAccent: p, InputBrackets: EffectTuplet, InputVelocityRow: i, InputPressureRow: i, InputTimbreRow: i, InputBendRow: p, InputVibratoRow: p, InputHumanize: r,
			InputPerformedVelocity: p, InputGestureBend: p, InputGesturePressure: i, InputGestureTimbre: i, InputGestureVibrato: p})
	case kind == "piano" || keyboard.ID(kind) != 0 && !declaresInstrument(s, kind):
		return with(with(common(), gestures(r)), map[string]string{InputAccent: i, InputBrackets: EffectChord, InputVelocityRow: p, InputPressureRow: r, InputTimbreRow: r, InputBendRow: r, InputVibratoRow: r, InputHumanize: p})
	case isModal || kind == "guitar":
		gate := p
		if isModal {
			gate = i
		}
		return with(with(common(), gestures(r)), map[string]string{InputAccent: i, InputBrackets: r, InputVelocityRow: p, InputPressureRow: i, InputTimbreRow: i, InputBendRow: i, InputVibratoRow: i, InputHumanize: p, InputGate: gate})
	}
	for _, sampler := range s.Samplers {
		if sampler.Name == kind {
			return with(common(), map[string]string{InputAccent: i, InputBrackets: EffectChord, InputVelocityRow: p, InputPressureRow: p, InputTimbreRow: r, InputBendRow: p, InputVibratoRow: p, InputHumanize: p,
				InputPerformedVelocity: p, InputGestureBend: p, InputGesturePressure: p, InputGestureTimbre: r, InputGestureVibrato: p})
		}
	}
	for _, instrument := range s.Instruments {
		if instrument.Name != kind {
			continue
		}
		names := instrumentNames(instrument)
		mono := instrument.Mode != "poly"
		reads := func(read bool) string {
			if read {
				return p
			}
			return i
		}
		brackets := EffectChord
		if mono {
			brackets = r
		}
		return with(common(), map[string]string{InputAccent: i, InputBrackets: brackets,
			InputVelocityRow: reads(names["velocity"]), InputPressureRow: reads(mono && names["pressure"]), InputTimbreRow: reads(mono && names["timbre"]),
			InputBendRow: reads(mono), InputVibratoRow: reads(mono), InputHumanize: reads(names["velocity"]),
			InputPerformedVelocity: reads(names["velocity"]), InputGestureBend: p, InputGesturePressure: reads(names["pressure"]), InputGestureTimbre: reads(names["timbre"]), InputGestureVibrato: p})
	}
	return nil
}

func ResolveVoice(s *Score, kind string) string {
	for range 4 {
		resolved := kind
		if first, rest, ok := strings.Cut(kind, "."); ok {
			if namespace, found := s.LibraryAliases[first]; found {
				resolved = namespace + "." + rest
			}
		}
		for _, preset := range s.Presets {
			if (preset.Name == kind || preset.Name == resolved) && preset.Target != "" {
				resolved = preset.Target
			}
		}
		if resolved == kind {
			return kind
		}
		kind = resolved
	}
	return kind
}

// VoiceLanes are the drum lanes a voice plays: every built-in lane for drums,
// a kit's bound lanes (a library's kit included), and nil for a voice that is
// not a drum voice or that the score does not declare.
func VoiceLanes(s *Score, kind string) []string {
	kind = ResolveVoice(s, kind)
	if kind == "drums" {
		return append([]string(nil), drum.Names[:]...)
	}
	for _, kit := range s.Kits {
		if kit.Name == kind {
			lanes := make([]string, 0, len(kit.Bindings))
			for _, binding := range kit.Bindings {
				lanes = append(lanes, binding.Lane)
			}
			return lanes
		}
	}
	return nil
}

// PatternInputs lists the rows and marks a melodic pattern carries.
func declaresInstrument(s *Score, name string) bool {
	for _, instrument := range s.Instruments {
		if instrument.Name == name {
			return true
		}
	}
	return false
}

// instrumentNames lists the names an instrument's voice graph reads.
func instrumentNames(instrument Instrument) map[string]bool {
	names := map[string]bool{}
	var walk func(*Expr)
	walk = func(e *Expr) {
		if e == nil {
			return
		}
		if e.Kind == "name" {
			names[e.Text] = true
		}
		walk(e.Left)
		walk(e.Right)
		for _, arg := range e.Args {
			walk(arg)
		}
	}
	for _, let := range instrument.Lets {
		walk(let.Value)
	}
	walk(instrument.Output)
	return names
}
