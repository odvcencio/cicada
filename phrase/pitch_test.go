package phrase

import "testing"

func TestMusicalPassesKeepPitchesAndArticulationsInRange(t *testing.T) {
	for scale := Minor; scale <= Blues; scale++ {
		for seed := uint64(0); seed < 200; seed++ {
			params := DefaultParams()
			params.Seed, params.Key, params.Scale = seed, 9, scale
			p, err := normalize(params)
			if err != nil {
				t.Fatal(err)
			}
			s := newStream(seed)
			onsets := generateRhythm(&s, int(p.Steps), p.density, false)
			notes, err := generateDegrees(&s, onsets, p)
			if err != nil {
				t.Fatal(err)
			}
			jumpOctaves(&s, notes, p.octaveJump)
			addAccents(notes, p.accentDensity)
			addSlides(&s, notes, p.slideDensity)
			if !notes[0].active || notes[0].class != classRoot {
				t.Fatalf("missing root downbeat: scale=%d seed=%d", scale, seed)
			}
			for index, note := range notes {
				if note.active && (note.note < 0 || note.note > 127) {
					t.Fatalf("invalid pitch at %d: %+v", index, note)
				}
				if note.slide && !notes[(index+1)%len(notes)].active {
					t.Fatalf("slide into rest: scale=%d seed=%d step=%d", scale, seed, index)
				}
				if note.tie && !note.active {
					t.Fatalf("tie on rest: scale=%d seed=%d step=%d", scale, seed, index)
				}
			}
			for _, draw := range s.trace {
				switch draw.Pass {
				case "rhythm", "degree", "octave", "slide":
				default:
					t.Fatalf("unexpected pass %q", draw.Pass)
				}
			}
		}
	}
}

func TestDegreeRowsAndScaleOffsets(t *testing.T) {
	for row, weights := range degreeWeights {
		sum := 0
		for _, weight := range weights {
			sum += int(weight)
		}
		if sum != 100 {
			t.Fatalf("degree row %d totals %d, want 100", row, sum)
		}
	}
	for scale := Minor; scale <= Blues; scale++ {
		if classOffsets[scale][classRoot] != 0 || classOffsets[scale][classOctave] != 12 {
			t.Fatalf("invalid endpoints for scale %d", scale)
		}
	}
}
