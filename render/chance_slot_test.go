package render

import (
	"fmt"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestWAVChanceUsesCompiledPatternSlot(t *testing.T) {
	var drumSeed uint32
	for ; drumSeed < 10000; drumSeed++ {
		if seq.ProbabilityHit(60, drumSeed, 0, 7, 0, 1) != seq.ProbabilityHit(60, drumSeed, 0, uint8(drum.CH), 0, 1) {
			break
		}
	}
	if drumSeed == 10000 {
		t.Fatal("no distinguishing drum chance seed")
	}
	var slideSeed uint32
	for ; slideSeed < 10000; slideSeed++ {
		if seq.ProbabilityHit(60, slideSeed, 0, 7, 0, 15) && !seq.ProbabilityHit(60, slideSeed, 0, 0, 0, 15) && seq.ProbabilityHit(60, slideSeed, 0, 13, 1, 0) {
			break
		}
	}
	if slideSeed == 10000 {
		t.Fatal("no distinguishing source/target chance seed")
	}
	for _, fixture := range []struct{ name, source string }{
		{"drum lane differs from slot", fmt.Sprintf(`seed %d
track drums drums {}
pattern beat drums { slot=7 ch: xx?60xx }
scene a { drums=beat }
song { a*2 }`, drumSeed)},
		{"acid explicit slot", `seed 1717
track bass acid {}
pattern line { slot=7 1?60 . 3?60 . }
scene a { bass=line }
song { a*2 }`},
		{"graph automatic second slot", `seed 1717
instrument tone { voice mono { out=sine(pitch)*env(gate,300ms) } }
track lead tone {}
pattern first { 1 . . . }
pattern second { 3?60 . 5?60 . }
scene a { lead=first }
scene b { lead=second }
song { a b }`},
		{"held gate from nonzero slot", fmt.Sprintf(`seed %d
track bass acid {}
pattern first { slot=7 gate=100%% - . . . . . . . . . . . . . . 1?60 }
pattern second { slot=13 . . . . . . . . . . . . . . . . }
scene a { bass=first }
scene b { bass=second }
song { a b }`, slideSeed)},
		{"probabilistic slide across slots", fmt.Sprintf(`seed %d
track bass acid {}
pattern first { slot=7 gate=60%% . . . . . . . . . . . . . . . 1~?60 }
pattern second { slot=13 gate=60%% 5?60 . . . . . . . . . . . . . . . }
scene a { bass=first }
scene b { bass=second }
song { a b }`, slideSeed)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			score, ds := notation.Parse([]byte(fixture.source))
			for _, d := range ds {
				if d.Severity == "error" {
					t.Fatal(d)
				}
			}
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			cfg, err := project.CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			a := driftOffline(t, score, 2, .1, 4096)
			b := driftEngine(t, cfg, 2, .1, driftLatency(t, cfg))
			for i := range a.left {
				if a.left[i] != b.left[i] || a.right[i] != b.right[i] {
					t.Fatalf("slot-dependent chance differs at sample %d: offline=(%g,%g), engine=(%g,%g)", i, a.left[i], a.right[i], b.left[i], b.right[i])
				}
			}
		})
	}
}

func TestWAVChorusChanceMatchesEngine(t *testing.T) {
	score, cfg := driftScore(t, "cicada-chorus")
	a := driftOffline(t, score, 1, 0, 4096)
	b := driftEngine(t, cfg, 1, 0, driftLatency(t, cfg))
	for i := range a.left {
		if a.left[i] != b.left[i] || a.right[i] != b.right[i] {
			t.Fatalf("chorus differs at sample %d: offline=(%g,%g), engine=(%g,%g)", i, a.left[i], a.right[i], b.left[i], b.right[i])
		}
	}
}
