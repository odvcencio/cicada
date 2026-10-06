package organ

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func mustOrgan(t testing.TB, rate int, params Params) *Instrument {
	t.Helper()
	p, err := New(rate, params)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func plain() Params {
	p := DefaultParams()
	p.Drawbars = [9]uint8{0, 0, 8}
	p.Percussion, p.Scanner = PercussionOff, ScannerOff
	p.KeyClick, p.Leakage, p.Crosstalk, p.Drive, p.RotaryMix, p.Gain = 0, 0, 0, 0, 0, 1
	return p
}

func renderEnergy(p *Instrument, frames int) float64 {
	var energy float64
	for range frames {
		l, r := p.NextStereo()
		energy += float64(l)*float64(l) + float64(r)*float64(r)
	}
	return energy / float64(frames*2)
}

func TestParamsAndEvents(t *testing.T) {
	if _, err := New(32000, DefaultParams()); err == nil {
		t.Fatal("accepted unsupported rate")
	}
	bad := []Params{DefaultParams(), DefaultParams(), DefaultParams(), DefaultParams(), DefaultParams()}
	bad[0].Drawbars[0] = 9
	bad[1].Percussion = 3
	bad[2].Scanner = 7
	bad[3].MicSpread = float32(math.NaN())
	bad[4].Gain = 3
	for i, params := range bad {
		if _, err := New(48000, params); err == nil {
			t.Fatalf("accepted invalid params %d", i)
		}
	}
	p := mustOrgan(t, 48000, DefaultParams())
	if p.NoteOn(20, 64) == nil || p.NoteOn(109, 64) == nil || p.NoteOn(60, 128) == nil {
		t.Fatal("accepted invalid note event")
	}
	if p.SetSustain(-1) == nil || p.SetSustain(float32(math.NaN())) == nil {
		t.Fatal("accepted invalid pedal")
	}
	before := p.drawbarTarget
	if p.SetDrawbars([9]uint8{1, 9}) == nil || p.drawbarTarget != before {
		t.Fatal("invalid drawbar update was not atomic")
	}
	_ = p.NoteOn(60, 100)
	_ = p.NoteOn(60, 0)
	if p.heldCount != 0 || p.voices[0].held {
		t.Fatal("zero velocity did not release the key")
	}
	for _, name := range []string{"tonewheel_organ", "organ_jazz", "organ_full", "organ_soft"} {
		params, ok := Patch(name)
		if !ok || validate(params) != nil {
			t.Fatalf("invalid patch %q", name)
		}
	}
	if _, ok := Patch("missing"); ok {
		t.Fatal("unknown patch accepted")
	}
}

func TestKeyboardFiniteAndRateIndependent(t *testing.T) {
	var levels [4]float64
	for index, rate := range []int{44100, 48000, 96000, 192000} {
		p := mustOrgan(t, rate, DefaultParams())
		peak := 0.
		for note := MinNote; note <= MaxNote; note++ {
			p.Reset()
			_ = p.NoteOn(uint8(note), 100)
			for range rate / 80 {
				l, r := p.NextStereo()
				if math.IsNaN(float64(l)) || math.IsInf(float64(l), 0) || math.IsNaN(float64(r)) || math.IsInf(float64(r), 0) {
					t.Fatalf("rate=%d note=%d nonfinite", rate, note)
				}
				peak = max(peak, math.Abs(float64(l)), math.Abs(float64(r)))
			}
		}
		if peak < .01 || peak > 4 {
			t.Fatalf("rate=%d peak=%g", rate, peak)
		}
		q := mustOrgan(t, rate, plain())
		_ = q.NoteOn(60, 100)
		renderEnergy(q, rate/20)
		levels[index] = renderEnergy(q, rate/5)
		t.Logf("rate=%d keyboard_peak=%.6f single_drawbar_rms=%.6f", rate, peak, math.Sqrt(levels[index]))
	}
	for _, level := range levels {
		if math.Abs(10*math.Log10(level/levels[1])) > .05 {
			t.Fatalf("sample-rate gain drift: %v", levels)
		}
	}
}

func TestTonewheelSharingFoldbackAndVelocity(t *testing.T) {
	p := mustOrgan(t, 48000, plain())
	if p.keys[84-MinNote].wheel[8] != p.keys[96-MinNote].wheel[8] {
		t.Fatal("treble foldback does not share the same wheel")
	}
	if p.keys[36-MinNote].wheel[3] != p.keys[48-MinNote].wheel[2] {
		t.Fatal("octave drawbars do not share the same wheel")
	}
	a, b := mustOrgan(t, 48000, plain()), mustOrgan(t, 48000, plain())
	_ = a.NoteOn(60, 1)
	_ = b.NoteOn(60, 127)
	for range 4096 {
		al, ar := a.NextStereo()
		bl, br := b.NextStereo()
		if al != bl || ar != br {
			t.Fatal("tonewheel amplitude depends on strike velocity with click disabled")
		}
	}
	// A coherent sinusoid projection measures the fundamental from PCM.
	_ = p.NoteOn(60, 100)
	renderEnergy(p, 2400)
	pcm := make([]float64, 24000)
	for i := range pcm {
		l, _ := p.NextStereo()
		pcm[i] = float64(l) * (.5 - .5*math.Cos(2*math.Pi*float64(i)/float64(len(pcm)-1)))
	}
	fundamental := 440 * math.Exp2(float64(60-69)/12)
	best, bestCents := 0., 0
	for cents := -5; cents <= 5; cents++ {
		frequency := fundamental * math.Exp2(float64(cents)/1200)
		var re, im float64
		for i, x := range pcm {
			angle := 2 * math.Pi * frequency * float64(i) / 48000
			re += x * math.Cos(angle)
			im += x * math.Sin(angle)
		}
		power := re*re + im*im
		if power > best {
			best, bestCents = power, cents
		}
	}
	if bestCents != 0 {
		t.Fatalf("fundamental pitch error=%d cents", bestCents)
	}
	t.Logf("fundamental=%.6f Hz error=%d cents shared_foldback=true", fundamental, bestCents)
}

func TestPercussionSingleTriggerAndDecay(t *testing.T) {
	for _, fast := range []bool{false, true} {
		params := plain()
		params.Percussion, params.PercussionFast = PercussionThird, fast
		p := mustOrgan(t, 48000, params)
		_ = p.NoteOn(60, 100)
		renderEnergy(p, 4800)
		before := p.percussion
		_ = p.NoteOn(64, 100)
		if p.percussion != before {
			t.Fatal("legato percussion retriggered")
		}
		p.NoteOff(60)
		_ = p.NoteOn(67, 100)
		if p.percussion != before {
			t.Fatal("percussion rearmed with another physical key held")
		}
		p.AllNotesOff()
		_ = p.NoteOn(65, 100)
		if p.percussion != 1 {
			t.Fatal("percussion did not rearm after every key released")
		}
		frames := 0
		for p.percussion > .001 {
			p.NextStereo()
			frames++
		}
		t60 := float64(frames) / 48000
		expected := .5 * math.Log(1000)
		if fast {
			expected = .105 * math.Log(1000)
		}
		if math.Abs(t60-expected) > .002 {
			t.Fatalf("fast=%t percussion T60=%g expected=%g", fast, t60, expected)
		}
		t.Logf("percussion fast=%t amplitude_T60=%.4f s", fast, t60)
	}
	params := plain()
	params.Drawbars, params.Percussion = [9]uint8{0, 0, 0, 0, 0, 0, 0, 0, 8}, PercussionSecond
	p := mustOrgan(t, 48000, params)
	_ = p.NoteOn(60, 100)
	p.percussion = 0
	if renderEnergy(p, 4800) != 0 {
		t.Fatal("1 foot drawbar was not cancelled by percussion")
	}
}

func TestReleaseSustainStealingAndReset(t *testing.T) {
	p := mustOrgan(t, 48000, DefaultParams())
	for i := 0; i < MaxVoices+1; i++ {
		_ = p.NoteOn(uint8(48+i*3), 100)
	}
	if p.heldCount != MaxVoices+1 {
		t.Fatal("stealing changed physical key state")
	}
	count := 0
	for _, v := range p.voices {
		if v.active {
			count++
		}
	}
	if count != MaxVoices {
		t.Fatalf("voice count=%d", count)
	}
	_ = p.SetSustain(1)
	p.AllNotesOff()
	if renderEnergy(p, 24000) < .00001 {
		t.Fatal("sustain failed to keep notes sounding")
	}
	_ = p.SetSustain(0)
	renderEnergy(p, 48000)
	if l, r := p.NextStereo(); l != 0 || r != 0 {
		t.Fatalf("tail did not reach exact silence: %g %g", l, r)
	}
	p.Reset()
	q := mustOrgan(t, 48000, DefaultParams())
	_ = p.NoteOn(60, 100)
	_ = q.NoteOn(60, 100)
	for range 4096 {
		l, r := p.NextStereo()
		ql, qr := q.NextStereo()
		if l != ql || r != qr {
			t.Fatal("Reset did not restore deterministic initial state")
		}
	}
}

func TestScannerAndRotary(t *testing.T) {
	dry := mustOrgan(t, 48000, plain())
	_ = dry.NoteOn(72, 100)
	for _, mode := range []Scanner{V1, V2, V3, C1, C2, C3} {
		params := plain()
		params.Scanner = mode
		p := mustOrgan(t, 48000, params)
		_ = p.NoteOn(72, 100)
		var difference float64
		dry.Reset()
		_ = dry.NoteOn(72, 100)
		for range 4800 {
			l, _ := p.NextStereo()
			dl, _ := dry.NextStereo()
			difference += math.Abs(float64(l - dl))
		}
		if difference < 1 {
			t.Fatalf("scanner mode=%d has no audible effect", mode)
		}
	}
	for _, spread := range []float32{0, 1} {
		params := plain()
		params.RotaryMix, params.MicSpread = 1, spread
		p := mustOrgan(t, 48000, params)
		_ = p.NoteOn(72, 100)
		var side float64
		for range 48000 {
			l, r := p.NextStereo()
			side += float64(l-r) * float64(l-r)
		}
		if spread == 0 && side != 0 || spread == 1 && side < .01 {
			t.Fatalf("spread=%g stereo energy=%g", spread, side)
		}
		p.SetRotaryFast(true)
		renderEnergy(p, 48000)
		hProgress := float64((p.hornSpeed - p.hornSlow) / (p.hornFast - p.hornSlow))
		dProgress := float64((p.drumSpeed - p.drumSlow) / (p.drumFast - p.drumSlow))
		if hProgress < .65 || hProgress > .7 || dProgress < .24 || dProgress > .28 {
			t.Fatalf("motor acceleration horn=%g drum=%g", hProgress, dProgress)
		}
		p.SetRotaryFast(false)
		hBefore, dBefore := p.hornSpeed, p.drumSpeed
		p.NextStereo()
		if p.hornSpeed >= hBefore || p.drumSpeed >= dBefore {
			t.Fatal("motor failed to decelerate")
		}
		t.Logf("spread=%.1f side_energy=%.6f one_second_ramp_horn=%.4f drum=%.4f", spread, side, hProgress, dProgress)
	}
}

func TestAllocationFreeControlsAndRender(t *testing.T) {
	p := mustOrgan(t, 48000, DefaultParams())
	allocations := testing.AllocsPerRun(100, func() {
		p.Reset()
		_ = p.SetVoiceLimit(8)
		_ = p.SetDrawbars([9]uint8{8, 3, 7, 5, 1, 0, 2, 0, 1})
		_ = p.SetSustain(1)
		p.SetRotaryFast(true)
		for i := 0; i < 10; i++ {
			_ = p.NoteOn(uint8(48+i*3), 100)
		}
		for range 128 {
			p.NextStereo()
		}
		p.NoteOff(60)
		p.AllNotesOff()
		_ = p.SetVoiceLimit(1)
	})
	if allocations != 0 {
		t.Fatalf("note/control/render allocations=%g", allocations)
	}
}

func TestVoiceLimit(t *testing.T) {
	p := mustOrgan(t, 48000, DefaultParams())
	if p.voiceLimit != MaxVoices || p.SetVoiceLimit(0) == nil || p.SetVoiceLimit(9) == nil {
		t.Fatal("default or invalid voice limit")
	}
	_ = p.SetVoiceLimit(2)
	for _, note := range []uint8{48, 60, 64} {
		_ = p.NoteOn(note, 100)
	}
	for i, v := range p.voices {
		if v.active != (i < 2) {
			t.Fatalf("voice %d exceeds limit", i)
		}
	}
	if p.heldCount != 3 {
		t.Fatal("voice limit changed physical key state")
	}
	_ = p.SetVoiceLimit(1)
	if p.voices[1].active || p.heldCount != 3 {
		t.Fatal("lower limit failed to clear only excluded voice")
	}
	p.Reset()
	if p.voiceLimit != 1 {
		t.Fatal("Reset changed voice limit")
	}
	_ = p.NoteOn(60, 100)
	_ = p.NoteOn(67, 100)
	if !p.voices[0].active || p.voices[0].note != 67 {
		t.Fatal("single voice stealing failed")
	}
	for _, v := range p.voices[1:] {
		if v.active {
			t.Fatal("single voice limit exceeded")
		}
	}
}

func TestGoldenStereo(t *testing.T) {
	p := mustOrgan(t, 48000, DefaultParams())
	pcm := make([]byte, 16384*8)
	for i := 0; i < 16384; i++ {
		switch i {
		case 0:
			_ = p.NoteOn(48, 100)
			_ = p.NoteOn(60, 100)
			_ = p.NoteOn(67, 100)
		case 2048:
			_ = p.NoteOn(64, 127)
			p.SetRotaryFast(true)
		case 4096:
			p.NoteOff(60)
			_ = p.SetDrawbars([9]uint8{8, 5, 8, 6, 3, 4, 1, 2, 3})
		case 8192:
			p.AllNotesOff()
		}
		l, r := p.NextStereo()
		binary.LittleEndian.PutUint32(pcm[i*8:], math.Float32bits(l))
		binary.LittleEndian.PutUint32(pcm[i*8+4:], math.Float32bits(r))
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(pcm))
	const expected = "e4b010173396f3eaaf2f1cfed1caa8a3a33589c7779e768344a366c9ba33b42b"
	if actual != expected {
		t.Fatalf("stereo golden SHA256=%s", actual)
	}
}

func BenchmarkNextStereo(b *testing.B) {
	for _, count := range []int{1, 8} {
		b.Run(fmt.Sprintf("voices_%d", count), func(b *testing.B) {
			p := mustOrgan(b, 48000, DefaultParams())
			for i := 0; i < count; i++ {
				_ = p.NoteOn(uint8(48+i*3), 100)
			}
			renderEnergy(p, 4800)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p.NextStereo()
			}
		})
	}
}
