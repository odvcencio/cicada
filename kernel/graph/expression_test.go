package graph

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
)

func TestExpressionInputsPitchAndNeutralDefaults(t *testing.T) {
	p := Program{Len: 4, Output: 0}
	p.Nodes[0], p.Nodes[1], p.Nodes[2], p.Nodes[3] = Node{Op: Pitch}, Node{Op: PitchBend}, Node{Op: Pressure}, Node{Op: Timbre}
	v, err := NewVoice(p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	v.NoteOn(69, 127, false)
	if v.Next() != 440 || v.values[1] != 0 || v.values[2] != 0 || v.values[3] != .5 {
		t.Fatalf("incorrect neutral inputs: %v", v.values[:4])
	}
	v.NoteExpression(1200, .23456789, .7654321)
	if v.Next() != 880 || v.values[1] != 1200 || v.values[2] != .23456789 || v.values[3] != .7654321 {
		t.Fatalf("incorrect expression inputs: %v", v.values[:4])
	}
	v.NoteOn(69, 127, true)
	if v.Next() != 440 || v.values[1] != 0 || v.values[2] != 0 || v.values[3] != .5 {
		t.Fatal("expression leaked across note identities")
	}
}

func TestVibratoPreservesPhaseAcrossTiedUpdatesAndAllocatesNothing(t *testing.T) {
	p := Program{Len: 1}
	p.Nodes[0] = Node{Op: Pitch}
	v, _ := NewVoice(p, 48000)
	v.NoteOn(69, 127, false)
	params := expression.Params{Set: true, Timbre: .5, VibratoRateHz: 5, VibratoDepthCents: 100}
	v.SetExpression(params)
	var last float32
	for range 2400 {
		last = v.Next()
	}
	if math.Abs(float64(last)-440*math.Exp2(100.0/1200)) > .01 {
		t.Fatalf("vibrato quarter-cycle pitch %g", last)
	}
	v.SetExpression(params)
	if got := v.Next(); math.Abs(float64(got-last)) > .01 {
		t.Fatal("tied expression update restarted vibrato")
	}
	if count := testing.AllocsPerRun(100, func() { v.SetExpression(params); v.Next() }); count != 0 {
		t.Fatalf("expression render allocated %v", count)
	}
	v.SetExpression(expression.Params{Set: true, Timbre: .5})
	if v.Next() != 440 {
		t.Fatal("explicit zero did not reset bend/vibrato")
	}
}

func TestPoolExpressionTargetsOnlyGatedCohort(t *testing.T) {
	program := Program{Len: 1, Nodes: [MaxNodes]Node{{Op: Pressure}}}
	pool, err := NewPool(program, 48000)
	if err != nil {
		t.Fatal(err)
	}
	first := Cohort{NoteID: 1, Generation: 3}
	second := Cohort{NoteID: 1, Generation: 4}
	if _, err := pool.NoteOn([4]uint8{60, 64, 67}, 3, 100, false, first); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.NoteOn([4]uint8{72}, 1, 100, false, second); err != nil {
		t.Fatal(err)
	}
	params := expression.Params{Set: true, Pressure: .25, Timbre: .5}
	if !pool.SetExpression(first, params) || pool.Next() != .75 {
		t.Fatal("expression did not reach exactly the three matching chord pitches")
	}
	if pool.SetExpression(Cohort{NoteID: 1, Generation: 2}, params) {
		t.Fatal("stale generation changed a replacement cohort")
	}
	if count := testing.AllocsPerRun(100, func() { pool.SetExpression(first, params); pool.Next() }); count != 0 {
		t.Fatalf("cohort expression allocated %g objects", count)
	}
	pool.Release(first)
	if pool.SetExpression(first, expression.Params{Set: true, Pressure: 1, Timbre: .5}) {
		t.Fatal("released voices accepted expression for an old gate")
	}
}
