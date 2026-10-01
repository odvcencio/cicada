package stream

import "testing"

func TestRetiredAndEndedReadersDoNotReportUnderruns(t *testing.T) {
	c, rs := newTestCache(t, 1, 0, 128)
	r := rs[0]
	var l, rr [128]float32
	r.Render(l[:], rr[:])
	if r.Stats() != (ReaderStats{}) {
		t.Fatal("unstarted reader reported an underrun", r.Stats())
	}
	r.SeekFrame(0, 1)
	prime(c)
	r.Render(l[:], rr[:])
	r.Render(l[:], rr[:])
	r.SeekFrame(0, 1)
	r.Render(l[:], rr[:])
	r.Stop()
	if r.Stats() != (ReaderStats{}) {
		t.Fatal("natural end reported cache recovery", r.Stats())
	}
}

func TestIntentRewriteDefersCancellation(t *testing.T) {
	c, rs := newTestCache(t, 1, 0, hourFrames)
	r := rs[0]
	r.SeekFrame(0, 1)
	work, ok := c.Next()
	if !ok {
		t.Fatal("no work")
	}
	r.intent.sequence.Add(1)
	if !work.Wanted() {
		t.Fatal("incomplete seek snapshot cancelled a useful read")
	}
	r.intent.sequence.Add(1)
	r.SeekFrame(PageFrames*10, 1)
	if work.Wanted() {
		t.Fatal("completed obsolete seek was not cancelled")
	}
	work.Cancel()
}

func TestAssetIDsPreventCrossAssetPageReuse(t *testing.T) {
	c, err := New(Config{Pages: 6, Readers: 2, AheadPages: 0}, []Asset{{ID: 1, Frames: PageFrames * 3, RateHz: 48000, Channels: 2}, {ID: 2, Frames: PageFrames * 3, RateHz: 48000, Channels: 1}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := c.NewReader(1)
	b, _ := c.NewReader(2)
	a.SeekFrame(0, 1)
	b.SeekFrame(0, 1)
	for {
		work, ok := c.Next()
		if !ok {
			break
		}
		for i := range work.Left() {
			work.Left()[i] = float32(work.Asset.ID)
			work.Right()[i] = float32(work.Asset.ID)
		}
		work.Publish()
	}
	la, _, oa := a.ReadFrame(0)
	lb, _, ob := b.ReadFrame(0)
	if !oa || !ob || la != 1 || lb != 2 || a.page == b.page {
		t.Fatal("different immutable assets shared PCM", la, lb)
	}
	a.Stop()
	b.Stop()
}
