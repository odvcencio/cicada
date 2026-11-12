package acid

import (
	"os"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// acidOutputDigest pins the acid voice's float32 output, and acidStateDigest
// that output together with its float64 envelopes, accent, smoothed controls
// and filter state, over a bass line played for three seconds at each sample
// rate. Accents and slides move the envelope modulation, the accent sweep and
// its capacitor; every bar the cutoff, resonance, envelope amount, decay and
// accent glide to new targets, the filter switches between diode and ladder,
// and the savage stage and vibrato come and go. Before the voice's math moved
// to kernel/dsp/detmath (#328, #329, #343) its output differed between amd64
// and arm64 within the first second; these digests are the same on amd64,
// arm64 and TinyGo's WebAssembly (make test-tinygo-digests). Print new values
// with CICADA_ACID_DIGEST_PRINT=1 and change them only with a stated reason.
const (
	acidOutputDigest = "a73a4d52c4579f764107931ddd6f3e4b337f4f7ac4bbf4cc937f70074bdbea04"
	acidStateDigest  = "e19205e68457fcf479e0202729f385985ee150a5ce69f4b3a93e4c573e5231f2"
)

func TestAcidDigest(t *testing.T) {
	output, state := bitdigest.New(), bitdigest.New()
	for _, rate := range []int{44_100, 48_000, 96_000} {
		renderAcidPattern(t, rate, output, state)
	}
	gotOutput, gotState := output.Hex(), state.Hex()
	if os.Getenv("CICADA_ACID_DIGEST_PRINT") == "1" {
		t.Logf("acidOutputDigest = %q", gotOutput)
		t.Logf("acidStateDigest  = %q", gotState)
		return
	}
	if gotOutput != acidOutputDigest {
		t.Errorf("acid output digest = %s, want %s", gotOutput, acidOutputDigest)
	}
	if gotState != acidStateDigest {
		t.Errorf("acid state digest = %s, want %s", gotState, acidStateDigest)
	}
}

// renderAcidPattern plays 16th notes at 130 BPM for three seconds.
func renderAcidPattern(t *testing.T, rate int, output, state *bitdigest.Digest) {
	t.Helper()
	v, err := New(rate)
	if err != nil {
		t.Fatal(err)
	}
	// Each bar starts from one of these settings and glides to the next.
	bars := []Params{
		{Cutoff: 380, Resonance: .72, EnvMod: .85, Decay: .25, Accent: .9, Drive: .35, Release: .03, Slide: .06, Gate: 60, LevelDB: -6, PulseWidth: .5, Filter: Diode},
		{Cutoff: 1400, Resonance: .4, EnvMod: .5, Decay: 1.2, Accent: .6, Drive: .7, Release: .05, Slide: .1, Gate: 70, LevelDB: -4, PulseWidth: .3, Wave: 1, Sub: .4, Filter: Ladder},
		{Cutoff: 220, Resonance: .95, EnvMod: 1, Decay: .12, Accent: 1, Drive: .15, Release: .02, Slide: .04, Gate: 45, LevelDB: -8, PulseWidth: .5, Detune: 12, Wave: .5, Filter: Diode, Savage: true},
		{Cutoff: 3000, Resonance: .2, EnvMod: .3, Decay: 2.5, Accent: .3, Drive: 1, Release: .2, Slide: .15, Gate: 90, LevelDB: -10, PulseWidth: .7, Sub: 1, Filter: Ladder, Savage: true},
	}
	notes := [16]int{36, 36, 48, -1, 39, 36, 43, 48, 36, 41, -1, 48, 46, 36, 43, 39}
	accent := [16]bool{true, false, false, false, true, false, false, true, false, false, false, true, false, true, false, false}
	slide := [16]bool{false, false, true, false, false, true, false, false, false, true, false, false, true, false, false, true}
	step := rate * 60 / 130 / 4
	total := 3 * rate
	alpha := 1 / (0.05 * float64(rate)) // the controls glide over about 50 ms
	block := make([]float32, 32)
	for frame, n := 0, 0; frame < total; n++ {
		bar, s := n/16, n%16
		if s == 0 {
			if err := v.SetParams(bars[bar%len(bars)]); err != nil {
				t.Fatal(err)
			}
			if err := v.SetParamsTarget(bars[(bar+1)%len(bars)], alpha); err != nil {
				t.Fatal(err)
			}
		}
		if notes[s] < 0 {
			v.NoteOff()
		} else {
			v.NoteOn(uint8(notes[s]), accent[s], slide[s], 100)
			if bar%2 == 1 { // NoteOn resets expression; vibrato on every other bar
				v.SetExpression(expression.Params{VibratoRateHz: 5.5, VibratoDepthCents: 30})
			}
		}
		release := step * 6 / 10
		for i := 0; i < step && frame < total; i += len(block) {
			if i >= release && !slide[(s+1)%16] {
				v.NoteOff()
			}
			v.Render(block)
			output.Float32(block...)
			state.Float32(block...)
			state.Float64(v.meg, v.vca, v.cap, v.sweepStrength, v.accentGain, v.pitchLog,
				v.params.Cutoff, v.params.Resonance, v.params.EnvMod, v.params.Decay, v.params.Accent,
				v.filterBlend, v.savageBlend, v.restingFilterG,
				v.diode.state[0], v.diode.state[1], v.diode.state[2], v.diode.state[3],
				v.ladder.state[0], v.ladder.state[1], v.ladder.state[2], v.ladder.state[3])
			frame += len(block)
		}
	}
	if v.Fault() {
		t.Fatalf("acid voice faulted at %d Hz", rate)
	}
}
