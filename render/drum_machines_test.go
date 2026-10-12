package render

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func classicDrumExample(t testing.TB) (*notation.Score, *notation.Score, *project.Project) {
	t.Helper()
	score, ds, err := project.LoadScore(filepath.Join("..", "examples", "drum-machines", "main.cicada"), nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("classic drums: %v %v", err, ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	text, err := project.ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	inline, ds := notation.Parse(text)
	if inline == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	return score, inline, p
}

func TestClassicDrumNativeOfflineParity(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	verifySourceNativeAndOfflineByteParity(t, classicDrumExample, "classic-drums")
}

func TestClassicDrumLanesAcrossRates(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	score, _, p := classicDrumExample(t)
	for _, rate := range []int{44100, 48000, 96000} {
		tracks, err := compileTracks(score, p, rate)
		if err != nil {
			t.Fatal(err)
		}
		if len(tracks) != 2 {
			t.Fatalf("expected two kits, got %d tracks", len(tracks))
		}
		for _, track := range tracks {
			for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
				t.Run(fmt.Sprintf("%d/%s/%s", rate, track.name, drum.Names[lane]), func(t *testing.T) {
					kit := track.drums
					kit.Reset()
					kit.Hit(lane, 127, false)
					peak, energy, sum := 0.0, 0.0, 0.0
					for i := 0; i < rate; i++ {
						l, r := kit.NextStereo()
						x := (float64(l) + float64(r)) / math.Sqrt2
						peak = max(peak, math.Abs(x))
						energy += x * x
						sum += x
						if math.IsNaN(x) || math.IsInf(x, 0) {
							t.Fatalf("non-finite sample at %d", i)
						}
					}
					if kit.Fault() || energy < 1e-6 || peak > 1 || math.Abs(sum/float64(rate)) > .003 {
						t.Fatalf("peak=%g energy=%g DC=%g fault=%v", peak, energy, sum/float64(rate), kit.Fault())
					}
				})
			}
		}
	}
}

func TestClassicDrumHatChokeAndAllocation(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	score, _, p := classicDrumExample(t)
	tracks, err := compileTracks(score, p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range tracks {
		kit := track.drums
		kit.Hit(drum.OH, 127, false)
		for range 1000 {
			kit.NextStereo()
		}
		kit.Hit(drum.CH, 100, false)
		// Silence the triggering closed hat so only an unchoked open hat
		// could contribute after the engine's 5 ms choke fade.
		params := kit.Params(drum.CH)
		params.LevelDB = -1000
		if err := kit.SetParams(drum.CH, params); err != nil {
			t.Fatal(err)
		}
		for range 240 {
			kit.NextStereo()
		}
		for range 128 {
			if l, r := kit.NextStereo(); l != 0 || r != 0 {
				t.Fatalf("%s open hat survived choke: %g %g", track.name, l, r)
			}
		}
		params.LevelDB = -6
		if err := kit.SetParams(drum.CH, params); err != nil {
			t.Fatal(err)
		}
		allocs := testing.AllocsPerRun(100, func() {
			kit.Hit(drum.BD, 110, false)
			kit.Hit(drum.SD, 90, true)
			kit.Hit(drum.CH, 85, false)
			for range 128 {
				kit.NextStereo()
			}
		})
		if allocs != 0 {
			t.Fatalf("%s hit/render allocated %g objects", track.name, allocs)
		}
	}
}
