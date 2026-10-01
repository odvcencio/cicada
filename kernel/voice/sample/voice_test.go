package sample

import (
	"math"
	"reflect"
	"testing"
)

func testRegion(length int, stereo, loop bool) Region {
	r := Region{Left: make([]float32, length), SampleRate: 48000, RootKey: 60, End: length, Loop: loop, LoopEnd: length}
	if stereo {
		r.Right = make([]float32, length)
	}
	for i := range r.Left {
		r.Left[i] = float32(.5 * math.Sin(float64(i)*.037))
		if stereo {
			r.Right[i] = float32(.3 * math.Cos(float64(i)*.071))
		}
	}
	return r
}

func testVoice(t *testing.T, r Region) *Voice {
	t.Helper()
	v, err := New(48000, r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func trigger(t *testing.T, v *Voice, note, velocity uint8) {
	t.Helper()
	if err := v.NoteOn(note, velocity); err != nil {
		t.Fatal(err)
	}
}

func TestSampleUnityPathIsBitExact(t *testing.T) {
	for _, stereo := range []bool{false, true} {
		r := testRegion(513, stereo, false)
		r.Start = 3
		r.Left[3] = math.Float32frombits(0x80000000) // Preserve negative zero.
		r.Left[4] = math.SmallestNonzeroFloat32
		r.Left[5] = math.Float32frombits(0x3f800001)
		v := testVoice(t, r)
		trigger(t, v, 60, 127)
		left, right := make([]float32, r.End-r.Start), make([]float32, r.End-r.Start)
		v.Render(left, right)
		for i, got := range left {
			wantR := r.Left[r.Start+i]
			if stereo {
				wantR = r.Right[r.Start+i]
			}
			if math.Float32bits(got) != math.Float32bits(r.Left[r.Start+i]) || math.Float32bits(right[i]) != math.Float32bits(wantR) {
				t.Fatalf("stereo=%v frame=%d: unity PCM changed", stereo, i)
			}
		}
		if v.KernelTaps() != 0 || v.Ratio() != 1 {
			t.Fatal("unity selected a sinc kernel")
		}
	}
}

func TestSampleLoopWrapsWithoutGap(t *testing.T) {
	r := Region{Left: []float32{9, 8, 1, 2, 3, 7}, Right: []float32{-9, -8, -1, -2, -3, -7}, SampleRate: 48000, RootKey: 60, Start: 1, End: 6, Loop: true, LoopStart: 2, LoopEnd: 5}
	v := testVoice(t, r)
	trigger(t, v, 60, 127)
	var left, right [13]float32
	v.Render(left[:], right[:])
	want := []float32{8, 1, 2, 3, 1, 2, 3, 1, 2, 3, 1, 2, 3}
	for i := range want {
		if left[i] != want[i] || right[i] != -want[i] {
			t.Fatalf("frame %d: got %g/%g want %g/%g", i, left[i], right[i], want[i], -want[i])
		}
	}
	// A one-frame loop must wrap even when one advance skips several cycles.
	r = testRegion(1, true, true)
	r.Left[0], r.Right[0] = .25, -.5
	v = testVoice(t, r)
	trigger(t, v, 84, 127)
	v.Render(left[:], right[:])
	for i := range left {
		if math.Abs(float64(left[i])-.25) > 1e-6 || math.Abs(float64(right[i])+.5) > 1e-6 {
			t.Fatalf("short SRC loop has a gap at %d: %g/%g", i, left[i], right[i])
		}
	}
}

func TestSampleStealOrderIsDeterministic(t *testing.T) {
	r := testRegion(1024, false, true)
	for repeat := 0; repeat < 10; repeat++ {
		p, err := NewPool(48000, 3, r)
		if err != nil {
			t.Fatal(err)
		}
		var handles [3]Handle
		for i := range handles {
			handles[i], err = p.NoteOn(60, 127)
			if err != nil || handles[i].Slot != i {
				t.Fatalf("free slot order: %v, %v", handles[i], err)
			}
		}
		oldest, _ := p.NoteOn(60, 127)
		if oldest.Slot != 0 || p.NoteOff(handles[0]) {
			t.Fatal("oldest order or stale handle changed")
		}
		p.NoteOff(handles[2])
		for i := 0; i < 32; i++ {
			var l, r [1]float32
			p.Render(l[:], r[:])
		}
		p.NoteOff(handles[1])
		quietest, _ := p.NoteOn(60, 127)
		if quietest.Slot != 2 {
			t.Fatalf("quietest release: got slot %d", quietest.Slot)
		}
		p.Reset()
		for i := range handles {
			handles[i], _ = p.NoteOn(60, 100)
			p.NoteOff(handles[i])
		}
		tie, _ := p.NoteOn(60, 127)
		if tie.Slot != 0 {
			t.Fatalf("release tie chose slot %d", tie.Slot)
		}
		// Identical birth IDs explicitly exercise the fallback index tie.
		p.Reset()
		for i := range handles {
			_, _ = p.NoteOn(60, 127)
			p.ids[i] = 1
		}
		tie, _ = p.NoteOn(60, 127)
		if tie.Slot != 0 {
			t.Fatalf("age tie chose slot %d", tie.Slot)
		}
	}
}

func TestSampleRenderDoesNotAllocate(t *testing.T) {
	r := testRegion(2048, true, true)
	var left, right [128]float32
	for _, note := range []uint8{36, 60, 67, 84, 96} {
		v := testVoice(t, r)
		trigger(t, v, note, 127)
		if n := testing.AllocsPerRun(100, func() { v.Render(left[:], right[:]) }); n != 0 {
			t.Fatalf("note %d: Voice.Render allocated %g", note, n)
		}
	}
	p, err := NewPool(48000, 64, r)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 64; i++ {
		if _, err := p.NoteOn(67, 127); err != nil {
			t.Fatal(err)
		}
	}
	if n := testing.AllocsPerRun(100, func() { p.Render(left[:], right[:]) }); n != 0 {
		t.Fatalf("Pool.Render allocated %g", n)
	}
	if n := testing.AllocsPerRun(100, func() {
		h, err := p.NoteOn(67, 80)
		if err != nil {
			panic(err)
		}
		p.NoteOff(h)
		p.Render(left[:], right[:])
	}); n != 0 {
		t.Fatalf("steal/release/Render allocated %g", n)
	}
}

func TestSampleDeterministicAcrossBlockSizes(t *testing.T) {
	for _, note := range []uint8{36, 60, 67, 84, 96} {
		var referenceL, referenceR []float32
		for _, blockSize := range []int{64, 128, 256} {
			p, err := NewPool(48000, 3, testRegion(911, true, true))
			if err != nil {
				t.Fatal(err)
			}
			first, _ := p.NoteOn(note, 127)
			_, _ = p.NoteOn(note-1, 93)
			left, right := make([]float32, 4096), make([]float32, 4096)
			for frame := 0; frame < len(left); frame += blockSize {
				if frame == 1024 {
					p.NoteOff(first)
					if err := p.voices[1].SetGainTarget(.7, .01); err != nil {
						t.Fatal(err)
					}
				}
				if frame == 2048 {
					if _, err := p.NoteOn(note-2, 81); err != nil {
						t.Fatal(err)
					}
					if _, err := p.NoteOn(note, 111); err != nil {
						t.Fatal(err)
					}
				}
				p.Render(left[frame:frame+blockSize], right[frame:frame+blockSize])
			}
			if referenceL == nil {
				referenceL, referenceR = left, right
			} else if !reflect.DeepEqual(referenceL, left) || !reflect.DeepEqual(referenceR, right) {
				t.Fatalf("note=%d block=%d changed PCM", note, blockSize)
			}
		}
	}
}

func TestSampleReleaseVelocityAndValidation(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		r := testRegion(4096, false, true)
		r.SampleRate = rate
		for i := range r.Left {
			r.Left[i] = 1
		}
		v, err := New(rate, r)
		if err != nil {
			t.Fatal(err)
		}
		trigger(t, v, 60, 64)
		v.NoteOff()
		for i := 0; i < rate/500; i++ {
			l, _ := v.NextStereo()
			want := (64.0 / 127) * float64(rate/500-i) / float64(rate/500)
			if math.Abs(float64(l)-want) > 1e-7 {
				t.Fatalf("rate=%d release frame=%d: %g want %g", rate, i, l, want)
			}
			v.NoteOff() // Repeated note-off must not extend the release.
		}
		if v.Active() {
			t.Fatal("release exceeded 2 ms")
		}
	}
	r := testRegion(1, false, false)
	r.Left[0] = .5
	v := testVoice(t, r)
	trigger(t, v, 60, 127)
	if l, _ := v.NextStereo(); l != .5 {
		t.Fatal("one-frame source changed")
	}
	last := float32(.5)
	for v.Active() {
		l, _ := v.NextStereo()
		if l > last || l < 0 {
			t.Fatal("natural end did not fade monotonically")
		}
		last = l
	}
	if _, err := New(123, r); err == nil {
		t.Fatal("invalid output rate accepted")
	}
	if _, err := NewPool(48000, 65, r); err == nil {
		t.Fatal("unbounded pool accepted")
	}
	for _, bad := range []Region{{}, {Left: r.Left, End: 2, SampleRate: 48000}, {Left: r.Left, End: 1, SampleRate: 48000, Loop: true, LoopEnd: 2}} {
		if _, err := New(48000, bad); err == nil {
			t.Fatal("invalid region accepted")
		}
	}
	for _, bad := range []Params{{Gain: math.NaN()}, {Gain: -1}, {Gain: 1, FineTuneCents: math.Inf(1)}} {
		if err := v.SetParams(bad); err == nil {
			t.Fatal("invalid parameters accepted")
		}
	}
	if err := v.NoteOn(0, 127); err == nil {
		t.Fatal("unsupported ratio accepted")
	}
	// Ending the source during an existing release must continue the fade
	// rather than multiplying the already faded last output by it again.
	r = testRegion(10, false, false)
	for i := range r.Left {
		r.Left[i] = .5
	}
	v = testVoice(t, r)
	trigger(t, v, 60, 127)
	v.NoteOff()
	for i := 0; i < v.fadeFrames; i++ {
		l, _ := v.NextStereo()
		want := .5 * float64(v.fadeFrames-i) / float64(v.fadeFrames)
		if math.Abs(float64(l)-want) > 1e-7 {
			t.Fatalf("source ended during release at frame %d: %g want %g", i, l, want)
		}
	}
}
