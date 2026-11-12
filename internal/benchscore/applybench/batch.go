package applybench

import (
	"fmt"
	"slices"
	"time"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

// DrumToggles returns n togglestep intents on distinct drum hits the session's
// score writes (lanes as authored, hits in order), as a batch an agent sends
// through cicada apply.
func DrumToggles(s *Session, n int) ([]edit.Intent, error) {
	score, _, err := project.LoadScore(s.Path, map[string][]byte{s.Path: s.Source})
	if err != nil {
		return nil, err
	}
	var intents []edit.Intent
	for _, pattern := range score.Patterns {
		for _, lane := range pattern.Lanes {
			for step := range lane.Hits {
				if len(intents) < n {
					intents = append(intents, &edit.ToggleStep{Entity: edit.EntityID(fmt.Sprintf("step:%s/%s/%d", pattern.Name, lane.Name, step)), Shared: "definition"})
				}
			}
		}
	}
	if len(intents) < n {
		return nil, fmt.Errorf("the score writes %d drum hits, want %d", len(intents), n)
	}
	return intents, nil
}

// MeasureBatch times edit.Apply of one envelope holding intents on the
// session's score, as Measure times one intent: each run from the kept state
// of the score (or none with config.NoBase), with fresh service options.
func MeasureBatch(s *Session, name string, intents []edit.Intent, config Config) (Measurement, error) {
	m := Measurement{Edit: name, Runs: config.Runs, Warmups: config.Warmups}
	var kept *project.State
	if !config.NoBase {
		var err error
		if kept, err = KeptState(s.Path, s.Source); err != nil {
			return m, err
		}
	}
	env := edit.Envelope{Version: edit.EnvelopeVersion, Revision: edit.Revision(s.Source), Intents: intents}
	var times []time.Duration
	for run := range config.Warmups + config.Runs {
		opts, compiler, err := s.Options(s.Source, &Hooks{})
		if err != nil {
			return m, err
		}
		WithBase(&opts, compiler, kept)
		start := time.Now()
		result, err := edit.Apply(s.Source, env, opts)
		elapsed := time.Since(start)
		if err != nil {
			return m, fmt.Errorf("%s: %w", name, err)
		}
		if config.Verify {
			if err := verify(s, result, compiler); err != nil {
				return m, fmt.Errorf("%s run %d: %w", name, run, err)
			}
			m.Verified++
		}
		if run >= config.Warmups {
			times = append(times, elapsed)
		}
	}
	slices.Sort(times)
	m.Min, m.Max = times[0], times[len(times)-1]
	m.Median, m.P95 = percentile(times, 50), percentile(times, 95)
	return m, nil
}
