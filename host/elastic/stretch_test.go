package elastic

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func renderPCM(t *testing.T, pcm []float32, rate, target int, anchors []Anchor) []float32 {
	t.Helper()
	out := make([]float32, target)
	next := int64(0)
	err := Render(context.Background(), Config{rate, int64(len(pcm)), int64(target), anchors}, func(_ context.Context, at int64, v []float32) error {
		if at < 0 || at+int64(len(v)) > int64(len(pcm)) {
			t.Fatalf("read beyond retained PCM: %d+%d", at, len(v))
		}
		copy(v, pcm[at:])
		return nil
	}, func(_ context.Context, at int64, v []float32) error {
		if at != next {
			t.Fatalf("noncontiguous output %d, want %d", at, next)
		}
		next += int64(len(v))
		copy(out[at:], v)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if next != int64(target) {
		t.Fatal("incomplete output", next, target)
	}
	return out
}

func TestIdentityKeepsEveryPCMBit(t *testing.T) {
	pcm := make([]float32, 19001)
	for i := range pcm {
		pcm[i] = float32(.31 * math.Sin(float64(i)*.17))
	}
	out := renderPCM(t, pcm, 48000, len(pcm), []Anchor{{}, {5000, 5000}, {19001, 19001}})
	if !reflect.DeepEqual(pcm, out) {
		t.Fatal("unity stretch altered raw PCM")
	}
}

func TestStretchPreservesPitchAndLevel(t *testing.T) {
	for _, rate := range []int{44100, 48000, 96000} {
		for _, ratio := range []float64{.5, .8, 1.2, 2} {
			n := rate * 2
			pcm := make([]float32, n)
			for i := range pcm {
				pcm[i] = float32(.2 * math.Sin(2*math.Pi*220*float64(i)/float64(rate)))
			}
			target := int(float64(n) * ratio)
			out := renderPCM(t, pcm, rate, target, []Anchor{{}, {int64(n), int64(target)}})
			// Count independent zero crossings in the settled middle. Resampling
			// instead of stretching would move this tone to 110--440 Hz.
			start, end := target/4, target*3/4
			crossings := 0
			var energy float64
			for i := start + 1; i < end; i++ {
				if out[i-1] <= 0 && out[i] > 0 {
					crossings++
				}
				energy += float64(out[i]) * float64(out[i])
			}
			hz := float64(crossings) * float64(rate) / float64(end-start)
			if math.Abs(hz-220) > 2.1 {
				t.Fatalf("rate=%d ratio=%g: pitch=%gHz", rate, ratio, hz)
			}
			rms := math.Sqrt(energy / float64(end-start-1))
			if rms < .13 || rms > .15 {
				t.Fatalf("rate=%d ratio=%g: RMS=%g", rate, ratio, rms)
			}
		}
	}
}

func TestTempoAnchorsKeepPhrasePositions(t *testing.T) {
	rate := 48000
	pcm := make([]float32, rate*4)
	for i := range pcm {
		seconds := float64(i) / float64(rate)
		if seconds >= .9 && seconds < 1.1 || seconds >= 2.9 && seconds < 3.1 {
			pcm[i] = float32(.25 * math.Sin(2*math.Pi*330*seconds))
		}
	}
	// First half slows down, second half speeds up. Their source/target
	// boundary is a musical anchor, not a global resampling ratio.
	out := renderPCM(t, pcm, rate, rate*4, []Anchor{{}, {int64(rate * 2), int64(rate * 5 / 2)}, {int64(rate * 4), int64(rate * 4)}})
	for _, center := range []float64{1.25, 3.25} {
		var weight, moment float64
		for i := int((center - .3) * float64(rate)); i < int((center+.3)*float64(rate)); i++ {
			e := float64(out[i]) * float64(out[i])
			weight += e
			moment += e * float64(i) / float64(rate)
		}
		if weight == 0 || math.Abs(moment/weight-center) > .035 {
			t.Fatalf("phrase shifted: expected %gs, energy center=%g", center, moment/weight)
		}
	}
}

func TestInvalidMappingAndCancellationDoNotWrite(t *testing.T) {
	for _, anchors := range [][]Anchor{{{}, {100, 0}}, {{}, {100, 300}}, {{1, 0}, {100, 100}}} {
		err := Render(context.Background(), Config{48000, 100, 100, anchors}, func(context.Context, int64, []float32) error { t.Fatal("read invalid mapping"); return nil }, func(context.Context, int64, []float32) error { t.Fatal("wrote invalid mapping"); return nil })
		if err == nil {
			t.Fatal("accepted invalid mapping", anchors)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Render(ctx, Config{48000, 100, 100, []Anchor{{}, {100, 100}}}, func(context.Context, int64, []float32) error { t.Fatal("read after cancellation"); return nil }, func(context.Context, int64, []float32) error { t.Fatal("wrote after cancellation"); return nil }); err != context.Canceled {
		t.Fatal(err)
	}
}
