package organ

import (
	"fmt"
	"math"
	"testing"
)

// The original idle path updated cached sine values after advancing phases.
// All other DSP was already bypassed after its existing quarter-second rule.
// Keep that path as a reference for state continuity through later notes.
func idleFullReference(p *Instrument) (float32, float32) {
	if p.idleFrames <= p.rate/4 {
		return p.NextStereo()
	}
	for i := range p.drawbar {
		p.drawbar[i] += float32(p.drawbarAlpha * float32(p.drawbarTarget[i]-p.drawbar[i]))
	}
	for i := range p.wheelPhase {
		p.wheelPhase[i] += p.wheelStep[i]
		p.wheelValue[i] = float32(p.phaseSine(p.wheelPhase[i]) * p.wheelGain[i])
	}
	p.percussion = float32(p.percussion * p.percussionDecay)
	if p.percussion < .0000001 {
		p.percussion = 0
	}
	p.stealTail = float32(p.stealTail * p.stealDecay)
	p.idleFrames++
	return 0, 0
}

func TestIdleOrganPreservesGeneratorPhaseAndRetriggerPCM(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			a := mustOrgan(t, rate, DefaultParams())
			b := mustOrgan(t, rate, DefaultParams())
			for frame := 0; frame < 95000; frame++ {
				if frame == 171 {
					drawbars := [9]uint8{8, 3, 6, 1, 2, 4, 0, 2, 1}
					a.SetDrawbars(drawbars)
					b.SetDrawbars(drawbars)
				}
				if frame == 20000 || frame == 67000 {
					a.NoteOn(60, 108)
					b.NoteOn(60, 108)
				}
				if frame == 21000 || frame == 68000 {
					a.NoteOff(60)
					b.NoteOff(60)
				}
				aL, aR := a.NextStereo()
				bL, bR := idleFullReference(b)
				if math.Float32bits(aL) != math.Float32bits(bL) || math.Float32bits(aR) != math.Float32bits(bR) {
					t.Fatalf("frame=%d optimized=(%g,%g) reference=(%g,%g)", frame, aL, aR, bL, bR)
				}
				if a.wheelPhase != b.wheelPhase || a.drawbar != b.drawbar || a.idleFrames != b.idleFrames || a.percussion != b.percussion || a.stealTail != b.stealTail {
					t.Fatalf("generator/control state differs at frame %d", frame)
				}
			}
			a.Reset()
			if allocs := testing.AllocsPerRun(100, func() {
				for range 128 {
					a.NextStereo()
				}
			}); allocs != 0 {
				t.Fatalf("idle organ allocations=%g", allocs)
			}
		})
	}
}

func BenchmarkIdleOrgan(b *testing.B) {
	for _, full := range []bool{false, true} {
		b.Run(fmt.Sprintf("full=%t", full), func(b *testing.B) {
			p := mustOrgan(b, 48000, DefaultParams())
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if full {
					idleFullReference(p)
				} else {
					p.NextStereo()
				}
			}
		})
	}
}
