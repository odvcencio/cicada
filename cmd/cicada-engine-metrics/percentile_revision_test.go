package main

import (
	"bytes"
	"fmt"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

func TestNearestRankLargeObservationCount(t *testing.T) {
	o := options{blocks: 22_000_000, runs: 1, warmup: 1, bars: 2, filter: "scene_every_bar=false"}
	if err := o.validate(); err != nil {
		t.Fatal(err)
	}
	if got := nearestRankIndex(o.blocks, 99); got != 21_779_999 {
		t.Fatalf("p99 index = %d, want 21779999", got)
	}
}

func TestNearestRankIndexBoundaries(t *testing.T) {
	for _, tc := range []struct{ count, percent, want int }{
		{1, 50, 0}, {1, 99, 0}, {2, 50, 0}, {3, 50, 1}, {3, 99, 2},
		{100, 99, 98}, {101, 50, 50}, {101, 99, 99}, {22_000_000, 50, 10_999_999},
	} {
		if got := nearestRankIndex(tc.count, tc.percent); got != tc.want {
			t.Fatalf("count=%d p%d: index=%d, want %d", tc.count, tc.percent, got, tc.want)
		}
	}
	var wantP99 int64 = 9_131_138_316_486_228_048
	if strconv.IntSize == 32 {
		wantP99 = 2_126_008_810
	}
	if got := int64(nearestRankIndex(math.MaxInt, 99)); got != wantP99 {
		t.Fatalf("MaxInt p99 index=%d, want %d", got, wantP99)
	}
	if got := nearestRankIndex(math.MaxInt, 100); got != math.MaxInt-1 {
		t.Fatalf("MaxInt p100 index=%d, want %d", got, math.MaxInt-1)
	}
	if got := nearestRank([]int64{10, 20, 30, 40}, 50); got != 20 {
		t.Fatalf("observed median=%d, want 20", got)
	}
}

func TestMachineMetricReportsInheritedGCPolicy(t *testing.T) {
	originalPercent := debug.SetGCPercent(37)
	defer debug.SetGCPercent(originalPercent)
	memoryLimit := debug.SetMemoryLimit(-1)
	for _, percent := range []int{37, -1} {
		debug.SetGCPercent(percent)
		var output bytes.Buffer
		writeMachineMetric(&output, "test")
		for _, field := range []string{
			"native_gc_percent=-1",
			fmt.Sprintf("offline_gc_percent=%d", percent),
			fmt.Sprintf("gomemlimit_bytes=%d", memoryLimit),
		} {
			if !strings.Contains(output.String(), field) {
				t.Fatalf("missing %q: %s", field, output.String())
			}
		}
		if got := debug.SetMemoryLimit(-1); got != memoryLimit {
			t.Fatalf("metadata changed inherited memory limit: %d -> %d", memoryLimit, got)
		}
	}
}
