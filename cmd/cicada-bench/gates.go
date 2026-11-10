package main

import (
	"fmt"
	"math"
)

// Gate is one speed limit on one step. A time or memory gate has Max, a
// real-time gate has Min, and a count gate has Exact, which takes no margin.
//
// The numbers are the plan's first proposals (plan.cicada.speed.2026-11-10,
// "Gates"), to be set to twice the medians measured on the CI runner.
type Gate struct {
	Step   string
	Metric string // median_ms, realtime, syntheses or peak_rss_mb
	Max    float64
	Min    float64
	Exact  *int
}

func exactly(n int) *int { return &n }

var gates = []Gate{
	{Step: "help", Metric: "median_ms", Max: 20},
	{Step: "check:large", Metric: "median_ms", Max: 100},
	{Step: "apply1:large", Metric: "median_ms", Max: 300},
	{Step: "apply50:large", Metric: "median_ms", Max: 3000},
	{Step: "render:medium", Metric: "realtime", Min: 20},
	{Step: "render:large", Metric: "realtime", Min: 1.5},
	{Step: "loudness:medium", Metric: "syntheses", Exact: exactly(1)},
	{Step: "audition:large", Metric: "peak_rss_mb", Max: 2048},
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
