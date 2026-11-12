// Command cicada-score-probe times the score engine on one score and links
// nothing else, so its WebAssembly build measures the engine's size.
//
//	cicada-score-probe <score> <pattern> [runs]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/scoreengine"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: cicada-score-probe <score> <pattern> [runs]")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	pattern := os.Args[2]
	runs := 200
	if len(os.Args) > 3 {
		runs, _ = strconv.Atoi(os.Args[3])
	}
	snapshot := scoreengine.Snapshot{Entry: "score.cicada", Files: map[string][]byte{"score.cicada": data}}
	var opened []time.Duration
	var engine *scoreengine.Engine
	for i := 0; i < 20; i++ {
		start := time.Now()
		engine, err = scoreengine.Open(snapshot, scoreengine.Options{})
		opened = append(opened, time.Since(start))
		if err != nil {
			fail(err)
		}
	}
	report := map[string]any{"open": stats(opened)}
	step := edits.EntityID("step:" + pattern + "/0")
	for _, c := range []struct {
		name   string
		intent edits.Intent
	}{
		{"togglestep", &edits.ToggleStep{Entity: step}},
		{"setpitch", &edits.SetPitch{Entity: step, Pitch: 62}},
	} {
		var times []time.Duration
		source := engine.Source("score.cicada")
		for i := 0; i < runs+10; i++ {
			fresh, err := scoreengine.Open(snapshot, scoreengine.Options{})
			if err != nil {
				fail(err)
			}
			_ = source
			start := time.Now()
			_, err = fresh.Apply(edits.Envelope{Version: 1, Revision: fresh.Revision(), Intents: []edits.Intent{c.intent}}, "")
			if i >= 10 {
				times = append(times, time.Since(start))
			}
			if err != nil {
				fail(fmt.Errorf("%s: %w", c.name, err))
			}
		}
		report[c.name] = stats(times)
	}
	out, _ := json.Marshal(report)
	fmt.Println(string(out))
}

func stats(times []time.Duration) map[string]float64 {
	sorted := append([]time.Duration(nil), times...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	p95 := sorted[(len(sorted)*95+99)/100-1]
	return map[string]float64{"runs": float64(len(sorted)), "median_ms": ms(sorted[len(sorted)/2]), "p95_ms": ms(p95), "max_ms": ms(sorted[len(sorted)-1])}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
