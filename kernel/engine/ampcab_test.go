package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"testing"
)

func TestAmpCabAlignsOtherTrack(t *testing.T) {
	cfg := testConfig()
	p := ampcab.DefaultParams()
	cfg.Track[0].InsertChain = []TrackEffectConfig{{Kind: TrackEffectAmpCab, AmpCab: &p}}
	wet, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Track[0].InsertChain = nil
	dry, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wet.layerMask, dry.layerMask = 2, 2
	if wet.TrackLatencyFrames() != ampcab.Latency {
		t.Fatalf("reported latency %d", wet.TrackLatencyFrames())
	}
	c := cmd.Command{Op: cmd.OpNoteOn, Track: 1, Arg0: 36 | 100<<8 | 1<<16}
	wet.Push(c)
	dry.Push(c)
	var wl, wr, dl, dr [512]float32
	for at := 0; at < 512; at += 128 {
		wet.Render(wl[at:at+128], wr[at:at+128])
		dry.Render(dl[at:at+128], dr[at:at+128])
	}
	for i := 0; i+ampcab.Latency < 512; i++ {
		if wl[i+ampcab.Latency] != dl[i] || wr[i+ampcab.Latency] != dr[i] {
			t.Fatalf("unprocessed track misaligned at %d", i)
		}
	}
}
