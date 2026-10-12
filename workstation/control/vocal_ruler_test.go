package control

import (
	"math"
	"reflect"
	"testing"
)

func TestVocalRulerDefersUnmeasuredWidth(t *testing.T) {
	for _, width := range []float64{0, -88, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := VocalRulerTicks(8*3840, width); got != nil {
			t.Fatalf("width %v produced ruler ticks %v", width, got)
		}
	}
	for _, end := range []int64{0, -1, 1<<40 + 1, math.MaxInt64} {
		if got := VocalRulerTicks(end, 1024); got != nil {
			t.Fatalf("invalid song end %d produced ruler ticks %v", end, got)
		}
	}
}

func TestVocalRulerKeepsDesktopAndPhoneBarSpacing(t *testing.T) {
	cases := []struct {
		name  string
		end   int64
		width float64
		want  []int64
	}{
		{"desktop", 8 * 3840, 1260, []int64{0, 3840, 7680, 11520, 15360, 19200, 23040, 26880}},
		{"phone", 8 * 3840, 244, []int64{0, 7680, 15360, 23040}},
		{"phone zoomed", 8 * 3840, 488, []int64{0, 3840, 7680, 11520, 15360, 19200, 23040, 26880}},
		{"less than one label", 8 * 3840, 1, []int64{0}},
		{"partial bar", 960, 244, []int64{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VocalRulerTicks(c.end, c.width); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ruler = %v, want %v", got, c.want)
			}
		})
	}
}

func TestVocalRulerBoundsLongSongsAndTinyWidths(t *testing.T) {
	for _, end := range []int64{3840, 128 * 3840, 513 * 3840, 1 << 40} {
		for _, width := range []float64{math.SmallestNonzeroFloat64, 1, 244, 1260, 2_000_000} {
			ticks := VocalRulerTicks(end, width)
			if len(ticks) == 0 || len(ticks) > 512 || ticks[0] != 0 {
				t.Fatalf("end=%d width=%v: invalid label count %d", end, width, len(ticks))
			}
			for i, tick := range ticks {
				if tick < 0 || tick >= end || tick%3840 != 0 || i > 0 && tick <= ticks[i-1] {
					t.Fatalf("end=%d width=%v: invalid ruler ticks %v", end, width, ticks)
				}
			}
		}
	}
}
