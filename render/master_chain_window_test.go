package render

import "testing"

// A limiter hit is counted only when its reduction lands on an output frame
// the export delivers. With insert latency 3 and master chain latency 144 the
// first delivered frame of a range export reads its reduction at the range
// start plus 147 render positions.
func TestMasterChainReductionAlignsWithDeliveredFrames(t *testing.T) {
	const fromFrame, endFrame, insertLatency, masterLatency = 100000, 300000, 3, 144
	start, end := masterChainWindow(2, fromFrame, endFrame, insertLatency, masterLatency)
	if want := int64(fromFrame + insertLatency + masterLatency); start != want {
		t.Fatalf("window start = %d, want %d", start, want)
	}
	if want := int64(endFrame + insertLatency + masterLatency); end != want {
		t.Fatalf("window end = %d, want %d", end, want)
	}

	var before Report
	before.chainStart, before.chainEnd = start, end
	// The hit ends on the last position before the first delivered frame.
	for at := start - 5000; at < start; at++ {
		before.noteMasterChainReduction(at, 9)
	}
	if before.MaxMasterChainLimiterReductionDB != 0 {
		t.Fatalf("a hit that ends before the first delivered frame counted %.2f dB", before.MaxMasterChainLimiterReductionDB)
	}

	var atStart Report
	atStart.chainStart, atStart.chainEnd = start, end
	atStart.noteMasterChainReduction(start-1, 9)
	atStart.noteMasterChainReduction(start, 4)
	if atStart.MaxMasterChainLimiterReductionDB != 4 {
		t.Fatalf("a hit that starts at the first delivered frame counted %.2f dB, want 4", atStart.MaxMasterChainLimiterReductionDB)
	}

	var after Report
	after.chainStart, after.chainEnd = start, end
	after.noteMasterChainReduction(end, 9)
	if after.MaxMasterChainLimiterReductionDB != 0 {
		t.Fatalf("a reduction after the delivered interval counted %.2f dB", after.MaxMasterChainLimiterReductionDB)
	}
}

func TestMasterChainWindowKeepsFullSongUnchanged(t *testing.T) {
	start, _ := masterChainWindow(0, 0, 500000, 3, 144)
	if start != 0 {
		t.Fatalf("full-song window starts at %d, want 0", start)
	}
}
