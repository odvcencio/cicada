package drum

import (
	"math"
	"testing"
)

func TestSixLanesDeterministicAndFinite(t *testing.T) {
	for lane := Lane(0); lane < LaneCount; lane++ {
		first, err := New(48_000, 4242)
		if err != nil {
			t.Fatal(err)
		}
		second, err := New(48_000, 4242)
		if err != nil {
			t.Fatal(err)
		}
		first.Hit(lane, 110, true)
		second.Hit(lane, 110, true)
		energy := 0.0
		for i := 0; i < 24_000; i++ {
			l, r := first.NextStereo()
			ll, rr := second.NextStereo()
			if l != ll || r != rr || math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) {
				t.Fatalf("lane %s changed at %d", Names[lane], i)
			}
			energy += float64(l*l + r*r)
		}
		if energy < 1e-8 || first.Fault() {
			t.Fatalf("lane %s was silent or faulted", Names[lane])
		}
	}
}

func TestClosedHatChokesOpenHat(t *testing.T) {
	kit, err := New(48_000, 7)
	if err != nil {
		t.Fatal(err)
	}
	kit.Hit(OH, 100, false)
	for i := 0; i < 100; i++ {
		kit.NextStereo()
	}
	kit.Hit(CH, 100, false)
	for i := 0; i < 240; i++ {
		kit.NextStereo()
	}
	if kit.lanes[OH].current.active {
		t.Fatal("open hat remained active after 5 ms choke")
	}
}

func TestRetriggerFadesPriorVoice(t *testing.T) {
	kit, err := New(48_000, 7)
	if err != nil {
		t.Fatal(err)
	}
	kit.Hit(BD, 100, false)
	for i := 0; i < 100; i++ {
		kit.NextStereo()
	}
	kit.Hit(BD, 100, false)
	if kit.lanes[BD].fadeRemaining != 48 {
		t.Fatal("old voice did not enter 1 ms fade")
	}
	for i := 0; i < 48; i++ {
		kit.NextStereo()
	}
	if kit.lanes[BD].fadeRemaining != 0 {
		t.Fatal("retrigger fade did not finish")
	}
}

func TestMetalModeChangesHatSound(t *testing.T) {
	plain, _ := New(48_000, 7)
	metal, _ := New(48_000, 7)
	p := metal.Params(CH)
	p.Metal = true
	if err := metal.SetParams(CH, p); err != nil {
		t.Fatal(err)
	}
	plain.Hit(CH, 100, false)
	metal.Hit(CH, 100, false)
	different := false
	for i := 0; i < 1000; i++ {
		l, _ := plain.NextStereo()
		m, _ := metal.NextStereo()
		if l != m {
			different = true
		}
	}
	if !different {
		t.Fatal("metal mode did not change hat output")
	}
}

func TestRenderSampleDoesNotAllocate(t *testing.T) {
	kit, err := New(48_000, 42)
	if err != nil {
		t.Fatal(err)
	}
	kit.Hit(CH, 100, false)
	allocs := testing.AllocsPerRun(1000, func() { kit.NextStereo() })
	if allocs != 0 {
		t.Fatalf("NextStereo allocated %.2f objects per sample", allocs)
	}
}
