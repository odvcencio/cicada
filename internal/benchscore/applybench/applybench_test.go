package applybench_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/applycount"
	"m31labs.dev/cicada/internal/benchscore"
	"m31labs.dev/cicada/internal/benchscore/applybench"
	"m31labs.dev/cicada/kernel/limits"
)

// session writes reference to a fresh folder and returns it with its hot
// edits, built from a full compile of its bytes.
func session(t testing.TB, reference benchscore.Reference) (*applybench.Session, []applybench.HotEdit) {
	t.Helper()
	s, err := applybench.NewSession(t.TempDir(), reference.Source)
	if err != nil {
		t.Fatal(err)
	}
	base, err := applybench.FullCompile(s.Path, s.Source, nil)
	if err != nil {
		t.Fatalf("%s does not compile: %v", reference.Name, err)
	}
	hot, err := applybench.HotEdits(reference, base)
	if err != nil {
		t.Fatal(err)
	}
	return s, hot
}

// refusedUnderPortableLimits reports whether this profile cannot compile REF-L.
func refusedUnderPortableLimits(name string) bool {
	return limits.MaxTracks == limits.PortableMaxTracks && name == benchscore.LargeName
}

// Every hot edit applies to every reference, and the state it hands on equals
// a full compile of its result (section B.6 of the revision). A change to the
// edit path that keeps state between edits must keep this passing.
func TestHotEditsEqualAFullCompile(t *testing.T) {
	references, err := benchscore.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range references {
		t.Run(reference.Name, func(t *testing.T) {
			if refusedUnderPortableLimits(reference.Name) {
				t.Skip("REF-L exceeds the portable 16-track limit")
			}
			if testing.Short() && reference.Name == benchscore.LargeName {
				t.Skip("REF-L takes seconds; run without -short")
			}
			s, hot := session(t, reference)
			for _, edit := range hot {
				m, err := applybench.Measure(s, edit, applybench.Config{Runs: 1, Verify: true})
				if err != nil {
					t.Fatalf("%s: %v", edit.Name, err)
				}
				if edit.Intent != nil && (m.Refused != "" || m.Verified != 1) {
					t.Fatalf("%s: refused %q, verified %d runs", edit.Name, m.Refused, m.Verified)
				}
			}
		})
	}
}

// Compare must catch a state that is not a full compile of the edit's
// result: a stale source, another project, another plan.
func TestCompareRejectsAStaleState(t *testing.T) {
	reference, err := benchscore.Load(benchscore.Placement)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := session(t, reference)
	before, err := applybench.FullCompile(s.Path, s.Source, nil)
	if err != nil {
		t.Fatal(err)
	}
	edited := bytes.Replace(s.Source, []byte("at = @5.2.1 length = 4bars"), []byte("at = @5.3.1 length = 4bars"), 1)
	after, err := applybench.FullCompile(s.Path, edited, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applybench.Compare(after, after); err != nil {
		t.Fatalf("a state differs from itself: %v", err)
	}
	stale := *after
	stale.Project, stale.Plan = before.Project, before.Plan
	if err := applybench.Compare(&stale, after); err == nil {
		t.Fatal("a state holding the project of the bytes before the edit passed")
	}
	stale = *after
	stale.Score = before.Score
	if err := applybench.Compare(&stale, after); err == nil {
		t.Fatal("a state holding the score of the bytes before the edit passed")
	}
	stale = *after
	stale.Diagnostics = append(append(stale.Diagnostics[:0:0], after.Diagnostics...), after.Diagnostics...)
	if len(after.Diagnostics) > 0 {
		if err := applybench.Compare(&stale, after); err == nil {
			t.Fatal("a state with repeated diagnostics passed")
		}
	}
	plan := *after.Plan
	plan.Revision = before.Plan.Revision
	stale = *after
	stale.Plan = &plan
	if err := applybench.Compare(&stale, after); err == nil {
		t.Fatal("a plan with the revision of the bytes before the edit passed")
	}
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	text := os.Getenv(name)
	if text == "" {
		return fallback
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		t.Fatalf("%s=%q: want a count", name, text)
	}
	return n
}

// TestApplyBenchmark prints the edit-cost table: each hot edit on each
// reference, timed and counted, plus a syntax parse and a full compile of the
// reference for scale. It runs when CICADA_APPLY_BENCH=1 (make bench-apply).
//
//	CICADA_APPLY_BENCH_REFS=REF-S,REF-P   the references (default all three)
//	CICADA_APPLY_BENCH_RUNS, _WARMUPS     runs and warm-ups (default 200 and 10)
//	CICADA_APPLY_BENCH_LARGE_RUNS, _LARGE_WARMUPS   the same for REF-L alone
//	CICADA_APPLY_BENCH_VERIFY=0           skip the full-compile check of each run
//	CICADA_APPLY_BENCH_OUT=<file>         also write the table there
//
// Build with -tags cicada_counters to fill the counter columns.
func TestApplyBenchmark(t *testing.T) {
	if os.Getenv("CICADA_APPLY_BENCH") != "1" {
		t.Skip("set CICADA_APPLY_BENCH=1 (make bench-apply)")
	}
	config := applybench.Config{
		Runs: envInt(t, "CICADA_APPLY_BENCH_RUNS", applybench.Default.Runs), Warmups: envInt(t, "CICADA_APPLY_BENCH_WARMUPS", applybench.Default.Warmups),
		Verify: os.Getenv("CICADA_APPLY_BENCH_VERIFY") != "0",
	}
	large := config
	large.Runs = envInt(t, "CICADA_APPLY_BENCH_LARGE_RUNS", config.Runs)
	large.Warmups = envInt(t, "CICADA_APPLY_BENCH_LARGE_WARMUPS", config.Warmups)
	if config.Runs < 1 || large.Runs < 1 {
		t.Fatal("at least one run")
	}
	names := benchscore.Names
	if list := os.Getenv("CICADA_APPLY_BENCH_REFS"); list != "" {
		names = strings.Split(list, ",")
	}
	var references []benchscore.Reference
	for _, name := range names {
		reference, err := benchscore.Load(strings.TrimSpace(name))
		if err != nil {
			t.Fatal(err)
		}
		references = append(references, reference)
	}
	var table bytes.Buffer
	applybench.Header(&table, references, config, applycount.Enabled)
	fmt.Fprintf(&table, "Limits: %d tracks, %d voices.\n", limits.MaxTracks, limits.MaxVoices)
	if large != config {
		fmt.Fprintf(&table, "REF-L: %d runs after %d warm-ups.\n", large.Runs, large.Warmups)
	}
	var measurements []applybench.Measurement
	for _, reference := range references {
		c := config
		if reference.Name == benchscore.LargeName {
			c = large
		}
		s, err := applybench.NewSession(t.TempDir(), reference.Source)
		if err != nil {
			t.Fatal(err)
		}
		for _, step := range []func(*applybench.Session, applybench.Config) (applybench.Measurement, error){applybench.MeasureParse, applybench.MeasureCompile} {
			m, err := step(s, c)
			if err != nil {
				t.Fatal(err)
			}
			m.Reference = reference.Name
			measurements = append(measurements, m)
		}
		if refusedUnderPortableLimits(reference.Name) {
			// The edits are refused by the first compile; time one refusal each.
			c = applybench.Config{Runs: min(c.Runs, 3), Warmups: min(c.Warmups, 1)}
			fmt.Fprintf(&table, "REF-L under portable limits: every edit is refused (16-track limit); %d runs after %d warm-ups each.\n", c.Runs, c.Warmups)
		}
		base, err := applybench.FullCompile(s.Path, s.Source, nil)
		var hot []applybench.HotEdit
		if err == nil {
			hot, err = applybench.HotEdits(reference, base)
		} else if refusedUnderPortableLimits(reference.Name) {
			hot, err = largeEditsWithoutAPlan(reference)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, edit := range hot {
			m, err := applybench.Measure(s, edit, c)
			if err != nil {
				t.Fatal(err)
			}
			m.Reference = reference.Name
			measurements = append(measurements, m)
			t.Logf("%s %s: median %v, p95 %v", reference.Name, edit.Name, m.Median, m.P95)
		}
	}
	fmt.Fprintln(&table)
	applybench.Table(&table, measurements)
	out := io.Writer(os.Stdout)
	if path := os.Getenv("CICADA_APPLY_BENCH_OUT"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		out = io.MultiWriter(os.Stdout, file)
	}
	if _, err := out.Write(table.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// largeEditsWithoutAPlan names REF-L's hot edits when the build cannot compile
// it (portable limits): the same entities, with the 16 notes read from a native compile's
// plan replaced by the first 16 notes the source spells.
func largeEditsWithoutAPlan(reference benchscore.Reference) ([]applybench.HotEdit, error) {
	return applybench.HotEditsFromSource(reference)
}
