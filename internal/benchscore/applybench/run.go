package applybench

import (
	"fmt"
	"runtime"
	"slices"
	"time"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/internal/applycount"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Config says how many times each edit runs.
type Config struct {
	Warmups, Runs int
	// Verify compares the state of every run, warm-ups included, with a full
	// compile of its result. It runs outside the timed region.
	Verify bool
	// NoBase applies each edit without a kept state (Options.Base nil), as a
	// host that keeps none does: Apply then builds the base state itself. By
	// default every run starts from the compiled state of the reference,
	// built once before the runs, as the edit service keeps it between edits.
	NoBase bool
}

// Default is the revision's method (section B.7): 200 runs after 10 warm-ups.
var Default = Config{Warmups: 10, Runs: 200, Verify: true}

// Work is what one Apply did, counted.
type Work struct {
	applycount.Counts
	// Compiles counts full compiles through Options.Compiler; ProjectParses
	// and Diagnoses count the other host callbacks (Hooks).
	Compiles, ProjectParses, Diagnoses int
}

// Measurement is one hot edit on one reference.
type Measurement struct {
	Reference, Edit string
	Note            string // from HotEdit.Note, or why it was not measured
	Runs, Warmups   int
	Median, P95     time.Duration
	Min, Max        time.Duration
	// Work is what each measured Apply did. Every measured run must do the
	// same work; Measure fails when two differ. Warm-ups may differ.
	Work Work
	// Counted is false in a build without the cicada_counters tag, where
	// Work holds only the host callbacks.
	Counted bool
	// Verified counts runs whose state equalled a full compile of the result.
	Verified int
	// Refused is the Apply error when the reference refuses the edit (REF-L
	// under GOOS=js exceeds the portable track limit); nothing is timed then.
	Refused string
	// RefusedAfter is how long the refusal took, the median of the runs.
	RefusedAfter time.Duration
}

// Measure applies hot to the session's score config.Warmups + config.Runs
// times, each time to the same base bytes with fresh service options, as one
// Studio request would: it times edit.Apply alone, reads the counters around
// it, and then (outside the timing) checks the produced state.
func Measure(session *Session, hot HotEdit, config Config) (Measurement, error) {
	m := Measurement{Edit: hot.Name, Note: hot.Note, Runs: config.Runs, Warmups: config.Warmups, Counted: applycount.Enabled}
	if hot.Intent == nil {
		m.Note = hot.Missing
		return m, nil
	}
	base := session.Source
	revision := edit.Revision(base)
	var kept *project.State
	if !config.NoBase {
		var err error
		if kept, err = KeptState(session.Path, base); err != nil && !refusedReference(err) {
			return m, err
		}
	}
	var times []time.Duration
	var refusals []time.Duration
	for run := range config.Warmups + config.Runs {
		hooks := &Hooks{}
		opts, compiler, err := session.Options(base, hooks)
		if err != nil {
			return m, err
		}
		WithBase(&opts, compiler, kept)
		env := edit.Envelope{Version: edit.EnvelopeVersion, Revision: revision, Intents: []edit.Intent{hot.Intent}}
		before := applycount.Snapshot()
		start := time.Now()
		result, err := edit.Apply(base, env, opts)
		elapsed := time.Since(start)
		work := Work{Counts: applycount.Snapshot().Sub(before), Compiles: compiler.Compiles, ProjectParses: hooks.ProjectParses, Diagnoses: hooks.Diagnoses}
		switch {
		case run == config.Warmups:
			m.Work = work
		case run > config.Warmups && work != m.Work:
			return m, fmt.Errorf("%s: measured run %d did different work from the first: %+v against %+v", hot.Name, run-config.Warmups, work, m.Work)
		}
		if err != nil {
			if m.Refused == "" && run > 0 {
				return m, fmt.Errorf("%s: refused only from run %d: %v", hot.Name, run, err)
			}
			m.Refused = err.Error()
			if run >= config.Warmups {
				refusals = append(refusals, elapsed)
			}
			continue
		}
		if m.Refused != "" {
			return m, fmt.Errorf("%s: applied on run %d after a refusal", hot.Name, run)
		}
		if result.Unchanged {
			return m, fmt.Errorf("%s: the edit changed nothing", hot.Name)
		}
		if run >= config.Warmups {
			times = append(times, elapsed)
		}
		if config.Verify {
			if err := verify(session, result, compiler); err != nil {
				return m, fmt.Errorf("%s run %d: the edit's state is not a full compile of its result: %w", hot.Name, run, err)
			}
			if err := verifyKept(session, kept); err != nil {
				return m, fmt.Errorf("%s run %d: the kept state changed: %w", hot.Name, run, err)
			}
			m.Verified++
		}
	}
	if len(refusals) > 0 {
		slices.Sort(refusals)
		m.RefusedAfter = refusals[len(refusals)/2]
		return m, nil
	}
	slices.Sort(times)
	m.Min, m.Max = times[0], times[len(times)-1]
	m.Median = percentile(times, 50)
	m.P95 = percentile(times, 95)
	return m, nil
}

// verify checks the state result came with against a full compile of
// result.Source (section B.6). Today the state an Apply hands on is the
// service compiler's latest compile, which Apply ran on result.Source, and
// result.Plan must be that compile's plan.
func verify(session *Session, result *edit.Result, compiler *Compiler) error {
	produced := compiler.Last
	if produced == nil || result.Plan != produced.Plan {
		return fmt.Errorf("the result's plan is not the latest compile's")
	}
	if result.State == nil {
		return fmt.Errorf("the result carries no state")
	}
	if plan, err := result.State.Plan(); err != nil || plan != result.Plan {
		return fmt.Errorf("the result's state does not hold the result's plan")
	}
	fresh, err := FullCompile(session.Path, result.Source, overrides(result.Files))
	if err != nil {
		return fmt.Errorf("full compile of the result: %w", err)
	}
	return Compare(produced, fresh)
}

// verifyKept checks that the state every run started from still equals a full
// compile of its bytes: no edit changed what it shares with its readers.
func verifyKept(session *Session, kept *project.State) error {
	if kept == nil {
		return nil
	}
	produced, err := StateOf(kept)
	if err != nil {
		return err
	}
	fresh, err := FullCompile(session.Path, kept.Source(), nil)
	if err != nil {
		return fmt.Errorf("full compile of the kept bytes: %w", err)
	}
	return Compare(produced, fresh)
}

// refusedReference reports a reference this build cannot compile (REF-L
// under js, beyond the portable track limit): its edits are timed to their
// refusal, without a kept state.
func refusedReference(err error) bool {
	return err != nil && runtime.GOOS == "js"
}

func overrides(files []edit.File) map[string][]byte {
	if len(files) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(files))
	for _, file := range files {
		out[file.Path] = file.After
	}
	return out
}

// MeasureParse times the syntax parse of the session's score alone
// (notation.ParseTree, which every compile starts with) and counts it.
func MeasureParse(session *Session, config Config) (Measurement, error) {
	return measureStep(session, "syntax parse", config, func() error {
		_, _, err := notation.ParseTree(session.Source)
		return err
	})
}

// MeasureCompile times one full compile of the session's score (FullCompile:
// what the edit service does to every candidate) and counts it.
func MeasureCompile(session *Session, config Config) (Measurement, error) {
	return measureStep(session, "full compile", config, func() error {
		_, err := FullCompile(session.Path, session.Source, nil)
		return err
	})
}

func measureStep(session *Session, name string, config Config, step func() error) (Measurement, error) {
	m := Measurement{Edit: name, Runs: config.Runs, Warmups: config.Warmups, Counted: applycount.Enabled}
	var times []time.Duration
	for run := range config.Warmups + config.Runs {
		before := applycount.Snapshot()
		start := time.Now()
		err := step()
		elapsed := time.Since(start)
		work := Work{Counts: applycount.Snapshot().Sub(before)}
		switch {
		case run == config.Warmups:
			m.Work = work
		case run > config.Warmups && work != m.Work:
			return m, fmt.Errorf("%s: measured run %d did different work from the first", name, run-config.Warmups)
		}
		if err != nil {
			m.Refused = err.Error()
		}
		if run >= config.Warmups {
			times = append(times, elapsed)
		}
	}
	slices.Sort(times)
	m.Min, m.Max = times[0], times[len(times)-1]
	m.Median, m.P95 = percentile(times, 50), percentile(times, 95)
	if m.Refused != "" {
		m.RefusedAfter, m.Median, m.P95 = m.Median, 0, 0
	}
	return m, nil
}

// percentile returns the nearest-rank percentile of sorted durations.
func percentile(sorted []time.Duration, p int) time.Duration {
	rank := (p*len(sorted) + 99) / 100
	return sorted[max(0, min(len(sorted)-1, rank-1))]
}
