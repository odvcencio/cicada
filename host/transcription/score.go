package transcription

import (
	"fmt"
	"math"
	"strings"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const maxScoreSteps = 16 * 64

// Complete infers musical context and returns formatted, compiled score rows.
// Notes retain measured seconds and pitch; QuantizationMS is mean absolute
// boundary movement in the score, rather than pitch-tracker onset error.
func Complete(result Result, options Options) (Result, error) {
	if err := validateOptions(options); err != nil {
		return Result{}, err
	}
	if len(result.Notes) == 0 {
		return Result{}, fmt.Errorf("no pitched notes to write")
	}
	for i, n := range result.Notes {
		if !finite(n.Start) || !finite(n.End) || !finite(n.MIDI) || !finite(n.Cents) || !finite(n.Confidence) || n.Start < 0 || n.End <= n.Start || n.MIDI < 32.5 || n.MIDI > 96.5 || n.Confidence < 0 || n.Confidence > 1 || i > 0 && n.Start < result.Notes[i-1].End-.025 {
			return Result{}, fmt.Errorf("invalid or overlapping note %d", i+1)
		}
		for j, p := range n.Pitch {
			if !finite(p.Time) || !finite(p.Cents) || math.Abs(p.Cents) > 9600 || j > 0 && p.Time <= n.Pitch[j-1].Time {
				return Result{}, fmt.Errorf("invalid pitch anchor in note %d", i+1)
			}
		}
	}
	result.Warnings = append([]string(nil), result.Warnings...)
	if options.Tempo != 0 {
		result.Tempo, result.TempoConfidence = options.Tempo, 1
	} else {
		result.Tempo, result.TempoConfidence = inferTempo(result.Notes)
		if result.TempoConfidence < .5 {
			result.Warnings = append(result.Warnings, "Tempo is ambiguous; set a tempo to choose the beat or half/double time.")
		}
	}
	result.Tempo = math.Round(result.Tempo*1000) / 1000
	if options.Key != "auto" && options.Key != "" {
		root, mode, _ := parseKey(options.Key)
		result.Key, result.KeyConfidence = pitchClasses[root]+" "+[]string{"major", "minor"}[mode], 1
	} else {
		result.Key, result.KeyConfidence = inferKey(result.Notes)
		if result.KeyConfidence < .5 {
			result.Warnings = append(result.Warnings, "Key is uncertain; chromatic notes are preserved and the key can be overridden.")
		}
	}
	result.Meter, result.MeterConfidence = inferMeter(result.Notes, result.Tempo)
	if result.MeterConfidence < .5 {
		result.Warnings = append(result.Warnings, "Meter is uncertain; review the beat grouping.")
	}
	if result.Meter != "4/4" {
		result.Warnings = append(result.Warnings, "The inferred meter is "+result.Meter+"; score rows use 4/4 bars while keeping the note sequence.")
	}
	source, drift, err := scoreRows(result, options)
	if err != nil {
		return Result{}, err
	}
	result.Source, result.QuantizationMS = source, drift
	if drift > 30 {
		result.Warnings = append(result.Warnings, "Quantization moved note boundaries by more than 30 ms on average; review the tempo and grid.")
	}
	return result, nil
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func scoreRows(r Result, o Options) (string, float64, error) {
	stepSeconds := 60 / r.Tempo / 4
	stride := 16 / o.Grid
	type span struct{ start, end int }
	spans := make([]span, len(r.Notes))
	drift := 0.0
	lastEnd := 0
	for i, note := range r.Notes {
		start := int(math.Round(note.Start/stepSeconds/float64(stride))) * stride
		end := int(math.Round(note.End/stepSeconds/float64(stride))) * stride
		if start < lastEnd {
			spans[i-1].end = start
			if spans[i-1].end <= spans[i-1].start {
				return "", 0, fmt.Errorf("notes %d and %d collapse on the selected grid; choose a finer grid or faster tempo", i, i+1)
			}
		}
		end = max(end, start+stride)
		if end > maxScoreSteps {
			return "", 0, fmt.Errorf("transcription exceeds 64 score bars; use a shorter recording or slower tempo")
		}
		spans[i], lastEnd = span{start, end}, end
	}
	steps := (lastEnd + 15) / 16 * 16
	rows, bends := make([]string, steps), make([]float64, steps)
	for i := range rows {
		rows[i] = "."
	}
	root, mode, _ := parseKey(r.Key)
	crossed := false
	for i, n := range r.Notes {
		s := spans[i]
		drift += math.Abs(float64(s.start)*stepSeconds-n.Start) + math.Abs(float64(s.end)*stepSeconds-n.End)
		for step := s.start; step < s.end; step++ {
			rows[step] = "-"
			if step == s.start || step%64 == 0 {
				rows[step] = degreePitch(int(math.Round(n.MIDI)), root, mode)
				crossed = crossed || step != s.start
			}
			bends[step] = pitchAt(n, n.Start+float64(step-s.start)*stepSeconds)
		}
	}
	var out strings.Builder
	fmt.Fprintf(&out, "cicada 2\ntitle \"Transcribed melody\"\ntempo %.3f\nkey %s\n", r.Tempo, r.Key)
	out.WriteString("instrument melody_voice {\n octave = 4\n voice mono {\n out = sine(pitch) * adsr(gate, 4ms, 30ms, 0.8, 12ms) * velocity\n }\n}\ntrack melody melody_voice { level = -6dB }\n")
	if crossed {
		out.WriteString("// Notes continuing across four-bar patterns are retriggered.\n")
	}
	chunks := (steps + 63) / 64
	for chunk := range chunks {
		begin, end := chunk*64, min((chunk+1)*64, steps)
		fmt.Fprintf(&out, "pattern melody_%d {\n gate = 100%%\n", chunk+1)
		for row := begin; row < end; row += 16 {
			if row != begin {
				out.WriteString(" |\n")
			}
			out.WriteString(" " + strings.Join(rows[row:row+16], " ") + "\n")
		}
		out.WriteString(" bend:")
		for _, value := range bends[begin:end] {
			fmt.Fprintf(&out, " %.2fct", value)
		}
		out.WriteString("\n}\n")
	}
	out.WriteString("arrange {\n")
	for chunk := range chunks {
		begin, end := chunk*64, min((chunk+1)*64, lastEnd)
		fmt.Fprintf(&out, " place part_%d melody melody_%d { at = %dticks length = %dticks }\n", chunk+1, chunk+1, begin*240, (end-begin)*240)
	}
	out.WriteString("}\n")
	source := []byte(out.String())
	score, ds := notation.Parse(source)
	if err := diagnosticError(ds); err != nil {
		return "", 0, err
	}
	p, ds := project.FromScore(score)
	if err := diagnosticError(ds); err != nil {
		return "", 0, err
	}
	if _, err := project.CompileEngine(p, 48000, 128); err != nil {
		return "", 0, fmt.Errorf("generated score cannot play: %w", err)
	}
	doc, err := notation.ParseDocument(source)
	if err != nil {
		return "", 0, err
	}
	formatted, err := notation.Format(doc)
	return string(formatted), drift / float64(2*len(r.Notes)) * 1000, err
}

func diagnosticError(ds []notation.Diagnostic) error {
	for _, d := range ds {
		if d.Severity == "error" {
			return fmt.Errorf("generated score: %s: %s", d.Code, d.Message)
		}
	}
	return nil
}

func degreePitch(midi, root, mode int) string {
	intervals := [2][7]int{{0, 2, 4, 5, 7, 9, 11}, {0, 2, 3, 5, 7, 8, 10}}
	bestDegree, bestOctave, bestAccidental, bestCost := 0, 0, 0, 100
	for octave := -4; octave <= 4; octave++ {
		for degree, interval := range intervals[mode] {
			accidental := midi - (60 + root + interval + 12*octave)
			if cost := absInt(accidental); cost < bestCost {
				bestDegree, bestOctave, bestAccidental, bestCost = degree, octave, accidental, cost
			}
		}
	}
	pitch := fmt.Sprint(bestDegree + 1)
	if bestAccidental < 0 {
		pitch += "b"
	} else if bestAccidental > 0 {
		pitch += "#"
	}
	if bestOctave > 0 {
		pitch += strings.Repeat("'", bestOctave)
	} else {
		pitch += strings.Repeat(",", -bestOctave)
	}
	return pitch
}

func absInt(x int) int { return max(x, -x) }

func pitchAt(n Note, time float64) float64 {
	if len(n.Pitch) == 0 {
		return n.Cents
	}
	if time <= n.Pitch[0].Time {
		return n.Pitch[0].Cents
	}
	for i := 1; i < len(n.Pitch); i++ {
		p, previous := n.Pitch[i], n.Pitch[i-1]
		if time <= p.Time {
			fraction := (time - previous.Time) / (p.Time - previous.Time)
			return previous.Cents + fraction*(p.Cents-previous.Cents)
		}
	}
	return n.Pitch[len(n.Pitch)-1].Cents
}
