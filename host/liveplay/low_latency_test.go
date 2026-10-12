package liveplay

import (
	"fmt"
	"testing"
)

func TestLowPeriodReadDoesNotRenderAheadOfLiveInput(t *testing.T) {
	for _, frames := range []int{64, 128, 256, 480, 512, 1024} {
		t.Run(fmt.Sprint(frames), func(t *testing.T) {
			p, err := New(noteScore(t), 48000)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			pcm := make([]byte, frames*8)
			if _, err := p.Read(pcm); err != nil {
				t.Fatal(err)
			}
			if p.sample != int64(frames) || p.read != p.buffered {
				t.Fatalf("device asked%dframes, engine rendered%d retained%dbytes", frames, p.sample, p.buffered-p.read)
			}
			if err := p.Note("bass", 60, 100, true); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Read(pcm); err != nil {
				t.Fatal(err)
			}
			if len(p.events) == 0 || (<-p.events).Kind != "note-on" {
				t.Fatal("note was delayed beyond next device callback")
			}
			if p.sample != int64(frames*2) || p.read != p.buffered {
				t.Fatal("live input added render lookahead")
			}
			if allocations := testing.AllocsPerRun(1000, func() {
				if _, err := p.Read(pcm); err != nil {
					panic(err)
				}
			}); allocations != 0 {
				t.Fatalf("low-period callback allocated%g", allocations)
			}
			t.Logf("METRIC device_period=%d render_lookahead_frames=0 callback_allocations=0", frames)
		})
	}
}
