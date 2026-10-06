package engine

import (
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/voice/guitar"
)

func guitarSceneConfig() Config {
	cfg := testConfig()
	cfg.Tracks = 1
	cfg.Track[0] = TrackConfig{Kind: VoiceGuitar, Experimental: true, Guitar: guitar.DefaultParams()}
	cfg.Scenes = []Scene{{}, {Settings: []SceneSetting{
		{Track: 0, ID: kernel.ParamGuitarBend, Value: 7},
		{Track: 0, ID: kernel.ParamGuitarVibrato, Value: 30},
		{Track: 0, ID: kernel.ParamGuitarBrightness, Value: .9},
		{Track: 0, ID: kernel.ParamGuitarDamping, Value: .2},
		{Track: 0, ID: kernel.ParamGuitarPickup, Value: .1},
		{Track: 0, ID: kernel.ParamGuitarDrive, Value: .7},
	}}}
	cfg.Song = []SongEntry{{Scene: 0, Bars: 1}, {Scene: 1, Bars: 1}, {Scene: 0, Bars: 1}}
	return cfg
}

func TestGuitarSceneDefaultsRestoredOnSeekAndRestart(t *testing.T) {
	for _, authored := range []guitar.Params{guitar.DefaultParams(), {Bend: -2, Vibrato: 12, Brightness: .6, Damping: .12, Pickup: .3, Drive: .1}} {
		for _, restart := range []bool{false, true} {
			cfg := guitarSceneConfig()
			cfg.Track[0].Guitar = authored
			e, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1, Arg1: 1})
			if e.faulted || e.voices[0].guitar.Params().Bend != 7 {
				t.Fatal("later scene did not apply guitar controls")
			}
			if restart {
				e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 3})
			} else {
				e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
			}
			if e.faulted || e.voices[0].guitar.Params() != authored {
				t.Fatalf("transport retained later guitar controls (restart=%v): %+v", restart, e.voices[0].guitar.Params())
			}
			fresh, err := guitar.New(48_000, authored)
			if err != nil {
				t.Fatal(err)
			}
			e.voices[0].guitar.NoteOn(57, 100, false, false)
			fresh.NoteOn(57, 100, false, false)
			for i := 0; i < 2048; i++ {
				if got, want := e.voices[0].guitar.Next(), fresh.Next(); got != want {
					t.Fatalf("restored guitar audio differs at sample %d (restart=%v): %g != %g", i, restart, got, want)
				}
			}
		}
	}
}

func TestGuitarSeekSettlesControlsAndBoundarySmooths(t *testing.T) {
	for _, position := range []struct{ bar, tick uint32 }{{1, 1}, {2, 0}} {
		e, err := New(guitarSceneConfig())
		if err != nil {
			t.Fatal(err)
		}
		e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: position.bar, Arg1: position.tick})
		if e.faulted {
			t.Fatal("guitar seek faulted")
		}
		settled, err := guitar.New(48_000, e.voices[0].guitar.Params())
		if err != nil {
			t.Fatal(err)
		}
		e.voices[0].guitar.NoteOn(57, 100, false, false)
		settled.NoteOn(57, 100, false, false)
		for i := 0; i < 2048; i++ {
			if got, want := e.voices[0].guitar.Next(), settled.Next(); got != want {
				t.Fatalf("seek introduced a control glide at sample %d (bar %d): %g != %g", i, position.bar, got, want)
			}
		}
	}
	// An exact boundary must still apply the current scene with smoothing.
	e, err := New(guitarSceneConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.apply(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1})
	smoothed, err := guitar.New(48_000, guitar.DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	for id := kernel.ParamGuitarBend; id <= kernel.ParamGuitarDrive; id++ {
		if err := smoothed.SetParam(id, e.voices[0].guitar.Params().Value(id)); err != nil {
			t.Fatal(err)
		}
	}
	e.voices[0].guitar.NoteOn(57, 100, false, false)
	smoothed.NoteOn(57, 100, false, false)
	for i := 0; i < 2048; i++ {
		if e.voices[0].guitar.Next() != smoothed.Next() {
			t.Fatalf("boundary smoothing changed at sample %d", i)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		e.apply(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1, Arg1: 1})
	})
	if e.faulted || allocs != 0 {
		t.Fatalf("guitar reconstruction fault=%v allocations=%g", e.faulted, allocs)
	}
}
