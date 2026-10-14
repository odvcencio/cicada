package project

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

const (
	firstLimiter = "fx first limiter { ceiling = -12dBTP lookahead = 1ms release = 20ms }\n"
	lastLimiter  = "fx last limiter { ceiling = 0dBTP lookahead = 10ms release = 20ms }\n"
)

func preparedChain(t *testing.T, effects, master string) *masterChain {
	t.Helper()
	p := masterProject(t, masterMusic+effects+master+"\n")
	chain, err := PrepareMaster(p, 48000)
	if err != nil || chain == nil {
		t.Fatalf("prepare master: %v", err)
	}
	return chain.(*masterChain)
}

type chainRun struct {
	aggregate []float64 // reading after each Process call, at the chain output
	first     []float64 // first limiter alone, read at its own output
	last      []float64 // last limiter alone, fed by the first limiter's output
}

type burstChains struct{ chain, firstOnly, lastOnly *masterChain }

func newBurstChains(t *testing.T) burstChains {
	t.Helper()
	return burstChains{
		preparedChain(t, firstLimiter+lastLimiter, "master { insert = first -> last }"),
		preparedChain(t, firstLimiter, "master { insert = first }"),
		preparedChain(t, lastLimiter, "master { insert = last }"),
	}
}

func runBurst(t *testing.T, chains burstChains, burstAt, frames int) chainRun {
	t.Helper()
	chain, firstOnly, lastOnly := chains.chain, chains.firstOnly, chains.lastOnly
	chain.Reset()
	firstOnly.Reset()
	lastOnly.Reset()
	var run chainRun
	for i := 0; i < frames; i++ {
		var in float32
		if i >= burstAt && i < burstAt+50 {
			in = 0.9
		}
		chain.Process(in, in)
		run.aggregate = append(run.aggregate, chain.LimiterGainReductionDB())
		l, r := firstOnly.Process(in, in)
		run.first = append(run.first, firstOnly.LimiterGainReductionDB())
		lastOnly.Process(l, r)
		run.last = append(run.last, lastOnly.LimiterGainReductionDB())
	}
	return run
}

func windowedMax(series []float64, start int) float64 {
	m := 0.0
	for c := start; c < len(series); c++ {
		m = max(m, series[c])
	}
	return m
}

// Each limiter's reading must reach the aggregate delayed by the latency of
// the stages after it, so the reading lines up with the chain output frame it
// affects.
func TestMasterChainAlignsEachLimiterWithDownstreamLatency(t *testing.T) {
	chain := preparedChain(t, firstLimiter+lastLimiter, "master { insert = first -> last }")
	downstream := chain.stages[1].LatencyFrames()
	if downstream == 0 || chain.LatencyFrames() <= downstream {
		t.Fatalf("fixture needs a later latent stage: downstream %d total %d", downstream, chain.LatencyFrames())
	}
	run := runBurst(t, newBurstChains(t), 3000, 12000)
	for c := range run.aggregate {
		want := run.last[c]
		if c >= downstream {
			want = max(want, run.first[c-downstream])
		}
		if math.Abs(run.aggregate[c]-want) > 1e-12 {
			t.Fatalf("frame %d: aggregate %.6f dB, want %.6f dB", c, run.aggregate[c], want)
		}
	}
}

// A peak whose first-limiter reduction lands on the first delivered frame
// counts; one that lands wholly before it does not. The windowed aggregate
// must agree with the aligned oracle at both boundaries.
func TestMasterChainWindowedReductionAtBothBoundaries(t *testing.T) {
	const frames, from = 26000, 16000
	chain := preparedChain(t, firstLimiter+lastLimiter, "master { insert = first -> last }")
	total, downstream := chain.LatencyFrames(), chain.stages[1].LatencyFrames()
	start := from + total
	oldDiffers := false
	counted, excluded := false, false
	chains := newBurstChains(t)
	var positions []int
	for p := start - 12000; p < start-1500; p += 1500 {
		positions = append(positions, p)
	}
	for p := start - 1500; p < start+500; p += 29 {
		positions = append(positions, p)
	}
	for _, p := range positions {
		run := runBurst(t, chains, p, frames)
		oracle := 0.0
		for c := start; c < frames; c++ {
			v := run.last[c]
			if c >= downstream {
				v = max(v, run.first[c-downstream])
			}
			oracle = max(oracle, v)
		}
		got := windowedMax(run.aggregate, start)
		if math.Abs(got-oracle) > 1e-12 {
			t.Fatalf("burst at %d: windowed reduction %.6f dB, want %.6f dB", p, got, oracle)
		}
		undelayed := 0.0
		for c := start; c < frames; c++ {
			undelayed = max(undelayed, run.last[c], run.first[c])
		}
		if math.Abs(undelayed-oracle) > 0.5 {
			oldDiffers = true
		}
		if oracle > 6 {
			counted = true
		}
		if oracle < 0.05 {
			excluded = true
		}
	}
	if !oldDiffers {
		t.Fatal("sweep never separates aligned from unaligned readings")
	}
	if !counted {
		t.Fatal("sweep never counted a burst in the delivered interval")
	}
	if !excluded {
		t.Fatal("sweep never excluded a burst that lies wholly before the first delivered frame")
	}
}

var _ engine.StereoProcessor = (*masterChain)(nil)
