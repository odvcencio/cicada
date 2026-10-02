package stream

import "testing"

// A filled 76 MiB arena exercises miss cost at workstation cache sizes, rather
// than only at the six-page test fixture's size. Storage is deliberately absent.
func BenchmarkStreamCacheMiss(b *testing.B) {
	c, err := New(Config{Pages: 2048, Readers: 1, AheadPages: 3}, []Asset{{ID: 1, Frames: hourFrames, RateHz: 48000, Channels: 2}})
	if err != nil {
		b.Fatal(err)
	}
	r, _ := c.NewReader(1)
	for i := int64(10); i < 2058; i++ {
		r.SeekFrame(i*PageFrames, 1)
		prime(c)
	}
	var left, right [128]float32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.SeekFrame(0, 1)
		r.Render(left[:], right[:])
	}
}
