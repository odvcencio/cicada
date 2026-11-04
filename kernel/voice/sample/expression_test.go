package sample

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
)

func TestSampleExpressionChangesPitchAndPressureWithoutRetrigger(t *testing.T) {
	pcm := make([]float32, 4096)
	for i := range pcm {
		pcm[i] = .25
	}
	v, err := New(48000, Region{Left: pcm, SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: true, LoopEnd: len(pcm)})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.NoteOn(60, 127); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		v.NextStereo()
	}
	phase := v.phase
	if err := v.SetExpression(expression.Params{Set: true, PitchCents: 1200, Pressure: 1, Timbre: .5}); err != nil {
		t.Fatal(err)
	}
	if v.phase != phase {
		t.Fatal("expression restarted the sample")
	}
	v.NextStereo()
	if math.Abs(v.ratio-2) > 1e-6 || math.Abs(v.phase-phase-2) > 1e-6 {
		t.Fatalf("pitch did not change playback: ratio %g phase %g", v.ratio, v.phase)
	}
	if v.pressureGain <= 1 || v.pressureGain >= 2 {
		t.Fatalf("pressure did not ramp: %g", v.pressureGain)
	}
	for i := 0; i < 4800; i++ {
		v.NextStereo()
	}
	if math.Abs(v.pressureGain-2) > .001 {
		t.Fatalf("pressure target: %g", v.pressureGain)
	}
	if got := testing.AllocsPerRun(100, func() { v.NextStereo() }); got != 0 {
		t.Fatalf("render allocated: %g", got)
	}
	if err := v.SetExpression(expression.Params{Set: true, PitchCents: 9600, Timbre: .5}); err != nil {
		t.Fatal(err)
	}
	v.NextStereo()
	if v.ratio != 8 {
		t.Fatalf("bend did not saturate at the supported ratio: %g", v.ratio)
	}
	if err := v.SetExpression(expression.Params{Set: true, Timbre: .8}); err == nil {
		t.Fatal("silently accepted unsupported timbre")
	}
	if v.expression.PitchCents != 9600 {
		t.Fatal("invalid control changed the current expression")
	}
}

func TestSamplePoolExpressionFollowsNoteIdentityAndRelease(t *testing.T) {
	pcm := make([]float32, 4096)
	for i := range pcm {
		pcm[i] = .25
	}
	p, err := NewPool(48000, 2, Region{Left: pcm, SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: true, LoopEnd: len(pcm)})
	if err != nil {
		t.Fatal(err)
	}
	a, b := Cohort{1, 7}, Cohort{2, 7}
	ha, err := p.NoteOnCohort(60, 127, a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := p.NoteOnCohort(60, 127, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetCohortControls(a, 1200, .7, .5); err != nil {
		t.Fatal(err)
	}
	p.NextStereo()
	if p.voices[ha.Slot].ratio != 2 || p.voices[hb.Slot].ratio != 1 {
		t.Fatal("one note's bend affected another note")
	}
	group := expression.Group{ID: 3, Generation: 7}
	if err := p.BindGroup(a, group, expression.Controls{Timbre: .5}); err != nil {
		t.Fatal(err)
	}
	p.ReleaseCohort(a)
	if err := p.SetCohortControls(a, 0, 0, .5); err != nil {
		t.Fatal(err)
	}
	if p.voices[ha.Slot].expression.PitchCents != 1200 {
		t.Fatal("released member controls changed")
	}
	if err := p.SetGroupControls(group, expression.Controls{PitchCents: -1200, Timbre: .5}); err != nil {
		t.Fatal(err)
	}
	if p.voices[ha.Slot].expression.PitchCents != 0 {
		t.Fatal("shared controls did not reach the release")
	}
	if _, err := p.NoteOnCohort(62, 127, Cohort{3, 7}); err != nil {
		t.Fatal(err)
	}
	if p.NoteOff(ha) {
		t.Fatal("stale handle released a replacement voice")
	}
}

func TestSamplePitchReturnsToNeutral(t *testing.T) {
	pcm := make([]float32, 256)
	v, err := New(48000, Region{Left: pcm, SampleRate: 48000, RootKey: 60, End: len(pcm), Loop: true, LoopEnd: len(pcm)})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.NoteOn(60, 127); err != nil {
		t.Fatal(err)
	}
	if err := v.SetExpression(expression.Params{Set: true, PitchCents: 1200, Timbre: .5}); err != nil {
		t.Fatal(err)
	}
	v.NextStereo()
	if v.Ratio() != 2 {
		t.Fatal("bend did not apply")
	}
	if err := v.SetExpression(expression.Params{Set: true, Timbre: .5}); err != nil {
		t.Fatal(err)
	}
	v.NextStereo()
	if v.Ratio() != 1 {
		t.Fatalf("neutral bend did not restore rate: %g", v.Ratio())
	}
}
