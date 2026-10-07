package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestSourceChainsRenderAudiblyAcrossExactTickBoundaries(t *testing.T) {
	source, err := os.ReadFile("../examples/section-chain.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(ds)
		}
	}
	var first []byte
	for _, block := range []int{64, 4096} {
		var out bytes.Buffer
		report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Bars: 1, Block: block}, &out)
		if err != nil {
			t.Fatal(err)
		}
		if report.OutputPeak < .001 {
			t.Fatal("source chain rendered silence")
		}
		if block == 64 {
			assertSceneEngineMatchesWAV(t, score, out.Bytes(), 48000, 0, 96000)
		}
		if first == nil {
			first = append([]byte(nil), out.Bytes()...)
		} else if !bytes.Equal(first, out.Bytes()) {
			t.Fatal("chain depends on offline block size")
		}
	}
	// Check every accepted section has audible energy, including the off-grid
	// 240-tick start of its 320-tick triplet cells.
	for _, start := range []int{0, 6000, 14000, 22000, 30000, 42000} {
		var energy float64
		for frame := start + 200; frame < start+1000; frame++ {
			value := math.Float32frombits(binary.LittleEndian.Uint32(first[44+frame*8:]))
			energy += float64(value * value)
		}
		if energy < 1e-6 {
			t.Fatalf("silent section at frame %d", start)
		}
	}
}

func TestOfflineChainSwitchesAllocationFree(t *testing.T) {
	source, err := os.ReadFile("../examples/section-chain.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, _ := notation.Parse(source)
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	tracks, err := compileTracks(score, p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range score.Tracks[0].Chain {
		tracks[0].chain = append(tracks[0].chain, ref.Text)
	}
	if n := testing.AllocsPerRun(1000, func() {
		if err := advanceRenderChains(tracks, tracks[0].chainDue); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("offline chain switch allocations %g", n)
	}
}
