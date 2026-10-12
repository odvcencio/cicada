package keyboard

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/voice/keyboard"
)

// liveCase names one live control by its patch and parameter name; the slot is
// looked up from the score parameter table so the test shares no constant with
// the voices.
type liveCase struct {
	patch, name string
	from, to    float32
	// noJump marks controls with no step to smooth: drawbars and rotary speed
	// apply through their own setters, and a new tremolo rate only bends the
	// phase of the next cycle.
	noJump bool
}

var liveCases = []liveCase{
	{"tine_tremolo", "tremolo", .1, .9, false},
	{"tine_tremolo", "tremolo_rate", 4, 9, true},
	{"tine_tremolo", "auto_pan", .1, .9, false},
	{"tine_ep", "gain", .5, 1.6, false},
	{"tine_ep", "drive", 0, .9, false},
	{"reed_tremolo", "tremolo", .2, .9, false},
	{"clav", "gain", .5, 1.6, false},
	{"clav", "drive", 0, .9, false},
	{"tonewheel_organ", "gain", .5, 1.6, false},
	{"tonewheel_organ", "drive", 0, .9, false},
	{"tonewheel_organ", "rotary_mix", .1, 1, false},
	{"organ_full", "rotary_fast", 0, 1, true},
	{"organ_full", "drawbar1", 0, 8, true},
	{"organ_full", "drawbar9", 0, 8, true},
	{"fm_ep", "gain", .3, 1.4, false},
	{"bell_keys", "gain", .3, 1.4, false},
	{"poly_keys", "drive", 0, .9, false},
	{"poly_keys", "output", -20, -2, false},
	{"sync_lead", "output", -20, -2, false},
	{"string_machine", "output", -20, -2, false},
}

func slotOf(t *testing.T, patch, name string) int {
	t.Helper()
	for _, p := range Parameters(patch) {
		if p.Name == name {
			return p.Index
		}
	}
	t.Fatalf("%s has no parameter %s", patch, name)
	return 0
}

// liveVoice prepares a patch with the control at its starting value and plays
// a chord past the attack, so the control acts on sound already running.
func liveVoice(t *testing.T, c liveCase) (keyboard.Voice, int) {
	t.Helper()
	s, err := DefaultSpec(c.patch)
	if err != nil {
		t.Fatal(err)
	}
	slot := slotOf(t, c.patch, c.name)
	s.Controls[slot] = c.from
	v, err := New(48000, &s)
	if err != nil {
		t.Fatalf("%s %s=%g: %v", c.patch, c.name, c.from, err)
	}
	for _, note := range [...]uint8{48, 60, 64, 67} {
		if err := v.NoteOn(note, 100); err != nil {
			t.Fatal(err)
		}
	}
	for range 1024 {
		v.NextStereo()
	}
	return v, slot
}

const liveBlock = 4096

func renderLive(v keyboard.Voice) (l, r [liveBlock]float32) {
	for i := range l {
		l[i], r[i] = v.NextStereo()
	}
	return
}

// onset is the largest difference from the untouched reference within the
// first onsetFrames frames after a change, over both channels. size is the
// largest difference over the whole block.
const onsetFrames = 16

func onset(a, ra, b, rb [liveBlock]float32) (first, size float64) {
	for i := range a {
		d := math.Max(math.Abs(float64(a[i]-b[i])), math.Abs(float64(ra[i]-rb[i])))
		if i < onsetFrames {
			first = math.Max(first, d)
		}
		size = math.Max(size, d)
	}
	return
}

// A step in a control must glide. In the first 16 frames after the change
// (0.3 ms) a 20 ms one-pole glide, which is what the engine derives from the
// registry, may add at most a fifth of the sound change an immediate step adds.
// A zipper step is exactly that early change.
func TestLiveControlStepGlidesWithoutZipperNoise(t *testing.T) {
	glide := float32(1 - math.Exp(-1/(.02*48000)))
	for _, c := range liveCases {
		t.Run(c.patch+"/"+c.name, func(t *testing.T) {
			ref, _ := liveVoice(t, c)
			refL, refR := renderLive(ref)
			now, slot := liveVoice(t, c)
			if err := now.(keyboard.Controller).SetControl(slot, c.to, 1); err != nil {
				t.Fatal(err)
			}
			nowL, nowR := renderLive(now)
			immediate, size := onset(nowL, nowR, refL, refR)
			if size == 0 {
				t.Fatalf("setting %s changed nothing audible", c.name)
			}
			if c.noJump {
				return
			}
			slow, _ := liveVoice(t, c)
			if err := slow.(keyboard.Controller).SetControl(slot, c.to, glide); err != nil {
				t.Fatal(err)
			}
			slowL, slowR := renderLive(slow)
			gliding, _ := onset(slowL, slowR, refL, refR)
			if gliding > immediate/5 {
				t.Fatalf("glide changes the first %d frames by %.3g; an immediate step changes them by %.3g (bound: one fifth)", onsetFrames, gliding, immediate)
			}
		})
	}
}

// With no control change the output must not move: a rejected call changes
// nothing, and a voice that was never touched renders as before.
func TestLiveControlRejectsBadCallsWithoutChangingSound(t *testing.T) {
	for _, c := range liveCases {
		t.Run(c.patch+"/"+c.name, func(t *testing.T) {
			ref, _ := liveVoice(t, c)
			refL, refR := renderLive(ref)
			v, slot := liveVoice(t, c)
			controller := v.(keyboard.Controller)
			for _, bad := range []struct {
				slot         int
				value, glide float32
			}{
				{slot, float32(math.NaN()), 1}, {slot, 1e9, 1}, {slot, -1e9, 1}, {slot, c.to, 0}, {slot, c.to, 1.5},
				{slot, c.to, float32(math.NaN())}, {-1, c.to, 1}, {127, c.to, 1},
			} {
				if err := controller.SetControl(bad.slot, bad.value, bad.glide); err == nil {
					t.Fatalf("SetControl(%d, %g, %g) was accepted", bad.slot, bad.value, bad.glide)
				}
			}
			gotL, gotR := renderLive(v)
			if gotL != refL || gotR != refR {
				t.Fatal("rejected calls changed the sound")
			}
		})
	}
	v, _ := liveVoice(t, liveCase{patch: "tonewheel_organ", name: "drawbar1", from: 0})
	for _, bad := range []float32{.5, 8.5, -1} {
		if err := v.(keyboard.Controller).SetControl(1, bad, 1); err == nil {
			t.Fatalf("drawbar accepted %g", bad)
		}
	}
	if err := v.(keyboard.Controller).SetControl(20, .5, 1); err == nil {
		t.Fatal("rotary speed accepted .5")
	}
}

// A control settles on its exact target, after which the voice does no extra
// work and renders as a voice prepared with that value does.
func TestLiveControlGlideSettlesOnTarget(t *testing.T) {
	glide := float32(1 - math.Exp(-1/(.02*48000)))
	for _, c := range liveCases {
		if c.noJump {
			continue
		}
		t.Run(c.patch+"/"+c.name, func(t *testing.T) {
			v, slot := liveVoice(t, c)
			if err := v.(keyboard.Controller).SetControl(slot, c.to, glide); err != nil {
				t.Fatal(err)
			}
			for range 96000 { // two seconds, far past the 20 ms constant
				v.NextStereo()
			}
			again, _ := liveVoice(t, c)
			// Same voice, set at once to the same target: both must end alike
			// once settled, within float32 rounding of the one-pole.
			if err := again.(keyboard.Controller).SetControl(slot, c.to, 1); err != nil {
				t.Fatal(err)
			}
			for range 96000 {
				again.NextStereo()
			}
			a, b := renderLive(v)
			x, y := renderLive(again)
			var worst, peak float64
			for i := range a {
				worst = math.Max(worst, math.Abs(float64(a[i]-x[i])))
				worst = math.Max(worst, math.Abs(float64(b[i]-y[i])))
				peak = math.Max(peak, math.Abs(float64(a[i])))
			}
			if peak > 1e-5 && worst > peak*1e-3 {
				t.Fatalf("settled glide differs from an immediate set by %.3g of peak %.3g", worst, peak)
			}
		})
	}
}

func TestLiveControlsAllocateNothing(t *testing.T) {
	glide := float32(1 - math.Exp(-1/(.02*48000)))
	for _, c := range liveCases {
		t.Run(c.patch+"/"+c.name, func(t *testing.T) {
			v, slot := liveVoice(t, c)
			controller := v.(keyboard.Controller)
			flip := false
			var setErr error
			allocs := testing.AllocsPerRun(50, func() {
				flip = !flip
				value := c.from
				if flip {
					value = c.to
				}
				if setErr = controller.SetControl(slot, value, glide); setErr != nil {
					return
				}
				for range 256 {
					v.NextStereo()
				}
			})
			if setErr != nil || allocs != 0 {
				t.Fatalf("err=%v allocations=%g", setErr, allocs)
			}
		})
	}
}

// Organ drive 0 bypasses the saturator, but any larger drive does not, so a
// glide down to 0 must fade the saturator out rather than switch it off with
// a step. A quarter second later the voice must sound like one set to 0.
func TestOrganDriveGlideToZeroSettlesToTheBypass(t *testing.T) {
	glide := float32(1 - math.Exp(-1/(.02*48000)))
	driven, slot := liveVoice(t, liveCase{patch: "organ_full", name: "drive", from: .5})
	static, _ := liveVoice(t, liveCase{patch: "organ_full", name: "drive", from: 0})
	if err := driven.(keyboard.Controller).SetControl(slot, 0, glide); err != nil {
		t.Fatal(err)
	}
	for range 12000 { // 250 ms
		driven.NextStereo()
		static.NextStereo()
	}
	var difference, reference float64
	for range 12000 {
		l, r := driven.NextStereo()
		wl, wr := static.NextStereo()
		difference += float64(l-wl)*float64(l-wl) + float64(r-wr)*float64(r-wr)
		reference += float64(wl)*float64(wl) + float64(wr)*float64(wr)
	}
	if reference == 0 || math.Sqrt(difference/reference) > .003 {
		t.Fatalf("after 250 ms the glided voice differs from a drive-0 voice by %.3g of its level", math.Sqrt(difference/reference))
	}
}

// The level trim wraps every voice NewCalibrated builds. Live controls must
// reach the voice through it, and the trim must stay applied.
func TestCalibratedVoiceForwardsLiveControls(t *testing.T) {
	for _, c := range liveCases {
		t.Run(c.patch+"/"+c.name, func(t *testing.T) {
			s, err := DefaultSpec(c.patch)
			if err != nil {
				t.Fatal(err)
			}
			slot := slotOf(t, c.patch, c.name)
			s.Controls[slot] = c.from
			build := func(prepare func(int, *keyboard.Spec) (keyboard.Voice, error)) keyboard.Voice {
				v, err := prepare(48000, &s)
				if err != nil {
					t.Fatal(err)
				}
				for _, note := range [...]uint8{48, 60, 64, 67} {
					_ = v.NoteOn(note, 100)
				}
				for range 1024 {
					v.NextStereo()
				}
				return v
			}
			trimmed, raw := build(NewCalibrated), build(New)
			if _, wrapped := trimmed.(*calibrated); wrapped != (LevelTrimLinear(s.Patch) != 1) {
				t.Fatalf("patch %s wrapped=%v", c.patch, wrapped)
			}
			ref := build(NewCalibrated)
			refL, refR := renderLive(ref)
			controller, ok := trimmed.(keyboard.Controller)
			if !ok {
				t.Fatal("calibrated voice lost its live controls")
			}
			if err := controller.SetControl(slot, c.to, 1); err != nil {
				t.Fatal(err)
			}
			gotL, gotR := renderLive(trimmed)
			if _, size := onset(gotL, gotR, refL, refR); size == 0 {
				t.Fatalf("%s did not change a calibrated voice", c.name)
			}
			if err := raw.(keyboard.Controller).SetControl(slot, c.to, 1); err != nil {
				t.Fatal(err)
			}
			rawL, _ := renderLive(raw)
			gain := LevelTrimLinear(s.Patch)
			var worst float64
			for i := range rawL {
				worst = math.Max(worst, math.Abs(float64(gotL[i])-float64(rawL[i]*gain)))
			}
			if worst > 1e-6 {
				t.Fatalf("calibrated output is not the trimmed raw output after the control: %g", worst)
			}
		})
	}
}
