package audiobackend

import "testing"

func TestEngineSoakBucket(t *testing.T) {
	tests := []struct {
		nanoseconds uint64
		want        int
	}{
		{0, 0},
		{1, 0},
		{2, 1},
		{3, 1},
		{4, 2},
		{7, 2},
		{8, 3},
		{1 << 63, 63},
		{^uint64(0), 63},
	}
	for _, test := range tests {
		if got := engineSoakBucket(test.nanoseconds); got != test.want {
			t.Errorf("engineSoakBucket(%d) = %d, want %d", test.nanoseconds, got, test.want)
		}
	}
}

func TestEngineSoakHistogramPercentile(t *testing.T) {
	var histogram engineSoakHistogram
	histogram.record(1)
	histogram.record(2)
	histogram.record(8)
	if got := histogram.percentile(50, 3); got != 2 {
		t.Errorf("p50 = %d ns, want 2 ns", got)
	}
	if got := histogram.percentile(99, 3); got != 8 {
		t.Errorf("p99 = %d ns, want 8 ns", got)
	}
}
