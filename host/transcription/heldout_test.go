package transcription_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"testing"

	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/host/transcription"
)

type referenceNote struct {
	start, end, midi float64
}

type melodyFixture struct {
	name                 string
	seed                 uint64
	bpm, lowest, vibrato float64
	legato, breathy      bool
	harmonics            []float64
}

// These random seeds and timbres are independent of the pitch tracker's unit
// fixtures. Notes cover several registers, tempos, articulations, and vibrato
// depths; reference times describe synthesis events rather than detector frames.
func TestHeldOutMonophonicNotes(t *testing.T) {
	var total noteMetrics
	var groups [2]noteMetrics
	fixtures := heldOutMelodies()
	for i, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			pcm, reference := synthesizeMelody(fixture, 22050)
			result, err := transcription.Analyze(pcm, 22050, transcription.DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			metrics := measureNotes(reference, result.Notes)
			t.Logf("%s", metrics.summary())
			total.add(metrics)
			groups[i/(len(fixtures)/2)].add(metrics)
		})
	}
	t.Logf("first batch: %s", groups[0].summary())
	t.Logf("independent batch: %s", groups[1].summary())
	t.Logf("aggregate: %s", total.summary())
	if total.within50*10 < total.reference*9 {
		t.Errorf("only %.1f%% of all %d reference notes within 50 cents; require 90%%", 100*ratio(total.within50, total.reference), total.reference)
	}
	if median(total.onsetErrors) > .030 {
		t.Errorf("median onset error %.1f ms exceeds 30 ms", median(total.onsetErrors)*1000)
	}
	if ratio(total.octaveErrors, total.reference) > .02 {
		t.Errorf("octave errors %.1f%% exceed 2%%", 100*ratio(total.octaveErrors, total.reference))
	}
	if independent := groups[1]; independent.within50*10 < independent.reference*9 || median(independent.onsetErrors) > .030 || ratio(independent.octaveErrors, independent.reference) > .02 {
		t.Errorf("independent held-out batch missed targets: %s", independent.summary())
	}
}

func heldOutMelodies() []melodyFixture {
	first := []melodyFixture{
		{"low-detached", 17093, 72, 43, 0, false, false, []float64{1, .4, .2, .1}},
		{"middle-detached", 23159, 96, 55, 20, false, false, []float64{1, .6, .3, .15, .08}},
		{"high-detached", 38113, 132, 67, 50, false, false, []float64{1, .25, .1}},
		{"low-legato", 49523, 104, 45, 20, true, false, []float64{1, .7, .4, .2}},
		{"middle-legato", 61357, 84, 56, 50, true, false, []float64{1, .5, .33, .25, .2}},
		{"high-legato", 72901, 116, 65, 0, true, false, []float64{.35, 1, .7, .35}},
		{"breathy-detached", 84631, 92, 52, 50, false, true, []float64{1, .7, .28, .18}},
		{"breathy-legato", 97213, 124, 59, 20, true, true, []float64{1, .3, .2, .1}},
	}
	fixtures := append([]melodyFixture(nil), first...)
	for _, fixture := range first {
		fixture.name = "independent-" + fixture.name
		fixture.seed ^= 0xd37baad49271f6c5
		fixtures = append(fixtures, fixture)
	}
	return fixtures
}

func TestHeldOutScorePitchRoundTrip(t *testing.T) {
	var total noteMetrics
	var groups [2]noteMetrics
	fixtures := heldOutMelodies()
	for i, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			pcm, reference := synthesizeMelody(fixture, 22050)
			options := transcription.DefaultOptions()
			options.Tempo = fixture.bpm
			result, err := transcription.Analyze(pcm, 22050, options)
			if err != nil {
				t.Fatal(err)
			}
			result, err = transcription.Complete(result, options)
			if err != nil {
				t.Fatal(err)
			}
			audio, err := audition.Render([]byte(result.Source), 44100, 0, .1)
			if err != nil {
				t.Fatal(err)
			}
			monophonic := make([]float32, len(audio.Left))
			for i := range monophonic {
				monophonic[i] = (audio.Left[i] + audio.Right[i]) / 2
			}
			played, err := transcription.Analyze(monophonic, 44100, options)
			if err != nil {
				t.Fatal(err)
			}
			metrics := measureNotes(reference, played.Notes)
			t.Logf("emitted score: %s; quantization boundary movement=%.1fms", metrics.summary(), result.QuantizationMS)
			total.add(metrics)
			groups[i/(len(fixtures)/2)].add(metrics)
		})
	}
	t.Logf("emitted score first batch: %s", groups[0].summary())
	t.Logf("emitted score independent batch: %s", groups[1].summary())
	t.Logf("emitted score aggregate: %s", total.summary())
	if total.within50*10 < total.reference*9 {
		t.Errorf("emitted score preserves %.1f%% of all %d reference notes within 50 cents; require 90%%", 100*ratio(total.within50, total.reference), total.reference)
	}
	if independent := groups[1]; independent.within50*10 < independent.reference*9 {
		t.Errorf("emitted score independent batch missed targets: %s", independent.summary())
	}
}

func synthesizeMelody(f melodyFixture, rate int) ([]float32, []referenceNote) {
	rng := rand.New(rand.NewPCG(f.seed, f.seed^0x9e3779b97f4a7c15))
	const count = 12
	reference := make([]referenceNote, count)
	start := .19
	previous := -1
	for i := range reference {
		degree := rng.IntN(12)
		for degree == previous {
			degree = rng.IntN(12)
		}
		previous = degree
		duration := (60 / f.bpm) * (.5 + .25*float64(rng.IntN(3)))
		end := start + duration
		if !f.legato {
			end -= .055
		}
		reference[i] = referenceNote{start, end, f.lowest + float64(degree) + (rng.Float64()-.5)*.24}
		start += duration
	}
	pcm := make([]float32, int((start+.25)*float64(rate)))
	phase := rng.Float64() * 2 * math.Pi
	for j, note := range reference {
		amplitude := .18 + rng.Float64()*.1
		attack := .006 + rng.Float64()*.012
		release := .008 + rng.Float64()*.016
		vibratoPhase := rng.Float64() * 2 * math.Pi
		for i := int(note.start * float64(rate)); i < int(note.end*float64(rate)); i++ {
			tm := float64(i)/float64(rate) - note.start
			pitch := note.midi + f.vibrato/100*math.Sin(2*math.Pi*5.3*tm+vibratoPhase)
			if f.legato && j > 0 && tm < .018 {
				pitch += (reference[j-1].midi - note.midi) * (1 - tm/.018)
			}
			phase += 2 * math.Pi * 440 * math.Exp2((pitch-69)/12) / float64(rate)
			envelope := 1.0
			if !f.legato || j == 0 {
				envelope = math.Min(envelope, tm/attack)
			}
			if !f.legato || j == len(reference)-1 {
				envelope = math.Min(envelope, (note.end-float64(i)/float64(rate))/release)
			}
			var value, normalization float64
			for h, gain := range f.harmonics {
				value += gain * math.Sin(float64(h+1)*phase+.11*float64(h))
				normalization += gain
			}
			value /= normalization
			if f.breathy {
				value += .055 * (rng.Float64()*2 - 1)
			}
			pcm[i] = float32(value * amplitude * math.Max(0, envelope))
		}
	}
	return pcm, reference
}

func TestHeldOutNoiseDoesNotProduceNotes(t *testing.T) {
	const rate = 22050
	rng := rand.New(rand.NewPCG(104729, 130363))
	pcm := make([]float32, rate*2)
	for i := range pcm {
		pcm[i] = float32((rng.Float64()*2 - 1) * .04)
	}
	result, err := transcription.Analyze(pcm, rate, transcription.DefaultOptions())
	if err != nil && !errors.Is(err, transcription.ErrNoNotes) {
		t.Fatal(err)
	}
	var voiced float64
	for _, note := range result.Notes {
		voiced += note.End - note.Start
	}
	t.Logf("noise false voiced positives: notes=%d duration=%.3fs", len(result.Notes), voiced)
	if len(result.Notes) != 0 {
		t.Errorf("unpitched noise produced %d notes (%.3fs)", len(result.Notes), voiced)
	}
}

func TestKernelGraphRecordingTranscription(t *testing.T) {
	const rate = 44100
	const source = `tempo 120
key c major
instrument melody {
  voice mono {
    let tone = sine(pitch) * 0.7 + sine(pitch * 2) * 0.2 + sine(pitch * 3) * 0.1
    let amp = adsr(gate, 4ms, 40ms, 0.8, 12ms)
    out = tone * amp * velocity * 0.3
  }
}
track tune melody {}
pattern phrase {
  c4 - d4 - e4 - g4 -
  a4 - g4 - e4 - d4 -
}
scene main { tune = phrase }
song { main }
`
	audio, err := audition.Render([]byte(source), rate, 1, .1)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, len(audio.Left))
	for i := range pcm {
		pcm[i] = (audio.Left[i] + audio.Right[i]) / 2
	}
	result, err := transcription.Analyze(pcm, rate, transcription.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	pitches := []float64{60, 62, 64, 67, 69, 67, 64, 62}
	reference := make([]referenceNote, len(pitches))
	for i, pitch := range pitches {
		reference[i] = referenceNote{float64(i) * .25, float64(i+1) * .25, pitch}
	}
	metrics := measureNotes(reference, result.Notes)
	t.Logf("kernel graph recording: %s", metrics.summary())
	if metrics.within50 != len(reference) || metrics.predicted != len(reference) || median(metrics.onsetErrors) > .030 {
		t.Errorf("kernel graph recording missed transcription targets: %s", metrics.summary())
	}
}

type noteMetrics struct {
	reference, predicted, matched, within50, octaveErrors int
	onsetErrors                                           []float64
}

// Monotone alignment uses time only so pitch mistakes remain visible. A missed
// reference or an extra prediction costs one; matches must overlap in time and
// begin within 160 ms. Precision and recall expose over/under-segmentation.
func measureNotes(reference []referenceNote, predicted []transcription.Note) noteMetrics {
	n, m := len(reference), len(predicted)
	dp := make([]float64, (n+1)*(m+1))
	step := make([]byte, len(dp))
	index := func(i, j int) int { return i*(m+1) + j }
	for i := 1; i <= n; i++ {
		dp[index(i, 0)], step[index(i, 0)] = float64(i), 'r'
	}
	for j := 1; j <= m; j++ {
		dp[index(0, j)], step[index(0, j)] = float64(j), 'p'
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			k := index(i, j)
			dp[k], step[k] = dp[index(i-1, j)]+1, 'r'
			if cost := dp[index(i, j-1)] + 1; cost < dp[k] {
				dp[k], step[k] = cost, 'p'
			}
			r, p := reference[i-1], predicted[j-1]
			delta := math.Abs(r.start - p.Start)
			overlap := math.Max(0, math.Min(r.end, p.End)-math.Max(r.start, p.Start))
			union := math.Max(r.end, p.End) - math.Min(r.start, p.Start)
			if delta <= .160 && overlap > 0 && union > 0 {
				cost := dp[index(i-1, j-1)] + delta/.160 + .25*(1-overlap/union)
				if cost < dp[k] {
					dp[k], step[k] = cost, 'm'
				}
			}
		}
	}
	metrics := noteMetrics{reference: n, predicted: m}
	for i, j := n, m; i > 0 || j > 0; {
		switch step[index(i, j)] {
		case 'm':
			r, p := reference[i-1], predicted[j-1]
			metrics.matched++
			metrics.onsetErrors = append(metrics.onsetErrors, math.Abs(r.start-p.Start))
			cents := 1200 * math.Log2(p.PitchHz/(440*math.Exp2((r.midi-69)/12)))
			if math.Abs(cents) <= 50 {
				metrics.within50++
			}
			if math.Abs(cents) >= 1150 && math.Abs(cents) <= 1250 {
				metrics.octaveErrors++
			}
			i--
			j--
		case 'r':
			i--
		case 'p':
			j--
		default:
			panic("invalid note alignment")
		}
	}
	return metrics
}

func (m *noteMetrics) add(other noteMetrics) {
	m.reference += other.reference
	m.predicted += other.predicted
	m.matched += other.matched
	m.within50 += other.within50
	m.octaveErrors += other.octaveErrors
	m.onsetErrors = append(m.onsetErrors, other.onsetErrors...)
}

func ratio(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

func percentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return math.Inf(1)
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	return ordered[int(math.Round(fraction*float64(len(ordered)-1)))]
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return math.Inf(1)
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 0 {
		return (ordered[middle-1] + ordered[middle]) / 2
	}
	return ordered[middle]
}

func (m noteMetrics) summary() string {
	return fmt.Sprintf("reference=%d predicted=%d matched=%d recall=%.1f%% precision=%.1f%% within50_all=%.1f%% within50_matched=%.1f%% onset_median=%.1fms onset_p95=%.1fms octave=%.1f%%", m.reference, m.predicted, m.matched, 100*ratio(m.matched, m.reference), 100*ratio(m.matched, m.predicted), 100*ratio(m.within50, m.reference), 100*ratio(m.within50, m.matched), median(m.onsetErrors)*1000, percentile(m.onsetErrors, .95)*1000, 100*ratio(m.octaveErrors, m.reference))
}

func TestHeldOutAlignmentCountsMissesExtrasAndOctaves(t *testing.T) {
	reference := []referenceNote{{0, .3, 60}, {.4, .7, 64}, {.8, 1.1, 67}}
	predicted := []transcription.Note{
		{Start: .010, End: .3, PitchHz: 440 * math.Exp2((60-69.0)/12)},
		{Start: .805, End: 1.1, PitchHz: 440 * math.Exp2((79-69.0)/12)},
		{Start: 1.3, End: 1.6, PitchHz: 440},
	}
	metrics := measureNotes(reference, predicted)
	if metrics.reference != 3 || metrics.predicted != 3 || metrics.matched != 2 || metrics.within50 != 1 || metrics.octaveErrors != 1 {
		t.Fatalf("alignment concealed a missed note, extra note, or octave error: %+v", metrics)
	}
}
