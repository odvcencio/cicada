package seq

import (
	"reflect"
	"testing"
)

func TestTiedExpressionEventsPreserveBlockInvariance(t *testing.T) {
	p := gatePattern(t, note(69), Step{Gate: true, Tie: true, Ratchet: 1, Probability: 100}, rest(), rest())
	p.Expression = new([64]Expression)
	p.Expression[1] = Expression{Set: true, PitchCents: 200, Timbre: .5}
	reference := collectGateEvents(t, p, 1, 1)
	for _, block := range []int{64, 128, 1024} {
		if got := collectGateEvents(t, p, block, 1); !reflect.DeepEqual(got, reference) {
			t.Fatalf("expression events changed at block %d", block)
		}
	}
	if len(reference) < 3 || reference[0].Kind != NoteOn || reference[1].Kind != NoteExpression || reference[1].Tick != TicksPerStep || reference[2].Kind != NoteOff {
		t.Fatalf("incorrect tie expression ordering: %+v", reference[:min(3, len(reference))])
	}
}
