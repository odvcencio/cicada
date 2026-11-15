//go:build !tinygo && !js

package render

import (
	"bytes"
	"runtime"
	"slices"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
	"m31labs.dev/cicada/notation"
)

// noteLanes picks each serial_reason code from the counts a render gathered;
// the table covers every code in SerialReasons and the parallel case.
func TestNoteLanesReasons(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range []struct {
		name                           string
		requested, jobs, lanes, tracks int
		counts                         laneCounts
		why, want                      string
	}{
		{"no lanes asked for", 0, 1, 1, 4, laneCounts{segments: 9}, "", SerialJobs},
		{"auto chose one lane", AutoJobs, 1, 1, 4, laneCounts{segments: 9}, "", SerialJobs},
		{"one track", 4, 4, 1, 1, laneCounts{segments: 9}, "", SerialOneTrack},
		{"graph LFO", 4, 4, 1, 4, laneCounts{segments: 9}, SerialGraphLFO, SerialGraphLFO},
		{"replayed mix", 4, 4, 1, 4, laneCounts{}, SerialReplay, SerialReplay},
		{"no processor free", 4, 4, 4, 4, laneCounts{segments: 9, starved: 9}, "", SerialNoProcessor},
		{"some segments starved, the rest light", 4, 4, 4, 4, laneCounts{segments: 9, starved: 2}, "", SerialNoProcessor},
		{"every segment light", 4, 4, 4, 4, laneCounts{segments: 9}, "", SerialLight},
		{"ran on lanes", 4, 4, 4, 4, laneCounts{segments: 9, parallel: 3, peak: 3}, "", ""},
	} {
		var r Report
		r.noteLanes(c.requested, c.jobs, c.lanes, c.tracks, c.counts, c.why)
		if r.SerialReason != c.want {
			t.Errorf("%s: serial_reason %q, want %q", c.name, r.SerialReason, c.want)
		}
		if c.want != "" && !slices.Contains(SerialReasons, c.want) {
			t.Errorf("%s: %q is not in SerialReasons", c.name, c.want)
		}
		covered[c.want] = true
	}
	for _, code := range SerialReasons {
		if !covered[code] {
			t.Errorf("no case gives %q", code)
		}
	}
}

const reasonOneTrackScore = `cicada 2
instrument lead { voice mono { out = sine(pitch) * 0.3 } }
track voice lead {}
pattern p { c4 e4 g4 . }
scene main { voice = p }
song { main }
`

// The modulator routes the song to the scheduled engine, whose graph-LFO
// clock is shared by every graph voice and advances on the render goroutine.
// The direct mixer gives each graph voice a clock of its own, so it needs no
// such rule.
const reasonGraphLFOScore = `cicada 2
lfo drift { wave = triangle rate = 8bars }
instrument lead { voice mono { out = sine(pitch) * (0.5 + 0.4 * lfo(1Hz, triangle, note)) } }
track voice lead {}
track bed acid {}
pattern p { c4 e4 g4 . }
mod drift -> bed.pan depth = 0.5
scene main { voice = p bed = p }
song { main }
`

func reasonScore(t *testing.T, source string) *notation.Score {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	return score
}

func renderReason(t *testing.T, score *notation.Score, opts Options) Report {
	t.Helper()
	var out bytes.Buffer
	report, err := WAV(score, opts, &out)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

// Each serial_reason code that a whole render can reach without depending on
// the machine's speed: one_track, graph_lfo, replay and no_processor (jobs
// is TestZeroJobsRendersSerially's). light depends on measured costs; the
// mapping is TestNoteLanesReasons's and the pool's decision
// renderlanes.TestLightSegmentsStaySerial's.
func TestSerialReasonsFromRenders(t *testing.T) {
	t.Run("one_track", func(t *testing.T) {
		defer renderlanes.ForceEveryLane()()
		report := renderReason(t, reasonScore(t, reasonOneTrackScore), Options{SampleRate: 48000, Bars: 1, Jobs: 4})
		if report.Lanes != 1 || report.SerialReason != SerialOneTrack {
			t.Fatalf("%+v", report.LaneReport)
		}
	})
	t.Run("graph_lfo", func(t *testing.T) {
		defer renderlanes.ForceEveryLane()()
		score := reasonScore(t, reasonGraphLFOScore)
		if !UsesScheduledEngine(score, Options{}) {
			t.Fatal("the graph-LFO score must render on the scheduled engine")
		}
		report := renderReason(t, score, Options{SampleRate: 48000, Bars: 1, Jobs: 4})
		if report.Lanes != 1 || report.SerialReason != SerialGraphLFO {
			t.Fatalf("%+v", report.LaneReport)
		}
	})
	t.Run("replay", func(t *testing.T) {
		for _, c := range []struct {
			name  string
			score *notation.Score
		}{{"direct mixer", cacheScoreVariant(t)}, {"scheduled engine", scheduledCacheScore(t, masterCacheChain)}} {
			cache := newProductionMasterCache(t)
			opts := Options{SampleRate: 48000, Bars: 1, TailSec: .1, Jobs: 4, MasterCache: cache}
			first := renderReason(t, c.score, opts)
			opts.MasterInputGainDB = 3
			second := renderReason(t, c.score, opts)
			if cache.Replays() != 1 || first.SerialReason == SerialReplay || second.SerialReason != SerialReplay {
				t.Fatalf("%s: %d replays; capture %+v; replay %+v", c.name, cache.Replays(), first.LaneReport, second.LaneReport)
			}
		}
	})
	t.Run("no_processor", func(t *testing.T) {
		// With GOMAXPROCS 1 the process budget has no worker lane to give,
		// so every segment forced onto the lanes stays on the caller.
		defer renderlanes.ForceEveryLane()()
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
		dir, clipScore := audioRenderFixture(t, true)
		chorus, _ := driftScore(t, "cicada-chorus")
		for _, c := range []struct {
			name  string
			score *notation.Score
			opts  Options
		}{
			{"scheduled engine", clipScore, Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Jobs: 4}},
			{"direct mixer", chorus, Options{SampleRate: 48000, Bits: 24, Bars: 2, Jobs: 4}},
		} {
			report := renderReason(t, c.score, c.opts)
			if report.Lanes < 2 || report.ParallelSegments != 0 || report.SerialReason != SerialNoProcessor {
				t.Fatalf("%s: %+v", c.name, report.LaneReport)
			}
		}
	})
}
