package control

import "testing"

func TestVocalDragKeepsCoverageAndExclusiveNonemptyEnd(t *testing.T) {
	cases := []struct {
		a, b, lo, hi, grid int64
		want               VocalRange
	}{
		{3840, 7680, 0, 7680, 240, VocalRange{3840, 7680}},
		{7680, 3840, 0, 7680, 240, VocalRange{3840, 7680}},
		{-100, 9000, 3840, 7680, 960, VocalRange{3840, 7680}},
		{7680, 7680, 0, 7680, 240, VocalRange{7440, 7680}},
		{0, 0, 0, 7680, 1, VocalRange{0, 1}},
		{5, 6, 5, 111, 240, VocalRange{5, 111}},
	}
	for _, c := range cases {
		if got := SelectVocalRange(c.a, c.b, c.lo, c.hi, c.grid); got != c.want {
			t.Fatalf("drag %+v -> %+v", c, got)
		}
	}
	r := VocalRange{3840, 7680}
	if got := TrimVocalRange(r, false, 9000, 0, 9000, 240); got.Start != 7679 || got.End != 7680 {
		t.Fatalf("crossed start %+v", got)
	}
	if got := TrimVocalRange(r, true, -100, 0, 9000, 240); got.Start != 3840 || got.End != 3841 {
		t.Fatalf("crossed end %+v", got)
	}
	if got := MoveVocalRange(r, 9000, 0, 9000, 240); got != (VocalRange{5160, 9000}) {
		t.Fatalf("move past end %+v", got)
	}
	if got := MoveVocalRange(r, -9000, 0, 9000, 240); got != (VocalRange{0, 3840}) {
		t.Fatalf("move past start %+v", got)
	}
}
