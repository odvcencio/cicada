package engine

import (
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/pro"
	"math"
	"testing"
)

func vocalTestChain() []TrackEffectConfig {
	eq := pro.Band{Enabled: true, Type: pro.Highpass, FrequencyHz: 80, Q: .707}
	comp := fx.DefaultCompParams()
	comp.MakeupAuto = false
	deess := fx.DefaultDeesserParams()
	return []TrackEffectConfig{{Kind: TrackEffectEQ, EQ: &eq}, {Kind: TrackEffectComp, Comp: &comp}, {Kind: TrackEffectDeesser, Deesser: &deess}}
}
func TestTrackEffectsOwnStateAndHonorChainOrder(t *testing.T) {
	cfg := testConfig()
	cfg.Track[0].InsertChain = vocalTestChain()
	cfg.Track[1].InsertChain = cfg.Track[0].InsertChain
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	a, b := e.voices[0].chain, e.voices[1].chain
	for i := 0; i < 48000; i++ {
		a.Process(float32(.8*math.Sin(2*math.Pi*9000*float64(i)/48000)), 0)
	}
	if a.stages[2].deesser.GainReductionDB() < 1 || b.stages[2].deesser.GainReductionDB() != 0 || b.stages[1].comp.GainReductionDB() != 0 {
		t.Fatal("track processor state shared")
	}
	drive := fx.DefaultDriveParams()
	drive.GainDB = 24
	comp := fx.DefaultCompParams()
	comp.MakeupAuto = false
	comp.AttackMs = .1
	first, _ := newTrackEffectChain(48000, []TrackEffectConfig{{Kind: TrackEffectDrive, Drive: &drive}, {Kind: TrackEffectComp, Comp: &comp}})
	second, _ := newTrackEffectChain(48000, []TrackEffectConfig{{Kind: TrackEffectComp, Comp: &comp}, {Kind: TrackEffectDrive, Drive: &drive}})
	var x, y float32
	for i := 0; i < 10000; i++ {
		x, _ = first.Process(.15, .15)
		y, _ = second.Process(.15, .15)
	}
	if math.Abs(float64(x-y)) < .01 {
		t.Fatalf("chain order had no effect %g %g", x, y)
	}
}
func TestTrackEffectsRenderResetAndBlockParityNoAllocation(t *testing.T) {
	cfg := testConfig()
	cfg.MaxBlock = 512
	cfg.Track[0].InsertChain = vocalTestChain()
	a, _ := New(cfg)
	b, _ := New(cfg)
	note := cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}
	a.Push(note)
	b.Push(note)
	var al, ar, bl, br [512]float32
	a.Render(al[:], ar[:])
	for i := 0; i < 4; i++ {
		b.Render(bl[i*128:(i+1)*128], br[i*128:(i+1)*128])
	}
	if al != bl || ar != br {
		t.Fatal("block size changed processed render")
	}
	a.Reset()
	a.Push(note)
	a.Render(bl[:], br[:])
	if al != bl || ar != br {
		t.Fatal("reset did not clear track insert history")
	}
	if n := testing.AllocsPerRun(100, func() { a.Render(bl[:], br[:]) }); n != 0 {
		t.Fatalf("track chain allocated %g", n)
	}
}
func TestTrackEffectBoundsAndZeroGainBypass(t *testing.T) {
	chain := vocalTestChain()
	chain[0].EQ.Type = pro.Peak
	chain[0].EQ.GainDB = 0
	chain[1].Comp.Mix = 0
	chain[2].Deesser.Mix = 0
	c, err := newTrackEffectChain(48000, chain)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []float32{0, 1, -1, .31, 1e-30} {
		l, r := c.Process(x, -x)
		if l != x || r != -x {
			t.Fatal("zero gain/mix bypass changed PCM")
		}
	}
	if _, err := newTrackEffectChain(48000, append(chain, chain...)); err != nil {
		t.Fatal(err)
	}
	over := append(append(chain, chain...), chain...)
	if _, err := newTrackEffectChain(48000, over); err == nil {
		t.Fatal("accepted unbounded inserts")
	}
	if _, err := newTrackEffectChain(48000, []TrackEffectConfig{{Kind: TrackEffectComp, EQ: chain[0].EQ}}); err == nil {
		t.Fatal("accepted mismatched parameters")
	}
}

func TestTrackChainDriveLatencyAlignsEveryTrack(t *testing.T) {
	cfg := testConfig()
	p := fx.DefaultDriveParams()
	p.Mix = 0
	cfg.Track[0].InsertChain = []TrackEffectConfig{{Kind: TrackEffectDrive, Drive: &p}, {Kind: TrackEffectDrive, Drive: &p}}
	wet, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Track[0].InsertChain = nil
	dry, _ := New(cfg)
	wet.layerMask, dry.layerMask = 2, 2
	if wet.TrackLatencyFrames() != 2*fx.DriveLatencyFrames || wet.LatencyFrames()-dry.LatencyFrames() != 2*fx.DriveLatencyFrames {
		t.Fatal("chain latency not reported")
	}
	c := cmd.Command{Op: cmd.OpNoteOn, Track: 1, Arg0: 36 | 100<<8 | 1<<16}
	wet.Push(c)
	dry.Push(c)
	var wl, wr, dl, dr [512]float32
	for at := 0; at < 512; at += 128 {
		wet.Render(wl[at:at+128], wr[at:at+128])
		dry.Render(dl[at:at+128], dr[at:at+128])
	}
	for i := 0; i+2*fx.DriveLatencyFrames < 512; i++ {
		if wl[i+2*fx.DriveLatencyFrames] != dl[i] || wr[i+2*fx.DriveLatencyFrames] != dr[i] {
			t.Fatal("unprocessed track not aligned to whole chain")
		}
	}
}
