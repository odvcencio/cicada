package render

import (
	"bytes"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

// TestTailAboveTenSecondsRenders renders a direct score and an arranged score
// (the scheduled renderer) with tails past the old 10 s limit, up to the 60 s
// maximum, and checks that the tail adds exactly its frames. A reverb decay
// reaches 12 s, so one decay time did not fit the old limit.
func TestTailAboveTenSecondsRenders(t *testing.T) {
	scores := map[string]string{"direct": testScore, "scheduled": arrangedTailScore}
	for name, source := range scores {
		score, diagnostics := notation.Parse([]byte(source))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("%s: parse: %+v", name, diagnostic)
			}
		}
		var short bytes.Buffer
		base, err := WAV(score, Options{SampleRate: 48_000, Bits: 16, Bars: 1, TailSec: 0}, &short)
		if err != nil {
			t.Fatalf("%s: tail 0s: %v", name, err)
		}
		for _, tail := range []float64{10.5, 12, MaxTailSeconds} {
			var long bytes.Buffer
			report, err := WAV(score, Options{SampleRate: 48_000, Bits: 16, Bars: 1, TailSec: tail}, &long)
			if err != nil {
				t.Fatalf("%s: tail %gs: %v", name, tail, err)
			}
			want := int64(math.Ceil(tail * 48_000))
			if report.TailFrames != want || report.Frames != base.Frames+want {
				t.Fatalf("%s: tail %gs: %d tail frames, %d frames; want %d and %d", name, tail, report.TailFrames, report.Frames, want, base.Frames+want)
			}
			path := filepath.Join(t.TempDir(), "tail.wav")
			if err := os.WriteFile(path, long.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 16, Bars: 1, TailSec: tail, PeakMaxDB: 100, DCMaxDB: 100}); err != nil {
				t.Fatalf("%s: verify-wav --tail %gs: %v", name, tail, err)
			}
		}
	}
}

// TestTailOutsideTheRangeNamesTheTail checks that every renderer and the WAV
// check refuse a tail outside 0 to 60 s with a *TailRangeError whose message
// names the tail, its value and the range. The scheduled renderer used to say
// only "invalid scheduled render options".
func TestTailOutsideTheRangeNamesTheTail(t *testing.T) {
	for name, source := range map[string]string{"direct": testScore, "scheduled": arrangedTailScore} {
		score, _ := notation.Parse([]byte(source))
		for _, tail := range []float64{60.5, -1, math.NaN(), math.Inf(1)} {
			_, err := WAV(score, Options{SampleRate: 48_000, Bits: 16, Bars: 1, TailSec: tail}, &bytes.Buffer{})
			var rangeErr *TailRangeError
			if !errors.As(err, &rangeErr) {
				t.Fatalf("%s: tail %g: error %v, want *TailRangeError", name, tail, err)
			}
			if tail == 60.5 && err.Error() != "tail 60.5s is outside the 0 to 60s range" {
				t.Fatalf("%s: message %q", name, err.Error())
			}
		}
		if _, err := Stems(score, Options{SampleRate: 48_000, Bits: 32, Bars: 1, TailSec: 61}, filepath.Join(t.TempDir(), "stems")); err == nil || !errors.As(err, new(*TailRangeError)) {
			t.Fatalf("%s: stems with a 61 s tail: %v", name, err)
		}
	}
	if _, err := VerifyWAV(filepath.Join(t.TempDir(), "missing.wav"), VerifyOptions{SampleRate: 48_000, Bits: 16, Bars: 1, TailSec: 61}); !errors.As(err, new(*TailRangeError)) {
		t.Fatalf("verify-wav with a 61 s tail: %v", err)
	}
}

// arrangedTailScore uses an arrangement, so it renders on the scheduled
// renderer, whose own option check carries the tail limit.
const arrangedTailScore = `cicada 2
tempo 120
seed 42
track bass acid { level = -18dB cutoff = 800Hz }
pattern pulse { c2 . g2 . c3 . eb3 . }
arrange {
  place opening bass pulse { at = @1.1.1 length = 2bars }
}
`
