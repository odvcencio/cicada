package main

import (
	"fmt"
	"math"
	"strings"
)

// Gate is one speed limit on one step. A time or memory gate has Max, a
// real-time gate has Min, and a count gate has Exact, which takes no margin.
type Gate struct {
	Step   string
	Metric string // median_ms, realtime, syntheses or peak_rss_mb
	Max    float64
	Min    float64
	Exact  *int
}

func exactly(n int) *int { return &n }

// gates are twice what the CI runner class (GitHub ubuntu-24.04, 4 CPUs)
// measured, or half for real time, taking the slower runner (medians of 5,
// pull request #318, 2026-11-10). Runner instances differed by up to 1.6 times
// on the parsing steps and by 1.4 times on renders: help 4.1 to 5.5 ms, check
// 36.5 to 51.2 ms, a one-intent apply 108.5 to 162.7 ms, 50 intents 1.42 to
// 2.28 s, the medium song at 11.4x to 19.7x real time. The large song's
// render and audition cover its first 8 bars (16 bars made the job take six
// minutes on a slow runner): 4.44x real time and 233 MiB on a fast runner,
// where its first 16 bars ran at 3.69x against 2.76x on a slow one, so the
// slow runner's 8 bars are taken as 4.44 * 2.76 / 3.69 = 3.3x. The plan's
// first proposals (plan.cicada.speed, "Gates") were 20 ms, 100 ms, 300 ms,
// 3 s, 20x, 1.5x and 2 GB; the runners render the medium song below 20x, so
// that gate starts at 5.7x. The count gate (one full synthesis) takes no
// margin and is the hard gate for the loudness render. The loudness render's
// time over a plain render's (the plan's F1 exit criterion, at most 1.5) is
// reported, not gated: runner samples gave 1.24 to 1.39, too close to 1.5 for
// a gate that must not fail by chance, and twice the measured value would be
// no check at all.
var gates = []Gate{
	{Step: "help", Metric: "median_ms", Max: 11},
	{Step: "check:large", Metric: "median_ms", Max: 102},
	{Step: "apply1:large", Metric: "median_ms", Max: 325},
	{Step: "apply50:large", Metric: "median_ms", Max: 4570},
	{Step: "render:medium", Metric: "realtime", Min: 5.7},
	{Step: "render:large", Metric: "realtime", Min: 1.65},
	{Step: "loudness:medium", Metric: "syntheses", Exact: exactly(1)},
	{Step: "audition:large", Metric: "peak_rss_mb", Max: 466},
}

// GateOutcome is a gate checked against a measured value.
type GateOutcome struct {
	Step   string  `json:"step"`
	Metric string  `json:"metric"`
	Limit  string  `json:"limit"`
	Value  float64 `json:"value"`
	Passed bool    `json:"passed"`
}

func (g GateOutcome) String() string {
	verdict := "pass"
	if !g.Passed {
		verdict = "FAIL"
	}
	return fmt.Sprintf("%-4s %-16s %-11s %10.3f, limit %s", verdict, g.Step, g.Metric, g.Value, g.Limit)
}

// checkGates checks every gate whose step ran. It reports whether all held.
func checkGates(results []Result) ([]GateOutcome, bool) {
	passed := true
	var outcomes []GateOutcome
	for _, gate := range gates {
		for _, result := range results {
			if result.Step != gate.Step {
				continue
			}
			value := metric(result, gate.Metric)
			outcome := GateOutcome{Step: gate.Step, Metric: gate.Metric, Value: value}
			switch {
			case gate.Exact != nil:
				outcome.Limit = fmt.Sprintf("exactly %d", *gate.Exact)
				outcome.Passed = value == float64(*gate.Exact)
			case gate.Min > 0:
				outcome.Limit = fmt.Sprintf("at least %g", gate.Min)
				outcome.Passed = value >= gate.Min
			default:
				outcome.Limit = fmt.Sprintf("at most %g", gate.Max)
				outcome.Passed = value <= gate.Max
			}
			if math.IsNaN(value) {
				outcome.Passed = false
			}
			passed = passed && outcome.Passed
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes, passed
}

func metric(r Result, name string) float64 {
	switch name {
	case "median_ms":
		return r.MedianMS
	case "realtime":
		return r.Realtime
	case "syntheses":
		return float64(r.Syntheses)
	case "peak_rss_mb":
		return r.PeakRSSMB
	}
	return math.NaN()
}

// loudnessRatio is a loudness render's median wall time over the median of
// the plain render of the same size, or NaN when that render did not run. It
// is reported, not gated.
func loudnessRatio(loudness Result, results []Result) float64 {
	_, size, _ := strings.Cut(loudness.Step, ":")
	for _, plain := range results {
		if plain.Step == "render:"+size && plain.MedianMS > 0 && plain.Bars == loudness.Bars {
			return loudness.MedianMS / plain.MedianMS
		}
	}
	return math.NaN()
}
