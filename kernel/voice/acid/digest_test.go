package acid

import (
	"os"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/internal/bitdigest"
)

// acidOutputDigest pins the acid voice's float32 output, and acidStateDigest
// that output together with its float64 envelopes, accent, smoothed controls
// and filter state, over a bass line of four bars and a rest at each sample
// rate (renderAcidPattern). Accents and slides move the envelope modulation,
// the accent sweep and its capacitor; every bar the cutoff, resonance,
// envelope amount, decay and accent glide to new targets, the filter switches
// between diode and ladder, the savage stage, the detuned oscillator, full
// drive and vibrato come and go, and during the rest the envelopes reach their
// floors and the resting filter runs. Before the voice's math moved
// to kernel/dsp/detmath (#328, #329, #343) its output differed between amd64
// and arm64 within the first second; these digests are the same on amd64,
// arm64 and TinyGo's WebAssembly (make test-tinygo-digests). Print new values
// with CICADA_ACID_DIGEST_PRINT=1 and change them only with a stated reason.
const (
	acidOutputDigest = "d3a21889c94eaff5fa8f9785bc7eca225759e49f89c22e60c4ba8501b1269fa2"
	acidStateDigest  = "4f209df7987e574692a86fca5bbdd053090e0cda9d8a238a19b0fdea379db659"
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

// renderAcidPattern plays four bars of 16th notes at 130 BPM at one rate: bars
// 0 and 1, a four-second rest, then bars 2 and 3. Each bar starts from one of
// four settings and glides to the next; only bars 2 and 3 turn on the savage
// stage, and bar 2 the detuned second oscillator and bar 3 full drive. The
// rest comes before the savage stage on purpose: once the savage stage has
// been on, its blend decays toward zero without reaching it, and the resting
// filter path needs it at exactly zero. During the rest every envelope reaches
// its floor and the voice runs that resting path.
func renderAcidPattern(t *testing.T, rate int, output, state *bitdigest.Digest) {
	t.Helper()
	v, err := New(rate)
	if err != nil {
		t.Fatal(err)
	}
	bars := []Params{
		{Cutoff: 380, Resonance: .72, EnvMod: .85, Decay: .25, Accent: .9, Drive: .35, Release: .03, Slide: .06, Gate: 60, LevelDB: -6, PulseWidth: .5, Filter: Diode},
		{Cutoff: 1400, Resonance: .4, EnvMod: .5, Decay: 1.2, Accent: .6, Drive: .7, Release: .05, Slide: .1, Gate: 70, LevelDB: -4, PulseWidth: .3, Wave: 1, Sub: .4, Filter: Ladder},
		{Cutoff: 220, Resonance: .95, EnvMod: 1, Decay: .12, Accent: 1, Drive: .15, Release: .02, Slide: .04, Gate: 45, LevelDB: -8, PulseWidth: .5, Detune: 12, Wave: .5, Filter: Diode, Savage: true},
		{Cutoff: 3000, Resonance: .2, EnvMod: .3, Decay: 2.5, Accent: .3, Drive: 1, Release: .2, Slide: .15, Gate: 90, LevelDB: -10, PulseWidth: .7, Sub: 1, Filter: Ladder, Savage: true},
	}
	// The rest: the diode filter, no resonance (so the accent capacitor
	// empties in about three seconds) and a short release.
	rest := Params{Cutoff: 500, Resonance: 0, EnvMod: .5, Decay: .1, Accent: .5, Drive: .2, Release: .005, Slide: .06, Gate: 60, LevelDB: -6, PulseWidth: .5, Filter: Diode}
	notes := [16]int{36, 36, 48, -1, 39, 36, 43, 48, 36, 41, -1, 48, 46, 36, 43, 39}
	accent := [16]bool{true, false, false, false, true, false, false, true, false, false, false, true, false, true, false, false}
	slide := [16]bool{false, false, true, false, false, true, false, false, false, true, false, false, true, false, false, true}
	step := rate * 60 / 130 / 4
	alpha := 1 / (0.05 * float64(rate)) // the controls glide over about 50 ms
	block := make([]float32, 32)
	render := func(frames int, release func(i int)) {
		for i := 0; i < frames; i += len(block) {
			release(i)
			v.Render(block)
			output.Float32(block...)
			state.Float32(block...)
			state.Float64(v.meg, v.vca, v.cap, v.sweepStrength, v.accentGain, v.pitchLog,
				v.params.Cutoff, v.params.Resonance, v.params.EnvMod, v.params.Decay, v.params.Accent,
				v.filterBlend, v.savageBlend, v.restingFilterG,
				v.diode.state[0], v.diode.state[1], v.diode.state[2], v.diode.state[3],
				v.ladder.state[0], v.ladder.state[1], v.ladder.state[2], v.ladder.state[3])
		}
	}
	for _, bar := range []int{0, 1, -1, 2, 3} {
		if bar < 0 {
			v.NoteOff()
			if err := v.SetParams(rest); err != nil {
				t.Fatal(err)
			}
			render(4*rate, func(int) {})
			continue
		}
		if err := v.SetParams(bars[bar]); err != nil {
			t.Fatal(err)
		}
		if err := v.SetParamsTarget(bars[(bar+1)%len(bars)], alpha); err != nil {
			t.Fatal(err)
		}
		for s := range notes {
			if notes[s] < 0 {
				v.NoteOff()
			} else {
				v.NoteOn(uint8(notes[s]), accent[s], slide[s], 100)
				if bar%2 == 1 { // NoteOn resets expression; vibrato on bars 1 and 3
					v.SetExpression(expression.Params{VibratoRateHz: 5.5, VibratoDepthCents: 30, Set: true})
				}
			}
			release := step * 6 / 10
			render(step, func(i int) {
				if i >= release && !slide[(s+1)%16] {
					v.NoteOff()
				}
			})
		}
	}
	if v.Fault() {
		t.Fatalf("acid voice faulted at %d Hz", rate)
	}
}
