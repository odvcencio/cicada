package main

import (
	"fmt"
	"math"
	"strings"
)

// Gate is one speed limit on one step. A time or memory gate has Max, a
// real-time or ratio gate has Min or Max, and a count gate has Exact, which
// takes no margin.
type Gate struct {
	Step   string
	Metric string // median_ms, realtime, syntheses, peak_rss_mb or loudness_ratio
	Max    float64
	Min    float64
	Exact  *int
}

func exactly(n int) *int { return &n }

// gates are twice what the CI runner class (GitHub ubuntu-24.04, 4 CPUs)
// measured, or half for real time, taking the slower of the first two
// report-mode runs on 2026-11-10 (CI runs 42909300273 and its successor on the
// same pull request, medians of 5). The runners differed by up to 1.6 times on
// the parsing steps: help 4.1 and 5.5 ms, check 36.5 and 51.2 ms, a one-intent
// apply 108.5 and 162.7 ms, 50 intents 1.42 and 2.28 s, the medium song at
// 15.2x and 16.2x real time, the large song's first 16 bars at 3.66x and
// 3.69x, their audition at 422 and 447 MiB. The plan's first proposals
// (plan.cicada.speed, "Gates") were 20 ms, 100 ms, 300 ms, 3 s, 20x, 1.5x and
// 2 GB; the runner renders the medium song below the proposed 20x, so that
// gate starts at 7.6x. The count gate (one full synthesis) and the loudness
// ratio (the F1 exit criterion: a loudness render in at most 1.5 times a
// plain render; 1.30 and 1.31 measured) take no margin.
var gates = []Gate{
	{Step: "help", Metric: "median_ms", Max: 11},
	{Step: "check:large", Metric: "median_ms", Max: 102},
	{Step: "apply1:large", Metric: "median_ms", Max: 325},
	{Step: "apply50:large", Metric: "median_ms", Max: 4570},
	{Step: "render:medium", Metric: "realtime", Min: 7.6},
	{Step: "render:large", Metric: "realtime", Min: 1.83},
	{Step: "loudness:medium", Metric: "syntheses", Exact: exactly(1)},
	{Step: "loudness:medium", Metric: "loudness_ratio", Max: 1.5},
	{Step: "audition:large", Metric: "peak_rss_mb", Max: 894},
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
			if gate.Metric == "loudness_ratio" {
				value = loudnessRatio(result, results)
			}
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
// the plain render of the same size, or NaN when that render did not run.
func loudnessRatio(loudness Result, results []Result) float64 {
	_, size, _ := strings.Cut(loudness.Step, ":")
	for _, plain := range results {
		if plain.Step == "render:"+size && plain.MedianMS > 0 && plain.Bars == loudness.Bars {
			return loudness.MedianMS / plain.MedianMS
		}
	}
	return math.NaN()
}
