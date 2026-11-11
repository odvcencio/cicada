package main

import (
	"bytes"
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/renderlanes"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// Every --jobs value must write the bytes of the serial render (--jobs 1):
// lanes only synthesize each track's own signal between control changes, and
// both renderers mix the tracks in track order whatever the lane count.
//
// TestParallelRenderIdentity checks a few short scores on every test run; they
// cover both renderers, sends, a sidechain compressor, automation, tempo
// changes, chance and humanize, modeled keyboards, modulators, an audio clip
// and a master chain. Each is rendered plain at every job count, once more
// with every segment forced onto every lane, with a loudness target, as stems,
// and with the note events audition reads. With
// CICADA_PARALLEL_IDENTITY_SWEEP=1 it checks every example score the
// repository can render, in full, plain and at -14 LUFS for every job count;
// the nightly CI job runs that sweep on amd64 and arm64
// (make test-parallel-identity).

// parallelIdentityJobs are the job counts compared with --jobs 1; zero is the
// automatic choice. CICADA_PARALLEL_IDENTITY_JOBS replaces them with its
// space-separated counts, so repeated sweeps can try other lane counts.
var parallelIdentityJobs = []int{2, 4, 8, 0}

func identityJobs(t *testing.T) []int {
	t.Helper()
	list := os.Getenv("CICADA_PARALLEL_IDENTITY_JOBS")
	if list == "" {
		return parallelIdentityJobs
	}
	var jobs []int
	for _, field := range strings.Fields(list) {
		n, err := strconv.Atoi(field)
		if err != nil || n < 0 {
			t.Fatalf("CICADA_PARALLEL_IDENTITY_JOBS: %q is not a job count", field)
		}
		jobs = append(jobs, n)
	}
	return jobs
}

type parallelIdentityScore struct {
	name string
	path string
	bars int
	// replacements rewrite the main source first, in pairs of old and new text.
	replacements []string
	// extras adds stems, note events and the forced-lane render.
	extras bool
}

func parallelIdentitySubset() []parallelIdentityScore {
	root := repositoryRoot()
	example := func(path string) string { return filepath.Join(root, "examples", filepath.FromSlash(path)) }
	return []parallelIdentityScore{
		// The scheduled engine: 12 tracks, two electric pianos, sends,
		// automation, an audio clip and a master chain ending in a limiter.
		{name: "scheduled-large-song", path: example("songs/anywhere-you-are.cicada"), bars: 1, extras: true},
		// The scheduled engine with modulators on the 128-frame control grid.
		{name: "scheduled-modulators", path: example("detail/modulators.cicada"), bars: 2},
		// The direct renderer: chance steps, and humanize on the drums.
		{name: "direct-chance-humanize", path: example("cicada-chorus.cicada"), bars: 2, replacements: []string{"pattern beat drums {", "pattern beat drums {\n  humanize = 12"}, extras: true},
		// The direct renderer: a compressor on the music bus keyed by a track.
		{name: "direct-sidechain", path: example("fx/compressor-bus.cicada"), bars: 2},
		// The direct renderer: tempo changes between scenes and a delay send.
		{name: "direct-scene-tempo", path: example("scene-tempo.cicada")},
		// The direct renderer: automation, sends, and a master chain with a limiter.
		{name: "direct-automation-master", path: example("songs/neon-causeway.cicada"), bars: 2, replacements: []string{"\nsong {", "\nfx ceiling limiter { ceiling = -1dBTP }\nmaster { insert = ceiling }\nsong {"}},
		// The direct renderer: modeled keyboards (FM bass, poly keys).
		{name: "direct-keys", path: example("first-song/main.cicada"), bars: 2},
	}
}

func parallelIdentitySweep(t *testing.T) []parallelIdentityScore {
	t.Helper()
	root := repositoryRoot()
	var scores []parallelIdentityScore
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".cicada") {
			name, _ := filepath.Rel(root, path)
			scores = append(scores, parallelIdentityScore{name: filepath.ToSlash(name), path: path})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return scores
}

// loadParallelIdentityScore inspects a score with its replacements applied, or
// says why it cannot render.
func loadParallelIdentityScore(t *testing.T, score parallelIdentityScore) (scoreInspection, string) {
	t.Helper()
	var overrides map[string][]byte
	if len(score.replacements) > 0 {
		source, err := os.ReadFile(score.path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(source)
		for i := 0; i+1 < len(score.replacements); i += 2 {
			if strings.Count(text, score.replacements[i]) != 1 {
				t.Fatalf("%s: %q must occur once", score.path, score.replacements[i])
			}
			text = strings.Replace(text, score.replacements[i], score.replacements[i+1], 1)
		}
		absolute, err := filepath.Abs(score.path)
		if err != nil {
			t.Fatal(err)
		}
		overrides = map[string][]byte{absolute: []byte(text)}
	}
	inspection, err := inspectScoreWith(score.path, overrides)
	if err != nil {
		return scoreInspection{}, err.Error()
	}
	if inspection.score == nil || hasDiagnosticErrors(inspection.diagnostics) {
		return scoreInspection{}, "the score has errors"
	}
	return inspection, ""
}

// withoutLanes clears the fields that say how a render used its lanes, the
// only fields of a report that may differ between job counts.
func withoutLanes(report render.Report) render.Report {
	report.Jobs, report.Lanes = 0, 0
	return report
}

func renderIdentityBytes(score *notation.Score, opts render.Options) ([]byte, render.Report, error) {
	var out bytes.Buffer
	report, err := render.WAV(score, opts, &out)
	return out.Bytes(), report, err
}

func TestParallelRenderIdentity(t *testing.T) {
	scores := parallelIdentitySubset()
	sweep := os.Getenv("CICADA_PARALLEL_IDENTITY_SWEEP") == "1"
	if sweep {
		scores = parallelIdentitySweep(t)
	}
	jobsList := identityJobs(t)
	checked, skipped := 0, 0
	for _, score := range scores {
		t.Run(score.name, func(t *testing.T) {
			inspection, why := loadParallelIdentityScore(t, score)
			if inspection.score == nil {
				if !sweep {
					t.Fatalf("subset score does not load: %s", why)
				}
				skipped++
				t.Skipf("not renderable without more files: %s", why)
			}
			root := inspection.root(score.path)
			opts := render.Options{SampleRate: 48000, Bits: 24, Bars: score.bars, TailSec: 3, AssetRoot: root, AssetDir: root, Jobs: 1}
			serial, serialReport, err := renderIdentityBytes(inspection.score, opts)
			if err != nil {
				if !sweep {
					t.Fatalf("subset score does not render: %v", err)
				}
				skipped++
				t.Skipf("does not render: %v", err)
			}
			if serialReport.Jobs != 1 || serialReport.Lanes != 1 {
				t.Fatalf("--jobs 1 reported jobs %d lanes %d", serialReport.Jobs, serialReport.Lanes)
			}
			t.Logf("tracks %d, scheduled engine %t, serial sha256 %x", len(inspection.score.Tracks), render.UsesScheduledEngine(inspection.score, opts), sha256.Sum256(serial))
			for _, jobs := range jobsList {
				opts.Jobs = jobs
				got, report, err := renderIdentityBytes(inspection.score, opts)
				if err != nil {
					t.Fatalf("jobs %d: %v", jobs, err)
				}
				if !bytes.Equal(serial, got) {
					t.Fatalf("jobs %d: WAV bytes differ from --jobs 1 (sha256 %x)", jobs, sha256.Sum256(got))
				}
				if !reflect.DeepEqual(withoutLanes(serialReport), withoutLanes(report)) {
					t.Fatalf("jobs %d: report differs from --jobs 1:\n%+v\n%+v", jobs, report, serialReport)
				}
				if want := renderlanes.Jobs(jobs); report.Jobs != want || report.Lanes < 1 || report.Lanes > min(want, len(inspection.score.Tracks)) {
					t.Fatalf("jobs %d: reported jobs %d lanes %d for %d tracks", jobs, report.Jobs, report.Lanes, len(inspection.score.Tracks))
				}
			}
			if score.extras {
				checkForcedLanes(t, inspection.score, opts, serial)
				checkStemsIdentity(t, inspection.score, opts)
			}
			checkLoudnessJobsIdentity(t, inspection.score, opts, sweep, jobsList)
			checked++
		})
	}
	t.Logf("METRIC PARALLEL-RENDER-IDENTITY scores=%d checked=%d skipped=%d jobs=%v", len(scores), checked, skipped, jobsList)
}

// checkForcedLanes renders with every segment on every lane, however light.
func checkForcedLanes(t *testing.T, score *notation.Score, opts render.Options, serial []byte) {
	t.Helper()
	restore := renderlanes.ForceEveryLane()
	defer restore()
	opts.Jobs = 4
	got, _, err := renderIdentityBytes(score, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(serial, got) {
		t.Fatal("every segment on every lane: WAV bytes differ from --jobs 1")
	}
}

// checkStemsIdentity compares every stem file, and the note events audition
// reads from the same render, between --jobs 1 and --jobs 4.
func checkStemsIdentity(t *testing.T, score *notation.Score, opts render.Options) {
	t.Helper()
	type noteEvent struct {
		track int
		event seq.Event
	}
	stems := func(jobs int) (map[string][]byte, []noteEvent) {
		var notes []noteEvent
		stemOpts := opts
		stemOpts.Bits, stemOpts.Jobs = 32, jobs
		stemOpts.NoteTap = func(track int, event seq.Event) { notes = append(notes, noteEvent{track, event}) }
		dir := filepath.Join(t.TempDir(), "stems")
		if _, err := render.Stems(score, stemOpts, dir); err != nil {
			t.Fatalf("stems jobs %d: %v", jobs, err)
		}
		files := map[string][]byte{}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files[entry.Name()] = data
		}
		return files, notes
	}
	serialFiles, serialNotes := stems(1)
	if len(serialFiles) == 0 || len(serialNotes) == 0 {
		t.Fatalf("stems wrote %d files and tapped %d notes", len(serialFiles), len(serialNotes))
	}
	files, notes := stems(4)
	names := slices.Sorted(func(yield func(string) bool) {
		for name := range serialFiles {
			if !yield(name) {
				return
			}
		}
	})
	for _, name := range names {
		if !bytes.Equal(serialFiles[name], files[name]) {
			t.Fatalf("stem %s differs between --jobs 1 and --jobs 4", name)
		}
	}
	if len(files) != len(serialFiles) || !reflect.DeepEqual(serialNotes, notes) {
		t.Fatalf("stems: %d files and %d notes with --jobs 4, %d and %d with --jobs 1", len(files), len(notes), len(serialFiles), len(serialNotes))
	}
}

// checkLoudnessJobsIdentity runs the loudness search to -14 LUFS at --jobs 1
// and compares the written file and the report with other job counts: the
// sweep checks every count, the per-run subset the automatic one.
func checkLoudnessJobsIdentity(t *testing.T, score *notation.Score, opts render.Options, sweep bool, all []int) {
	t.Helper()
	target := renderTargetOptions{LoudnessTarget: -14, TruePeakMaxDBTP: -1, Tolerance: 0.5, Loudness: true}
	search := func(jobs int) ([]byte, loudnessFileReport, error) {
		path := filepath.Join(t.TempDir(), "loud.wav")
		searchOpts := opts
		searchOpts.Jobs = jobs
		report, err := renderLoudnessFileReport(score, path, searchOpts, target, nil)
		data, _ := os.ReadFile(path)
		report.Jobs, report.Lanes = 0, 0
		return data, report, err
	}
	serial, serialReport, serialErr := search(1)
	jobsList := []int{0}
	if sweep {
		jobsList = all
	}
	for _, jobs := range jobsList {
		got, report, err := search(jobs)
		if (err == nil) != (serialErr == nil) || err != nil && err.Error() != serialErr.Error() {
			t.Fatalf("loudness jobs %d: error %v, --jobs 1 gave %v", jobs, err, serialErr)
		}
		if !bytes.Equal(serial, got) {
			t.Fatalf("loudness jobs %d: WAV bytes differ from --jobs 1", jobs)
		}
		if !reflect.DeepEqual(serialReport, report) {
			t.Fatalf("loudness jobs %d: report differs from --jobs 1:\n%+v\n%+v", jobs, report, serialReport)
		}
	}
}
