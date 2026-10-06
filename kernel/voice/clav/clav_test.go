package clav

import (
	"fmt"
	"math"
	"testing"
)

func TestClavPickupsMuteReleaseAndAllocations(t *testing.T) {
	var hashes [4]uint64
	for pickup := Bridge; pickup <= Difference; pickup++ {
		p := DefaultParams()
		p.Pickup = pickup
		i, err := New(48000, p)
		if err != nil {
			t.Fatal(err)
		}
		_ = i.NoteOn(60, 110)
		var energy float64
		for range 12000 {
			l, r := i.NextStereo()
			if math.IsNaN(float64(l)) || math.IsInf(float64(r), 0) {
				t.Fatal("nonfinite")
			}
			energy += float64(l) * float64(l)
			hashes[pickup] = hashes[pickup]*131 + uint64(math.Float32bits(l))
		}
		if energy < .1 {
			t.Fatal("silent")
		}
		i.AllNotesOff()
		for range 48000 {
			i.NextStereo()
		}
		if i.Active() != 0 {
			t.Fatal("release")
		}
		if got := testing.AllocsPerRun(20, func() {
			for n := 0; n < 12; n++ {
				_ = i.NoteOn(uint8(48+n*3), 100)
			}
			for range 128 {
				i.NextStereo()
			}
			i.AllNotesOff()
			_ = i.SetSustain(1)
			_ = i.SetSustain(0)
		}); got != 0 {
			t.Fatalf("allocations %g", got)
		}
	}
	for j, x := range hashes {
		for k := j + 1; k < len(hashes); k++ {
			if x == hashes[k] {
				t.Fatal("pickups sound identical")
			}
		}
	}
	var e [2]float64
	for j, mute := range []float64{0, 1} {
		p := DefaultParams()
		p.Mute = mute
		i, _ := New(48000, p)
		_ = i.NoteOn(60, 100)
		for frame := 0; frame < 48000; frame++ {
			l, _ := i.NextStereo()
			if frame > 24000 {
				e[j] += float64(l) * float64(l)
			}
		}
	}
	if e[1] >= e[0]*.1 {
		t.Fatalf("mute did not shorten decay: %v", e)
	}
}
func TestClavValidation(t *testing.T) {
	p := DefaultParams()
	for _, rate := range []int{0, 8000, 47999} {
		if _, e := New(rate, p); e == nil {
			t.Fatal("invalid rate")
		}
	}
	for _, x := range []float64{-1, 2, math.NaN(), math.Inf(1)} {
		q := p
		q.Mute = x
		if _, e := New(48000, q); e == nil {
			t.Fatal("invalid mute")
		}
	}
	i, _ := New(48000, p)
	if i.NoteOn(20, 100) == nil || i.NoteOn(60, 0) == nil || i.SetSustain(float32(math.NaN())) == nil {
		t.Fatal("invalid event")
	}
}
func TestClavGolden(t *testing.T) {
	p := DefaultParams()
	i, _ := New(48000, p)
	var hash uint64 = 14695981039346656037
	for frame := 0; frame < 16384; frame++ {
		switch frame {
		case 0:
			_ = i.NoteOn(60, 45)
		case 2048:
			_ = i.NoteOn(67, 120)
		case 8192:
			i.AllNotesOff()
		}
		l, r := i.NextStereo()
		for _, x := range [2]float32{l, r} {
			bits := math.Float32bits(x)
			for range 4 {
				hash ^= uint64(byte(bits))
				hash *= 1099511628211
				bits >>= 8
			}
		}
	}
	const expected uint64 = 0xff65138c0d8de409
	if expected != 0 && hash != expected {
		t.Fatalf("golden changed: %016x", hash)
	}
	t.Logf("clav golden=%016x", hash)
}
func BenchmarkClav(b *testing.B) {
	for _, n := range []int{1, 8} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			p := DefaultParams()
			i, _ := New(48000, p)
			for j := 0; j < n; j++ {
				_ = i.NoteOn(uint8(48+j*3), 100)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				if k%100 == 0 {
					i.Reset()
					for j := 0; j < n; j++ {
						_ = i.NoteOn(uint8(48+j*3), 100)
					}
				}
				for range 128 {
					i.NextStereo()
				}
			}
		})
	}
}

func TestConfiguredVoiceLimit(t *testing.T) {
	i, _ := New(48000, DefaultParams())
	if i.SetVoiceLimit(0) == nil || i.SetVoiceLimit(9) == nil {
		t.Fatal("invalid voice limit accepted")
	}
	for _, limit := range []int{1, 2, 4, 8} {
		i.Reset()
		_ = i.SetVoiceLimit(limit)
		for n := 0; n < 12; n++ {
			_ = i.NoteOn(uint8(36+n*3), 100)
		}
		if i.Active() != limit {
			t.Fatalf("active=%d limit=%d", i.Active(), limit)
		}
		i.Reset()
		for n := 0; n < 12; n++ {
			_ = i.NoteOn(uint8(36+n*3), 100)
		}
		if i.Active() != limit {
			t.Fatal("reset lost voice limit")
		}
	}
}
