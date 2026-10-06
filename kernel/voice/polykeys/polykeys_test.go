package polykeys

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"testing"
)

func TestValidation(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		if _, err := New(rate, DefaultParams()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := New(32000, DefaultParams()); err == nil {
		t.Fatal("accepted unsupported rate")
	}
	for _, change := range []func(*Params){
		func(p *Params) { p.Saw = math.NaN() }, func(p *Params) { p.Release = math.Inf(1) },
		func(p *Params) { p.Attack = 0 }, func(p *Params) { p.Sync = .5 },
		func(p *Params) { p.Filter = 2 }, func(p *Params) { p.PulseWidth = 1 },
		func(p *Params) { p.Resonance = 1 }, func(p *Params) { p.Saw, p.Pulse = 0, 0 },
	} {
		p := DefaultParams()
		change(&p)
		if _, err := New(48000, p); err == nil {
			t.Fatal("accepted invalid parameters", p)
		}
	}
	i, _ := New(48000, DefaultParams())
	for _, n := range []uint8{0, 20, 109, 255} {
		if i.NoteOn(n, 100) == nil {
			t.Fatal("accepted note", n)
		}
	}
	if i.NoteOn(60, 128) == nil {
		t.Fatal("accepted velocity 128")
	}
	for _, pedal := range []float32{-1, 2, float32(math.NaN()), float32(math.Inf(1))} {
		if i.SetSustain(pedal) == nil {
			t.Fatal("accepted pedal", pedal)
		}
	}
	if _, err := Patch("factory"); err == nil {
		t.Fatal("accepted unknown patch")
	}
}

func TestPatchesStayFiniteAndRelease(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		for _, name := range []string{"poly_keys", "brass_stab", "soft_pad", "sync_lead"} {
			p, _ := Patch(name)
			i, err := New(rate, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, note := range []uint8{21, 36, 48, 60, 72, 84, 96, 108} {
				_ = i.NoteOn(note, 127)
			}
			var power float64
			for range rate / 4 {
				l, r := i.NextStereo()
				if l != l || r != r || math.Abs(float64(l)) > 8 || math.Abs(float64(r)) > 8 {
					t.Fatalf("%s rate=%d unstable (%g,%g)", name, rate, l, r)
				}
				power += float64(l*l + r*r)
			}
			if power < 1e-5 {
				t.Fatal(name, "silent patch")
			}
			i.AllNotesOff()
			for range int(float64(rate) * (p.Release*3.2 + .05)) {
				i.NextStereo()
			}
			if i.Active() {
				t.Fatalf("%s rate=%d did not release", name, rate)
			}
		}
	}
}

func TestSustainVelocityAndStealing(t *testing.T) {
	p := DefaultParams()
	p.Chorus = 0
	energy := func(velocity uint8) float64 {
		i, _ := New(48000, p)
		_ = i.NoteOn(60, velocity)
		var sum float64
		for range 12000 {
			l, r := i.NextStereo()
			sum += float64(l*l + r*r)
		}
		return sum
	}
	if energy(120) < energy(30)*2 {
		t.Fatal("velocity did not change level and filter envelope")
	}
	i, _ := New(48000, p)
	_ = i.NoteOn(60, 100)
	_ = i.SetSustain(1)
	i.NoteOff(60)
	if i.voices[0].stage == 3 {
		t.Fatal("sustain failed to hold released key")
	}
	_ = i.SetSustain(0)
	if i.voices[0].stage != 3 {
		t.Fatal("pedal up did not release key")
	}
	for n := uint8(40); n < 49; n++ {
		_ = i.NoteOn(n, 100)
	}
	for _, v := range i.voices {
		if v.stage != 0 && (v.note == 60 || v.note == 40) {
			t.Fatal("voice stealing did not prefer released/oldest voice")
		}
	}
	_ = i.NoteOn(48, 0)
	for _, v := range i.voices {
		if v.note == 48 && v.held {
			t.Fatal("velocity zero did not release key")
		}
	}
}

func TestAllocationFree(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	if allocations := testing.AllocsPerRun(50, func() {
		_ = i.SetVoiceLimit(8)
		for n := uint8(48); n < 60; n++ {
			_ = i.NoteOn(n, 100)
		}
		_ = i.SetSustain(1)
		i.NoteOff(60)
		for range 128 {
			i.NextStereo()
		}
		i.AllNotesOff()
	}); allocations != 0 {
		t.Fatalf("trigger/render allocated %g times", allocations)
	}
}

func TestHardSyncResetsSlaveAtFractionalMasterWrap(t *testing.T) {
	p, _ := Patch("sync_lead")
	i, _ := New(48000, p)
	v := voice{phase: .99, phaseB: .4, deltaB: .055}
	_ = i.oscillator(&v, .02, .5)
	if math.Abs(float64(v.phaseB-.0275)) > 1e-6 || v.syncCorrection == 0 {
		t.Fatalf("slave phase=%g correction=%g", v.phaseB, v.syncCorrection)
	}
}

func TestPolyBLEPReducesAliasedEnergy(t *testing.T) {
	const frames, bin = 2048, 383
	delta := float32(bin) / frames
	aliasEnergy := func(corrected bool) float64 {
		var energy float64
		for k := 1; k < frames/2; k++ {
			if k == bin || k == 2*bin {
				continue
			}
			var re, im float64
			for n := 0; n < frames; n++ {
				phase := wrap(float32(n) * delta)
				x := float32(2*phase - 1)
				if corrected {
					x = saw(phase, delta)
				}
				angle := 2 * math.Pi * float64(k*n) / frames
				re += float64(x) * math.Cos(angle)
				im += float64(x) * math.Sin(angle)
			}
			energy += re*re + im*im
		}
		return energy
	}
	naive, corrected := aliasEnergy(false), aliasEnergy(true)
	reduction := 10 * math.Log10(naive/corrected)
	if reduction < 15 {
		t.Fatalf("alias reduction only %.2f dB", reduction)
	}
	t.Logf("saw frequency=%.1fHz alias energy reduction=%.2fdB", 48000*float64(delta), reduction)
}

func TestFilterCutoffCalibration(t *testing.T) {
	for _, model := range []Filter{Ladder, StateVariable} {
		p := DefaultParams()
		p.Filter, p.Cutoff, p.FilterEnv, p.KeyTrack, p.Resonance, p.Drive = model, 1000, 0, 0, 0, 0
		i, _ := New(48000, p)
		idx := int(i.cutoffIndexes[60-21])
		measure := func(hz float64) float64 {
			var v voice
			var sum float64
			for n := 0; n < 19200; n++ {
				x := float32(.001 * math.Sin(2*math.Pi*hz*float64(n)/96000))
				y := i.filter(&v, x, i.table[idx])
				if n >= 9600 {
					sum += float64(y * y)
				}
			}
			return math.Sqrt(sum / 9600)
		}
		ratio := measure(1000) / measure(100)
		if ratio < .45 || ratio > .8 {
			t.Fatalf("model=%d cutoff/low-frequency ratio=%g", model, ratio)
		}
		t.Logf("filter=%d cutoff/100Hz=%.2fdB", model, 20*math.Log10(ratio))
	}
}

func TestGolden(t *testing.T) {
	want := map[string]uint64{"poly_keys": 0x77d2a358a7f04147, "brass_stab": 0x7ebb45876297e216, "soft_pad": 0x636952cfadf0c656, "sync_lead": 0x2fea6f1122af6275}
	for _, name := range []string{"poly_keys", "brass_stab", "soft_pad", "sync_lead"} {
		p, _ := Patch(name)
		i, _ := New(48000, p)
		h := fnv.New64a()
		var data [8]byte
		for frame := 0; frame < 16384; frame++ {
			switch frame {
			case 0:
				_ = i.NoteOn(48, 76)
				_ = i.NoteOn(60, 105)
			case 2048:
				_ = i.SetSustain(1)
				i.NoteOff(48)
			case 4096:
				for n := uint8(40); n < 50; n++ {
					_ = i.NoteOn(n, 100)
				}
			case 8192:
				i.AllNotesOff()
			}
			l, r := i.NextStereo()
			binary.LittleEndian.PutUint32(data[:4], math.Float32bits(l))
			binary.LittleEndian.PutUint32(data[4:], math.Float32bits(r))
			_, _ = h.Write(data[:])
		}
		if h.Sum64() != want[name] {
			t.Errorf("%s golden=%016x want=%016x", name, h.Sum64(), want[name])
		}
	}
}

func TestReset(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	_ = i.NoteOn(60, 100)
	for range 2048 {
		i.NextStereo()
	}
	i.Reset()
	for range 512 {
		l, r := i.NextStereo()
		if l != 0 || r != 0 || i.Active() {
			t.Fatal("reset left a note or chorus tail")
		}
	}
	i.Reset()
	fresh, _ := New(48000, DefaultParams())
	_ = i.NoteOn(60, 100)
	_ = fresh.NoteOn(60, 100)
	for range 4096 {
		a, b := i.NextStereo()
		c, d := fresh.NextStereo()
		if a != c || b != d {
			t.Fatal("reset did not restore initial phase state")
		}
	}
}

func TestExtremeFilterAndSyncStability(t *testing.T) {
	for _, model := range []Filter{Ladder, StateVariable} {
		for _, rate := range []int{44100, 48000, 96000, 192000} {
			p := DefaultParams()
			p.Cutoff, p.Resonance, p.FilterEnv, p.Drive, p.Filter = 18000, .95, 6, 1, model
			p.Sync, p.Drift, p.PWM, p.Saw, p.Pulse, p.Sub = 8, 10, .35, 1, 1, 1
			i, _ := New(rate, p)
			for n := uint8(101); n <= 108; n++ {
				_ = i.NoteOn(n, 127)
			}
			for range 12000 {
				l, r := i.NextStereo()
				if l != l || r != r || math.Abs(float64(l)) > 8 || math.Abs(float64(r)) > 8 {
					t.Fatalf("model=%d rate=%d output=(%g,%g)", model, rate, l, r)
				}
			}
		}
	}
}

func BenchmarkPolyKeys(b *testing.B) {
	for _, patch := range []string{"poly_keys", "brass_stab", "soft_pad", "sync_lead"} {
		for _, count := range []int{1, 8} {
			name := "one_voice"
			if count == 8 {
				name = "eight_voices"
			}
			b.Run(patch+"/"+name, func(b *testing.B) {
				p, _ := Patch(patch)
				i, _ := New(48000, p)
				for n := 0; n < count; n++ {
					_ = i.NoteOn(uint8(48+n*3), 100)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					for range 128 {
						i.NextStereo()
					}
				}
			})
		}
	}
}

func TestChorusRingBoundary(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	i.chorusBase, i.chorusDepth = 1e-7, 0
	// A delay just below zero rounds to exactly the ring length after wrapping.
	// Rendering must wrap that rounded index too.
	i.NextStereo()
}

func TestVoiceLimit(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	if i.voiceLimit != MaxVoices {
		t.Fatal("default polyphony is not eight")
	}
	for _, invalid := range []int{-1, 0, 9} {
		if i.SetVoiceLimit(invalid) == nil {
			t.Fatal("accepted voice limit", invalid)
		}
	}
	for n := uint8(40); n < 48; n++ {
		_ = i.NoteOn(n, 100)
	}
	_ = i.SetVoiceLimit(2)
	for _, v := range i.voices[2:] {
		if v.stage != 0 {
			t.Fatal("reducing polyphony left a voice outside the limit")
		}
	}
	for limit := 1; limit <= MaxVoices; limit++ {
		_ = i.SetVoiceLimit(limit)
		i.Reset()
		if i.voiceLimit != limit {
			t.Fatal("reset changed voice limit")
		}
		for n := uint8(40); n < 56; n++ {
			_ = i.NoteOn(n, 100)
			if n%2 == 0 {
				i.NoteOff(n)
			}
			active := 0
			for _, v := range i.voices {
				if v.stage != 0 {
					active++
				}
			}
			if active > limit {
				t.Fatalf("limit=%d active=%d", limit, active)
			}
			i.NextStereo()
		}
	}
}

func TestSustainedAsymmetricPulseHasNoDC(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		for _, filter := range []Filter{Ladder, StateVariable} {
			p := DefaultParams()
			p.Saw, p.Pulse, p.PulseWidth, p.PWM, p.Sub = 0, 1, .15, 0, 0
			p.Detune, p.Drift, p.FilterEnv = 0, 0, 0
			p.Attack, p.Sustain, p.Drive, p.Chorus = .01, 1, .8, .9
			p.Filter = filter
			i, _ := New(rate, p)
			_ = i.NoteOn(69, 127)
			for range rate {
				i.NextStereo()
			}
			var sum, power, uncoupled [2]float64
			for range rate * 4 {
				l, r := i.NextStereo()
				for c, x := range [...]float32{l, r} {
					sum[c] += float64(x)
					power[c] += float64(x) * float64(x)
					uncoupled[c] += float64(i.dcInput[c])
				}
			}
			for c := range sum {
				mean, before := sum[c]/float64(rate*4), uncoupled[c]/float64(rate*4)
				rms := math.Sqrt(power[c] / float64(rate*4))
				if math.Abs(before) < .005 {
					t.Fatalf("rate=%d filter=%d test did not exercise pulse bias: %g", rate, filter, before)
				}
				if math.Abs(mean) > rms*.001 {
					t.Fatalf("rate=%d filter=%d channel=%d mean=%g RMS=%g", rate, filter, c, mean, rms)
				}
				t.Logf("rate=%d filter=%d channel=%d output DC/RMS=%.2fdB", rate, filter, c, 20*math.Log10(math.Abs(mean)/rms))
			}
		}
	}
}

func TestChangingPadChordsHaveNoIntegratedDC(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000, 192000} {
		p, _ := Patch("soft_pad")
		i, _ := New(rate, p)
		var sum, power [2]float64
		chords := [...][3]uint8{{48, 55, 60}, {53, 57, 65}, {43, 50, 59}, {48, 55, 64}}
		for frame := range rate * 12 {
			if frame%rate == 0 && frame < rate*4 {
				i.AllNotesOff()
				for _, note := range chords[frame/rate] {
					_ = i.NoteOn(note, uint8(70+frame/rate*15))
				}
			}
			if frame == rate*4 {
				i.AllNotesOff()
			}
			l, r := i.NextStereo()
			for c, x := range [...]float32{l, r} {
				sum[c] += float64(x)
				power[c] += float64(x) * float64(x)
			}
		}
		for c := range sum {
			mean := sum[c] / float64(rate*12)
			rms := math.Sqrt(power[c] / float64(rate*12))
			if math.Abs(mean) > rms*.0001 {
				t.Fatalf("rate=%d channel=%d chord mean=%g RMS=%g", rate, c, mean, rms)
			}
			t.Logf("rate=%d channel=%d integrated DC/RMS=%.2fdB", rate, c, 20*math.Log10(math.Abs(mean)/rms))
		}
		i.Reset()
		if i.dcInput != [2]float32{} || i.dcOutput != [2]float32{} {
			t.Fatal("reset retained coupling history")
		}
	}
}
