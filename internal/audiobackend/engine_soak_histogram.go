package audiobackend

import (
	"math/bits"
	"sync/atomic"
)

const engineSoakHistogramBuckets = 64

type engineSoakHistogram struct {
	buckets [engineSoakHistogramBuckets]atomic.Uint64
}

func engineSoakBucket(nanoseconds uint64) int {
	if nanoseconds == 0 {
		return 0
	}
	return bits.Len64(nanoseconds) - 1
}

func (h *engineSoakHistogram) record(nanoseconds uint64) {
	h.buckets[engineSoakBucket(nanoseconds)].Add(1)
}

func (h *engineSoakHistogram) percentile(percent int, total uint64) uint64 {
	if total == 0 {
		return 0
	}
	rank := (total*uint64(percent) + 99) / 100
	var seen uint64
	for bucket := range h.buckets {
		seen += h.buckets[bucket].Load()
		if seen >= rank {
			return uint64(1) << bucket
		}
	}
	return 0
}
