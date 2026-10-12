package fx

import (
	"fmt"
	"math"
	"testing"
)

// Run the previous full kernels alongside the silent paths. Forcing only the
// conservative state predicate does not alter any signal or parameter state.
func TestSilentEffectsPreserveFullKernelPCM(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, kind := range []string{"drive", "delay", "reverb"} {
			t.Run(fmt.Sprintf("%s/%d", kind, rate), func(t *testing.T) {
				var actual, full Processor
				var forceFull func()
				var automate func(int)
				switch kind {
				case "drive":
					a, _ := NewDrive(rate)
					b, _ := NewDrive(rate)
					actual, full = a, b
					forceFull = func() { b.zeroFrames = 0 }
					automate = func(frame int) {
						p := a.Params()
						p.Shape = DriveShape((frame / 31) % int(driveShapes))
						p.GainDB, p.Mix, p.ToneHz = 31, .37, 1900
						if err := a.SetParams(p); err != nil {
							t.Fatal(err)
						}
						if err := b.SetParams(p); err != nil {
							t.Fatal(err)
						}
					}
				case "delay":
					a, _ := NewDelay(rate, 120000)
					b, _ := NewDelay(rate, 120000)
					actual, full = a, b
					forceFull = func() { b.excited = true }
					automate = func(frame int) {
						p := a.Params()
						p.Division, p.TimeMs = FreeDelay, 17.31+float64(frame%7)
						p.Feedback, p.Width, p.Mix, p.PingPong = .85, .73, .83, frame%2 != 0
						if err := a.SetParams(p); err != nil {
							t.Fatal(err)
						}
						if err := b.SetParams(p); err != nil {
							t.Fatal(err)
						}
					}
				case "reverb":
					a, _ := NewReverb(rate)
					b, _ := NewReverb(rate)
					actual, full = a, b
					forceFull = func() { b.excited = true }
					automate = func(frame int) {
						p := a.Params()
						p.Size, p.PredelayMs = .5+float64(frame%11)/10, float64(frame%200)
						p.DecaySec, p.Mix, p.DampHz, p.HighpassHz = 8.7, .81, 3200, 80
						if err := a.SetParams(p); err != nil {
							t.Fatal(err)
						}
						if err := b.SetParams(p); err != nil {
							t.Fatal(err)
						}
					}
				}
				for _, gap := range []int{0, 13781, rate*4 + 317} {
					actual.Reset()
					full.Reset()
					for frame := 0; frame < gap+rate/2; frame++ {
						if frame == 17 || frame == 2371 || frame == gap+177 || frame == gap+16001 {
							automate(frame)
						}
						var l, r float32
						if frame >= gap && frame < gap+113 {
							l = float32(.7 * math.Sin(float64(frame-gap)*.17))
							r = float32(.4 * math.Cos(float64(frame-gap)*.29))
						}
						// Retrigger after the drive's FIR histories have drained,
						// while its tone states and feedback effects still retain tails.
						if frame == gap+3911 {
							l, r = .51, -.38
						}
						forceFull()
						aL, aR := actual.Process(l, r)
						bL, bR := full.Process(l, r)
						if math.Float32bits(aL) != math.Float32bits(bL) || math.Float32bits(aR) != math.Float32bits(bR) {
							t.Fatalf("gap=%d frame=%d actual=(%.9g,%.9g) full=(%.9g,%.9g)", gap, frame, aL, aR, bL, bR)
						}
					}
				}
			})
		}
	}
}

func TestSilentEffectsNeverMistakeAReleaseForIdle(t *testing.T) {
	drive, _ := NewDrive(48000)
	driveParams := drive.Params()
	driveParams.ToneHz = 1000
	if err := drive.SetParams(driveParams); err != nil {
		t.Fatal(err)
	}
	drive.Reset()
	delay, _ := NewDelay(48000, 120000)
	reverb, _ := NewReverb(48000)
	deesser, _ := NewDeesser(48000, DefaultDeesserParams())
	for _, effect := range []interface {
		Processor
		Silent() bool
	}{drive, delay, reverb, deesser} {
		if !effect.Silent() {
			t.Fatalf("%T should start silent", effect)
		}
		effect.Process(.8, -.6)
		if effect.Silent() {
			t.Fatalf("%T lost its excited state", effect)
		}
		for range 2048 {
			effect.Process(0, 0)
		}
		if effect.Silent() {
			t.Fatalf("%T discarded its tail/detector state", effect)
		}
		effect.Reset()
		if !effect.Silent() {
			t.Fatalf("%T reset did not restore silence", effect)
		}
		if allocs := testing.AllocsPerRun(100, func() {
			for range 128 {
				effect.Process(0, 0)
			}
		}); allocs != 0 {
			t.Fatalf("%T silent callback allocations=%g", effect, allocs)
		}
	}
}

func BenchmarkIdleEffects(b *testing.B) {
	for _, kind := range []string{"drive", "delay", "reverb"} {
		for _, full := range []bool{false, true} {
			b.Run(fmt.Sprintf("%s/full=%t", kind, full), func(b *testing.B) {
				var p Processor
				switch kind {
				case "drive":
					p, _ = NewDrive(48000)
				case "delay":
					p, _ = NewDelay(48000, 120000)
				case "reverb":
					p, _ = NewReverb(48000)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if full {
						switch p := p.(type) {
						case *Drive:
							p.zeroFrames = 0
						case *Delay:
							p.excited = true
						case *Reverb:
							p.excited = true
						}
					}
					p.Process(0, 0)
				}
			})
		}
	}
}
