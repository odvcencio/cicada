package detmath

import "testing"

// TestConstantSplits checks that the halves twoProdSplit is given are the ones
// split computes, so twoProdSplit(a, b, bh, bl) equals twoProd(a, b) bit for
// bit.
func TestConstantSplits(t *testing.T) {
	for _, c := range []struct {
		name    string
		b, h, l float64
	}{
		{"ln2Hi", ln2Hi, ln2HiH, ln2HiL},
		{"ln10Hi", ln10Hi, ln10HiH, ln10HiL},
		{"halfPi4", halfPi4, halfPi4H, halfPi4L},
		{"piHi", 0x1.921fb54442d18p+01, piHiH, piHiL}, // SinPi's piHi
	} {
		if h, l := split(c.b); h != c.h || l != c.l {
			t.Errorf("split(%s) = %x, %x; the constants say %x, %x", c.name, h, l, c.h, c.l)
		}
		for _, a := range []float64{0.3, -1.7e-9, 12345.678, 0x1p-500} {
			p, e := twoProd(a, c.b)
			q, f := twoProdSplit(a, c.b, c.h, c.l)
			if p != q || e != f {
				t.Errorf("twoProdSplit(%v, %s) = %x, %x; twoProd gives %x, %x", a, c.name, q, f, p, e)
			}
		}
	}
}
