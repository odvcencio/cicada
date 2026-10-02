package stream

import (
	"math"
	"testing"
)

const hourFrames = int64(48000 * 3600)

func newTestCache(t *testing.T, readers, ahead int, frames int64) (*Cache, []*Reader) {
	t.Helper()
	c, err := New(Config{Pages: readers * (ahead + 3), Readers: readers, AheadPages: ahead}, []Asset{{ID: 1, Frames: frames, RateHz: 48000, Channels: 2}})
	if err != nil {
		t.Fatal(err)
	}
	r := make([]*Reader, readers)
	for i := range r {
		r[i], err = c.NewReader(1)
		if err != nil {
			t.Fatal(err)
		}
	}
	return c, r
}

func pcm(frame int64) float32 { return float32(frame%251-125) / 256 }

func fill(work Work) {
	left, right := work.Left(), work.Right()
	for i := range left {
		left[i], right[i] = pcm(work.Start+int64(i)), -pcm(work.Start+int64(i))
	}
}

func prime(c *Cache) {
	for {
		work, ok := c.Next()
		if !ok {
			return
		}
		fill(work)
		work.Publish()
	}
}

func TestGuardsExactForwardReverseAndTail(t *testing.T) {
	frames := int64(PageFrames*3 + 17)
	c, rs := newTestCache(t, 1, 1, frames)
	r := rs[0]
	for _, direction := range []int{1, -1} {
		start := int64(PageFrames - 7)
		if direction < 0 {
			start = frames - 1
		}
		if err := r.SeekFrame(start, direction); err != nil {
			t.Fatal(err)
		}
		prime(c)
		// Explicitly read both guards, including frames in neighboring pages.
		for frame := start - 10; frame <= start+10; frame++ {
			l, rr, ok := r.ReadFrame(frame)
			want := float32(0)
			if frame >= 0 && frame < frames {
				want = pcm(frame)
			}
			if !ok || l != want || rr != -want {
				t.Fatalf("guard frame %d: %g/%g ready=%v", frame, l, rr, ok)
			}
		}
		var left, right [128]float32
		for block := 0; block < 40; block++ {
			prime(c)
			position := r.Position()
			r.Render(left[:], right[:])
			for i, l := range left {
				frame := position + int64(i*direction)
				if frame < 0 || frame >= frames {
					break
				}
				if l != pcm(frame) || right[i] != -l {
					t.Fatalf("direction %d frame %d: %g != %g", direction, frame, l, pcm(frame))
				}
			}
		}
	}
	if r.Stats().MissingFrames != 0 {
		t.Fatal(r.Stats())
	}
}

func TestPinnedPageSurvivesEvictionAndRetirement(t *testing.T) {
	c, rs := newTestCache(t, 2, 0, hourFrames)
	a, b := rs[0], rs[1]
	a.SeekFrame(32*PageFrames, 1)
	b.SeekFrame(32*PageFrames, 1)
	prime(c)
	a.ReadFrame(32 * PageFrames)
	held := a.page
	// The worker cannot reclaim a pinned page even after its intent retires.
	a.intent.low.Store(1)
	a.intent.high.Store(0)
	for i := 40; i < 140; i++ {
		b.SeekFrame(int64(i*PageFrames), 1)
		prime(c)
	}
	if held.state.Load() < ready+pin || held.index != 32 || held.left[GuardFrames] != pcm(32*PageFrames) {
		t.Fatal("worker changed pinned PCM")
	}
	a.Stop()
	b.Stop()
	if held.state.Load() != ready {
		t.Fatal("retirement leaked page pin")
	}
	b.SeekFrame(300*PageFrames, 1)
	prime(c)
	if c.Stats().Evicted == 0 {
		t.Fatal("eviction was not exercised")
	}
}

func TestSeekStormCoalescesAndRejectsStaleCompletion(t *testing.T) {
	c, rs := newTestCache(t, 1, 3, hourFrames)
	r := rs[0]
	r.SeekFrame(0, 1)
	old, ok := c.Next()
	if !ok {
		t.Fatal("no initial work")
	}
	for i := int64(1); i <= 10000; i++ {
		r.SeekFrame((i*7919)%(hourFrames-PageFrames), 1)
	}
	fill(old)
	if old.Wanted() || old.Publish() {
		t.Fatal("obsolete seek published")
	}
	latest := r.Position() / PageFrames
	work, ok := c.Next()
	if !ok || work.Index != latest {
		t.Fatalf("latest current page lost: %+v", work)
	}
	fill(work)
	work.Publish()
	prime(c)
	if !r.WindowReady() || len(c.pages) != 6 || c.Stats().Cancelled != 1 {
		t.Fatal("seek storm exceeded fixed arena or failed to prime", c.Stats())
	}
}

func TestMissFadesAndRecoversAtAdvancingPosition(t *testing.T) {
	c, rs := newTestCache(t, 1, 0, hourFrames)
	r := rs[0]
	r.SeekFrame(100, 1)
	prime(c)
	var left, right [128]float32
	r.Render(left[:], right[:])
	last := left[127]
	r.SeekFrame(900*PageFrames, 1)
	r.Render(left[:], right[:])
	for i := 0; i < r.fadeFrames; i++ {
		want := last * float32(r.fadeFrames-i-1) / float32(r.fadeFrames)
		if math.Abs(float64(left[i]-want)) > 1e-7 {
			t.Fatalf("fade frame %d: %g != %g", i, left[i], want)
		}
	}
	if left[127] != 0 || r.Position() != 900*PageFrames+128 {
		t.Fatal("miss blocked advancement or failed to silence")
	}
	prime(c)
	r.Render(left[:], right[:])
	for i := r.fadeFrames; i < len(left); i++ {
		if left[i] != pcm(900*PageFrames+128+int64(i)) {
			t.Fatal("recovery replayed stale frames")
		}
	}
	if s := r.Stats(); s.Misses != 1 || s.MissingFrames != 128 || s.Recoveries != 1 {
		t.Fatal(s)
	}
}

func TestHourLongPlaybackBoundedMemory(t *testing.T) {
	c, rs := newTestCache(t, 1, 3, hourFrames)
	r := rs[0]
	r.SeekFrame(0, 1)
	var left, right [PageFrames]float32
	before := c.Stats().ArenaBytes
	for position := int64(0); position < hourFrames; position += PageFrames {
		prime(c)
		size := int(min(int64(PageFrames), hourFrames-position))
		r.Render(left[:size], right[:size])
		for i, l := range left[:size] {
			if l != pcm(position+int64(i)) || right[i] != -l {
				t.Fatalf("hour fixture frame %d differed", position+int64(i))
			}
		}
	}
	r.Stop()
	if c.Stats().ArenaBytes != before || r.Stats().MissingFrames != 0 || c.Stats().Evicted < 40000 {
		t.Fatal(c.Stats(), r.Stats())
	}
	t.Logf("hour stereo 48kHz: resident baseline=%d arena=%d frames=%d evictions=%d misses=%d", hourFrames*8, before, hourFrames, c.Stats().Evicted, r.Stats().Misses)
}

func TestStreamRenderSeekMissAndRecoveryDoNotAllocate(t *testing.T) {
	c, rs := newTestCache(t, 1, 3, hourFrames)
	r := rs[0]
	var left, right [128]float32
	r.SeekFrame(0, 1)
	prime(c)
	if allocs := testing.AllocsPerRun(1000, func() {
		r.SeekFrame(0, 1)
		r.Render(left[:], right[:])
		r.SeekFrame(hourFrames/2, 1)
		r.Render(left[:], right[:])
		r.Stop()
	}); allocs != 0 {
		t.Fatalf("audio callback allocates: %g", allocs)
	}
	// The recovery path also reaches its fade while pages are ready.
	r.SeekFrame(hourFrames/2, 1)
	prime(c)
	if allocs := testing.AllocsPerRun(1000, func() {
		r.SeekFrame(PageFrames*100, 1)
		r.Render(left[:], right[:])
		r.SeekFrame(hourFrames/2, 1)
		r.Render(left[:], right[:])
	}); allocs != 0 {
		t.Fatalf("recovery allocates: %g", allocs)
	}
}

func TestAdmissionAndSharedPageOwnership(t *testing.T) {
	if _, err := New(Config{Pages: 5, Readers: 1, AheadPages: 3}, []Asset{{ID: 1, Frames: hourFrames, RateHz: 48000, Channels: 2}}); err == nil {
		t.Fatal("undersized arena admitted")
	}
	c, rs := newTestCache(t, 2, 1, hourFrames)
	for _, r := range rs {
		r.SeekFrame(PageFrames, 1)
	}
	prime(c)
	for _, r := range rs {
		r.ReadFrame(PageFrames)
	}
	if rs[0].page != rs[1].page || rs[0].page.state.Load() != ready+2*pin {
		t.Fatal("shared page ownership failed")
	}
	rs[0].Stop()
	if rs[1].page.state.Load() != ready+pin {
		t.Fatal("one owner released another owner's pin")
	}
	rs[1].Stop()
	if _, err := c.NewReader(1); err == nil {
		t.Fatal("unbounded reader registration")
	}
}
