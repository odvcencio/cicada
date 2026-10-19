package graph

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/expression"
)

func TestSharedControlsFollowOnlyOwnedHeldAndReleasedVoices(t *testing.T) {
	for _, voices := range []int{4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) {
			program := Program{Len: 1, Nodes: [MaxNodes]Node{{Op: Pressure}}}
			group := expression.Group{ID: 7, Generation: 11}
			cohort := Cohort{NoteID: 3, Generation: LiveGeneration}
			var on func()
			var release func()
			var member func(float32) bool
			var shared func(expression.Group, expression.Controls) bool
			var next func() float32
			var active func(expression.Group) bool
			if voices == 4 {
				p, err := NewPool(program, 48000)
				if err != nil {
					t.Fatal(err)
				}
				on = func() {
					if _, err := p.NoteOn([4]uint8{60}, 1, 100, false, cohort); err != nil {
						t.Fatal(err)
					}
					p.BindGroup(cohort, group, expression.Controls{Timbre: .5})
					p.SetExpression(cohort, expression.Params{Set: true, Pressure: .2, Timbre: .5})
				}
				release = func() { p.Release(cohort) }
				member = func(value float32) bool {
					return p.SetExpression(cohort, expression.Params{Set: true, Pressure: value, Timbre: .5})
				}
				shared = p.SetGroupControls
				next = p.Next
				active = p.GroupActive
			} else {
				p, err := NewPoly(program, 48000)
				if err != nil {
					t.Fatal(err)
				}
				on = func() {
					p.NoteOnIdentity(60, 100, 3)
					p.BindGroupIdentity(3, group, expression.Controls{Timbre: .5})
					p.NoteExpressionIdentity(3, 0, .2, .5)
				}
				release = func() { p.NoteOffIdentity(3) }
				member = func(value float32) bool { p.NoteExpressionIdentity(3, 0, value, .5); return false }
				shared = p.SetGroupControls
				next = func() float32 { v, _ := p.NextStereo(); return v }
				active = p.GroupActive
			}
			on()
			next()
			release()
			if member(1) {
				t.Fatal("released member was mutable")
			}
			if shared(expression.Group{ID: 7, Generation: 10}, expression.Controls{Pressure: 1, Timbre: .5}) {
				t.Fatal("stale group generation changed a voice")
			}
			if !active(group) || !shared(group, expression.Controls{Pressure: .8, Timbre: .5}) {
				t.Fatal("group stopped following an audible release")
			}
			if got := next(); got <= .5 {
				t.Fatal("shared pressure did not reach released PCM", got)
			}
			if !shared(group, expression.Controls{Timbre: .5}) {
				t.Fatal("release lost its group")
			}
			if got := next(); got <= 0 || got > .2 {
				t.Fatal("member pressure was not frozen during release", got)
			}
			for range 2000 {
				next()
			}
			if active(group) || shared(group, expression.Controls{Pressure: 1, Timbre: .5}) {
				t.Fatal("finished voice still accepted group updates")
			}
			on()
			oldGroup := group
			group = expression.Group{ID: 8, Generation: 12}
			cohort.NoteID++
			for range voices {
				on()
			}
			if active(oldGroup) || shared(oldGroup, expression.Controls{Pressure: 1, Timbre: .5}) {
				t.Fatal("stolen slot inherited an old shared lane")
			}
			// Allocation and render work remain bounded after repeated group updates.
			if got := testing.AllocsPerRun(1000, func() { shared(group, expression.Controls{Pressure: .3, Timbre: .5}); next() }); got != 0 {
				t.Fatal("group callback allocated", got)
			}
			if math.IsNaN(float64(next())) {
				t.Fatal("nonfinite group output")
			}
		})
	}
}

func TestLegacyLegatoCannotInheritSharedLane(t *testing.T) {
	p, err := NewPoly(Program{Len: 1, Nodes: [MaxNodes]Node{{Op: Pressure}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	group := expression.Group{ID: 1, Generation: 1}
	p.NoteOn(60, 100, false)
	if !p.BindGroupNote(60, group, expression.Controls{Pressure: .8, Timbre: .5}) {
		t.Fatal("missing original binding")
	}
	p.NoteOn(64, 100, true)
	if p.GroupActive(group) || p.SetGroupControls(group, expression.Controls{Pressure: 1, Timbre: .5}) {
		t.Fatal("legato replacement retained old shared ownership")
	}
}
