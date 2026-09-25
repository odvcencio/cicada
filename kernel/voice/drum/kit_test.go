package drum

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/graph"
)

func TestElevenLanesDeterministicAndFinite(t *testing.T) {
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

func TestDefaultLanePeakAndDC(t *testing.T) {
	for _, rate := range []int{44_100, 48_000, 96_000} {
		for lane := Lane(0); lane < LaneCount; lane++ {
			kit, err := New(rate, 4242)
			if err != nil {
				t.Fatal(err)
			}
			kit.Hit(lane, 127, false)
			peak, sum := 0.0, 0.0
			frames := 3 * rate
			for sample := 0; sample < frames; sample++ {
				left, right := kit.NextStereo()
				// Undo the equal-power center pan to measure the lane's
				// pre-master level without counting pan as attenuation.
				mono := (float64(left) + float64(right)) / math.Sqrt2
				peak = math.Max(peak, math.Abs(mono))
				sum += mono
			}
			peakDB := 20 * math.Log10(peak)
			dcDB := 20 * math.Log10(math.Abs(sum/float64(frames)))
			if peakDB < -6 || peakDB > -1 || dcDB > -60 || kit.Fault() {
				t.Errorf("%d Hz %s: peak %.2f dBFS, DC %.2f dBFS, fault=%v", rate, Names[lane], peakDB, dcDB, kit.Fault())
			}
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

func TestCymbalDecayIsNotCutAtFourSeconds(t *testing.T) {
	kit, err := New(48_000, 7)
	if err != nil {
		t.Fatal(err)
	}
	p := kit.Params(CY)
	p.Decay = 4
	if err := kit.SetParams(CY, p); err != nil {
		t.Fatal(err)
	}
	kit.Hit(CY, 100, false)
	for i := 0; i < 4*48_000+1; i++ {
		kit.NextStereo()
	}
	if !kit.lanes[CY].current.active {
		t.Fatal("long cymbal decay was cut at four seconds")
	}
}

func TestLanePanAndOffLevel(t *testing.T) {
	kit, err := New(48_000, 42)
	if err != nil {
		t.Fatal(err)
	}
	p := kit.Params(BD)
	p.Pan = 1
	if err := kit.SetParams(BD, p); err != nil {
		t.Fatal(err)
	}
	kit.Hit(BD, 127, false)
	leftEnergy, rightEnergy := 0.0, 0.0
	for i := 0; i < 1000; i++ {
		l, r := kit.NextStereo()
		leftEnergy += float64(l * l)
		rightEnergy += float64(r * r)
	}
	if leftEnergy > 1e-12 || rightEnergy < 1e-8 {
		t.Fatalf("pan did not isolate right channel: %g %g", leftEnergy, rightEnergy)
	}
	kit.Reset()
	p.LevelDB = -1000
	if err := kit.SetParams(BD, p); err != nil {
		t.Fatal(err)
	}
	kit.Hit(BD, 127, false)
	for i := 0; i < 1000; i++ {
		l, r := kit.NextStereo()
		if l != 0 || r != 0 {
			t.Fatal("off lane produced output")
		}
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

func TestMappedRecipeMatchesNativeVoice(t *testing.T) {
	const seed = uint32(73)
	mapped, err := New(48_000, seed)
	if err != nil {
		t.Fatal(err)
	}
	// Match the per-source noise seeds so this compares the recipe itself.
	nativeLane := SD
	nativeSeed := seed ^ uint32(BD+1)*0x9e3779b9 ^ uint32(nativeLane+1)*0x9e3779b9
	native, err := New(48_000, nativeSeed)
	if err != nil {
		t.Fatal(err)
	}
	if err := mapped.SetRecipe(BD, SD); err != nil {
		t.Fatal(err)
	}
	if mapped.Params(BD) != DefaultParams(SD) {
		t.Fatal("mapped lane did not receive recipe defaults")
	}
	mapped.Hit(BD, 107, false)
	native.Hit(SD, 107, false)
	for i := 0; i < 2000; i++ {
		left, right := mapped.NextStereo()
		nativeLeft, nativeRight := native.NextStereo()
		if left != nativeLeft || right != nativeRight {
			t.Fatalf("mapped snare differs from native snare at sample %d", i)
		}
	}
}

func TestMappedLanesRemainIndependentAndCanBeDisabled(t *testing.T) {
	kit, err := New(48_000, 73)
	if err != nil {
		t.Fatal(err)
	}
	for lane := Lane(0); lane < LaneCount; lane++ {
		if err := kit.Disable(lane); err != nil {
			t.Fatal(err)
		}
	}
	if err := kit.SetRecipe(BD, SD); err != nil {
		t.Fatal(err)
	}
	if err := kit.SetRecipe(CP, SD); err != nil {
		t.Fatal(err)
	}
	kit.Hit(BD, 100, false)
	kit.Hit(CP, 110, false)
	if !kit.lanes[BD].current.active || !kit.lanes[CP].current.active {
		t.Fatal("source lanes sharing one recipe did not get independent voices")
	}
	if err := kit.Disable(BD); err != nil {
		t.Fatal(err)
	}
	if kit.lanes[BD].current.active || !kit.lanes[CP].current.active {
		t.Fatal("disabling one source lane affected another")
	}
	energy := 0.0
	for i := 0; i < 1000; i++ {
		left, right := kit.NextStereo()
		energy += float64(left*left + right*right)
	}
	if energy == 0 || kit.Fault() {
		t.Fatal("remaining mapped lane was silent or faulted")
	}
	if err := kit.Disable(CP); err != nil {
		t.Fatal(err)
	}
	kit.Hit(CP, 127, false)
	left, right := kit.NextStereo()
	if left != 0 || right != 0 {
		t.Fatal("omitted lane produced sound")
	}
	if err := kit.SetRecipe(BD, LaneCount); err == nil {
		t.Fatal("invalid recipe was accepted")
	}
}

func TestAuthoredGraphLaneRendersAndChokes(t *testing.T) {
	program := graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Gate}}}
	kit, err := New(48_000, 73)
	if err != nil {
		t.Fatal(err)
	}
	for lane := Lane(0); lane < LaneCount; lane++ {
		if err := kit.Disable(lane); err != nil {
			t.Fatal(err)
		}
	}
	if err := kit.SetGraph(OH, program); err != nil {
		t.Fatal(err)
	}
	if err := kit.SetGraph(CH, program); err != nil {
		t.Fatal(err)
	}
	kit.Hit(OH, 100, false)
	left, right := kit.NextStereo()
	if left <= 0 || right <= 0 || left != right {
		t.Fatalf("authored open-hat graph did not render in stereo: %g %g", left, right)
	}
	kit.Hit(CH, 100, false)
	for i := 0; i < 240; i++ {
		kit.NextStereo()
	}
	if kit.lanes[OH].customActive {
		t.Fatal("closed hat did not choke authored open hat within 5 ms")
	}
	if err := kit.Disable(CH); err != nil {
		t.Fatal(err)
	}
	left, right = kit.NextStereo()
	if left != 0 || right != 0 {
		t.Fatal("authored lanes produced sound after choke and disable")
	}
}

func TestAuthoredGraphLaneRetriggerAndAllocationFree(t *testing.T) {
	program := graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Gate}}}
	kit, err := New(48_000, 73)
	if err != nil {
		t.Fatal(err)
	}
	for lane := Lane(0); lane < LaneCount; lane++ {
		if err := kit.Disable(lane); err != nil {
			t.Fatal(err)
		}
	}
	if err := kit.SetGraph(BD, program); err != nil {
		t.Fatal(err)
	}
	kit.Hit(BD, 100, false)
	kit.NextStereo()
	kit.Hit(BD, 127, true)
	if kit.lanes[BD].fadeRemaining != 48 {
		t.Fatal("authored graph retrigger did not fade prior state")
	}
	allocs := testing.AllocsPerRun(1000, func() { kit.NextStereo() })
	if allocs != 0 {
		t.Fatalf("authored graph allocated %.2f objects per sample", allocs)
	}
	if err := kit.SetGraph(BD, graph.Program{}); err == nil {
		t.Fatal("invalid graph was accepted")
	}
}

func TestAuthoredGraphLaneReceivesSourcePitch(t *testing.T) {
	program := graph.Program{Len: 1, Nodes: [graph.MaxNodes]graph.Node{{Op: graph.Pitch}}}
	for _, lane := range []Lane{BD, CY} {
		kit, err := New(48_000, 73)
		if err != nil {
			t.Fatal(err)
		}
		for other := Lane(0); other < LaneCount; other++ {
			if err := kit.Disable(other); err != nil {
				t.Fatal(err)
			}
		}
		if err := kit.SetGraph(lane, program); err != nil {
			t.Fatal(err)
		}
		kit.Hit(lane, 100, false)
		left, right := kit.NextStereo()
		want := float32(440*math.Exp2((float64(MIDINotes[lane])-69)/12)) * float32(math.Pow(10, -6.0/20)/math.Sqrt2)
		if math.Abs(float64(left-want)) > 1e-5 || left != right {
			t.Fatalf("lane %s graph pitch: left=%g right=%g want=%g", Names[lane], left, right, want)
		}
	}
}
