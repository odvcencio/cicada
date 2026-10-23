package stream

import (
	"sync/atomic"
	"testing"
)

func BenchmarkSharedResidentPage(b *testing.B) {
	c, err := New(Config{Pages: 3, Readers: 1, AheadPages: 0}, []Asset{{ID: 1, Frames: PageFrames * 2, RateHz: 48000, Channels: 2}})
	if err != nil {
		b.Fatal(err)
	}
	r, _ := c.NewReader(1)
	r.SeekFrame(0, 1)
	prime(c)
	var misses atomic.Int64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			p := c.acquire(1, 0)
			if p == nil {
				misses.Add(1)
			} else {
				p.state.Add(^uint32(pin - 1))
			}
		}
	})
	b.ReportMetric(float64(misses.Load()), "misses")
}
