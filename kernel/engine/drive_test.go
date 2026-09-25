package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/fx"
)

func TestDriveInsertAlignsLiveTracksAndDoesNotAllocate(t *testing.T) {
	base := testConfig()
	active := base
	params := fx.DriveParams{Shape: fx.Hard, GainDB: 18, ToneHz: 12_000, Mix: .75}
	active.Track[0].InsertDrive = &params
	driven, err := New(active)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	// Hear only drums. Their waveform must be unchanged, delayed by the
	// insert's latency before the shared master limiter.
	driven.layerMask, dry.layerMask = 2, 2
	note := cmd.Command{Op: cmd.OpNoteOn, Track: 1, Index: 0, Arg0: 36 | 110<<8 | 1<<16}
	if !driven.Push(note) || !dry.Push(note) {
		t.Fatal("drum note rejected")
	}
	var drivenL, drivenR, dryL, dryR [512]float32
	var blockL, blockR [128]float32
	for block := 0; block < 4; block++ {
		driven.Render(blockL[:], blockR[:])
		copy(drivenL[block*128:], blockL[:])
		copy(drivenR[block*128:], blockR[:])
		dry.Render(blockL[:], blockR[:])
		copy(dryL[block*128:], blockL[:])
		copy(dryR[block*128:], blockR[:])
	}
	heard := false
	for frame := 0; frame+fx.DriveLatencyFrames < len(dryL); frame++ {
		if dryL[frame] != 0 || dryR[frame] != 0 {
			heard = true
		}
		if drivenL[frame+fx.DriveLatencyFrames] != dryL[frame] || drivenR[frame+fx.DriveLatencyFrames] != dryR[frame] {
			t.Fatalf("dry track changed at frame %d with drive insert on another track", frame)
		}
	}
	if !heard {
		t.Fatal("drum test note was silent")
	}
	driven.Reset()
	if !driven.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: 45 | 100<<8}) {
		t.Fatal("acid note rejected after reset")
	}
	if count := testing.AllocsPerRun(100, func() { driven.Render(blockL[:], blockR[:]) }); count != 0 {
		t.Fatalf("engine with drive insert allocated %g times per block", count)
	}
}
