package render

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

var errSizeTestWriter = errors.New("size test reached writer")

type sizeTestWriter struct{ calls int }

func (w *sizeTestWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errSizeTestWriter
}

func TestScheduledWAVRejectsOverflowBeforeWriting(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntempo 20\ntrack bass acid {}\npattern hit { c3 }\narrange { place p bass hit { at = 0ticks length = 4000000000000000ticks } }\n"))
	if score == nil {
		t.Fatal(ds)
	}
	w := &sizeTestWriter{}
	_, err := WAV(score, Options{SampleRate: 96000, Bits: 32}, w)
	if err == nil || !strings.Contains(err.Error(), "WAV exceeds RIFF size limit") || w.calls != 0 {
		t.Fatalf("huge arrangement err=%v writer calls=%d; want RIFF rejection before writing", err, w.calls)
	}
}

func TestScheduledWAVRIFFFrameBoundary(t *testing.T) {
	for _, bits := range []int{16, 24, 32} {
		clock, _ := seq.NewClock(48000, 120000)
		maxFrames := int64(^uint32(0)-60) / int64(bits/4)
		lastBar := maxFrames / clock.SampleAtTick(seq.TicksPerBar)
		for _, extra := range []int64{0, 1} {
			t.Run(fmt.Sprintf("bits%d/extra%d", bits, extra), func(t *testing.T) {
				source := fmt.Sprintf("cicada 2\ntempo 120\ntrack bass acid {}\npattern hit { c3 }\narrange { place p bass hit { at = 0ticks length = %dticks } }\n", (lastBar+extra)*seq.TicksPerBar)
				score, ds := notation.Parse([]byte(source))
				if score == nil {
					t.Fatal(ds)
				}
				w := &sizeTestWriter{}
				_, err := WAV(score, Options{Bits: bits}, w)
				if extra == 0 {
					if !errors.Is(err, errSizeTestWriter) || w.calls != 1 {
						t.Fatalf("valid span err=%v writer calls=%d", err, w.calls)
					}
				} else if err == nil || !strings.Contains(err.Error(), "WAV exceeds RIFF size limit") || w.calls != 0 {
					t.Fatalf("oversized span err=%v writer calls=%d", err, w.calls)
				}
			})
		}
	}
}
