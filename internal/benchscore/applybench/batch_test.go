package applybench_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"m31labs.dev/cicada/internal/benchscore/applybench"
)

// songSession writes the speed plan's example score
// (examples/songs/anywhere-you-are.cicada, 15,557 bytes) and its manifest to
// a fresh folder.
func songSession(t testing.TB) *applybench.Session {
	t.Helper()
	if runtime.GOOS == "js" {
		// As REF-L: the portable limits refuse it ("project exceeds 32
		// allocated voices"), so it is measured natively only.
		t.Skip("the portable limits refuse anywhere-you-are under js")
	}
	dir := t.TempDir()
	songs := filepath.Join("..", "..", "..", "examples", "songs")
	score, err := os.ReadFile(filepath.Join(songs, "anywhere-you-are.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(songs, "cicada.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cicada.mod"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := applybench.NewSession(dir, score)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSongBenchmark prints the speed plan's criteria on its example score:
// one intent and a batch of 50 in one edit.Apply, from the kept state, timed
// as TestApplyBenchmark times the hot edits. It runs when CICADA_APPLY_BENCH=1.
func TestSongBenchmark(t *testing.T) {
	if os.Getenv("CICADA_APPLY_BENCH") != "1" {
		t.Skip("set CICADA_APPLY_BENCH=1 (make bench-apply)")
	}
	config := applybench.Config{
		Runs: envInt(t, "CICADA_APPLY_BENCH_RUNS", applybench.Default.Runs), Warmups: envInt(t, "CICADA_APPLY_BENCH_WARMUPS", applybench.Default.Warmups),
		Verify: os.Getenv("CICADA_APPLY_BENCH_VERIFY") != "0", NoBase: os.Getenv("CICADA_APPLY_BENCH_NO_BASE") == "1",
	}
	s := songSession(t)
	intents, err := applybench.DrumToggles(s, 50)
	if err != nil {
		t.Fatal(err)
	}
	var table bytes.Buffer
	fmt.Fprintf(&table, "anywhere-you-are (%d bytes): %d runs after %d warm-ups, kept state %t.\n\n", len(s.Source), config.Runs, config.Warmups, !config.NoBase)
	var measurements []applybench.Measurement
	for _, batch := range []struct {
		name    string
		intents int
	}{{"1 togglestep", 1}, {"50 togglesteps", 50}} {
		m, err := applybench.MeasureBatch(s, batch.name, intents[:batch.intents], config)
		if err != nil {
			t.Fatal(err)
		}
		m.Reference = "anywhere-you-are"
		measurements = append(measurements, m)
	}
	applybench.Table(&table, measurements)
	if _, err := os.Stdout.Write(table.Bytes()); err != nil {
		t.Fatal(err)
	}
}

// The batch helpers work on the song: 50 distinct drum hits, and a batch
// that applies.
func TestSongBatchApplies(t *testing.T) {
	s := songSession(t)
	intents, err := applybench.DrumToggles(s, 50)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applybench.MeasureBatch(s, "50 togglesteps", intents, applybench.Config{Runs: 1, Verify: true}); err != nil {
		t.Fatal(err)
	}
}
