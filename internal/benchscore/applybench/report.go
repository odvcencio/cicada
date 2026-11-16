package applybench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"m31labs.dev/cicada/internal/benchscore"
)

// Header writes what a table was measured on: the Go version, the target,
// GOMAXPROCS, the counters' state, the method, and each reference's size and
// SHA-256. Timings need the machine and its load beside them, which the
// caller adds.
func Header(out io.Writer, references []benchscore.Reference, config Config, counted bool) {
	counters := "off (build with -tags cicada_counters to count)"
	if counted {
		counters = "on"
	}
	fmt.Fprintf(out, "Edit cost per edit.Apply with Studio's service options: %s %s/%s, GOMAXPROCS=%d, counters %s.\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), counters)
	fmt.Fprintf(out, "Method: %d runs after %d warm-ups, each on the reference's own bytes with fresh options; median and 95th percentile (nearest rank) of edit.Apply alone; the state of every run checked against a full compile of its result: %t.\n",
		config.Runs, config.Warmups, config.Verify)
	for _, reference := range references {
		sum := sha256.Sum256(reference.Source)
		fmt.Fprintf(out, "%s: %d bytes, SHA-256 %s.\n", reference.Name, len(reference.Source), hex.EncodeToString(sum[:]))
	}
}

// Table writes measurements as a Markdown table: times in milliseconds, then
// the work of one Apply.
func Table(out io.Writer, measurements []Measurement) {
	fmt.Fprintln(out, "| Ref | Edit | Median ms | p95 ms | Syntax parses | Bytes parsed | Items lowered | notation.Validate | Entities compiled | ValidateProject | Engine builds | Edit plans | Revision hashes | Bytes hashed | Compiles | Project parses | Diagnoses | Verified | Note |")
	fmt.Fprintln(out, "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|")
	for _, m := range measurements {
		median, p95 := ms(m.Median), ms(m.P95)
		note := m.Note
		switch {
		case m.Refused != "":
			median, p95 = "refused", "refused"
			note = strings.TrimSpace(fmt.Sprintf("%s refused after %s ms (median): %s", note, ms(m.RefusedAfter), firstLine(m.Refused)))
		case m.Runs == 0 || m.Median == 0 && m.P95 == 0:
			median, p95 = "-", "-"
		}
		counts := []string{"-", "-", "-", "-", "-", "-", "-", "-", "-", "-"}
		if m.Counted {
			w := m.Work
			counts = []string{
				fmt.Sprint(w.SyntaxParses), fmt.Sprint(w.BytesParsed), fmt.Sprint(w.ItemsLowered), fmt.Sprint(w.NotationValidations),
				fmt.Sprint(w.EntitiesCompiled), fmt.Sprint(w.ProjectValidations), fmt.Sprint(w.EngineBuilds), fmt.Sprint(w.EditPlans),
				fmt.Sprint(w.RevisionHashes), fmt.Sprint(w.BytesHashed),
			}
		}
		verified := fmt.Sprint(m.Verified)
		if m.Verified == 0 {
			verified = "-"
		}
		fmt.Fprintf(out, "| %s | %s | %s | %s | %s | %d | %d | %d | %s | %s |\n", m.Reference, m.Edit, median, p95, strings.Join(counts, " | "),
			m.Work.Compiles, m.Work.ProjectParses, m.Work.Diagnoses, verified, note)
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d)/float64(time.Millisecond)) }

func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	if len(line) > 120 {
		line = line[:120] + "..."
	}
	return line
}
