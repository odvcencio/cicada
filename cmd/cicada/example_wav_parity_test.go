package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestExampleWAVParity checks the determinism contract at the level of the
// exported file: the same score must write the same WAV bytes on amd64 and
// arm64. The PCM24 parity test compares the engine's samples; this test runs
// `cicada render` itself, so the master chain, the loudness search, the
// normalize pass, dither and the WAV encoder are covered as well.
//
// Each score renders four ways: plain, --normalize, -14 LUFS / -1 dBTP and
// -9 LUFS / -1.5 dBTP. A render that fails is recorded with its exit status, so
// a score that fails on one machine and not the other is a difference too.
//
// In CI the amd64 parity job writes the hashes (CICADA_WAV_PARITY_OUT) and
// uploads them; the arm64 parity job compares its own renders with them
// (CICADA_WAV_PARITY_REFERENCE). Pull requests render wavParitySubset; the
// nightly sweep sets CICADA_WAV_PARITY_ALL=1 and renders every score under
// examples/. By hand, on amd64 and then on arm64:
//
//	CICADA_WAV_PARITY_OUT=$PWD/build/wav-hashes.txt go test ./cmd/cicada -run '^TestExampleWAVParity$' -count=1 -v
//	CICADA_WAV_PARITY_REFERENCE=$PWD/build/wav-hashes.txt go test ./cmd/cicada -run '^TestExampleWAVParity$' -count=1 -v
func TestExampleWAVParity(t *testing.T) {
	out := os.Getenv("CICADA_WAV_PARITY_OUT")
	reference := os.Getenv("CICADA_WAV_PARITY_REFERENCE")
	if out == "" && reference == "" {
		t.Skip("set CICADA_WAV_PARITY_OUT to write the hashes or CICADA_WAV_PARITY_REFERENCE to compare with them")
	}
	root := repositoryRoot()
	// go test runs in the package folder; read relative paths from the
	// repository root, where the commands above are typed.
	if out != "" && !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	if reference != "" && !filepath.IsAbs(reference) {
		reference = filepath.Join(root, reference)
	}
	scores := wavParitySubset
	if os.Getenv("CICADA_WAV_PARITY_ALL") == "1" {
		scores = exampleScores(t, root)
	}
	got := renderWAVHashes(t, root, scores)
	if t.Failed() {
		return
	}
	if out != "" {
		if err := writeWAVHashes(out, got); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d render hashes to %s", len(got), out)
	}
	if reference == "" {
		return
	}
	want, err := readWAVHashes(reference)
	if err != nil {
		t.Fatal(err)
	}
	problems, notes := compareWAVHashes(got, want, knownWAVDifferences, os.Getenv("CICADA_WAV_PARITY_ALL") == "1")
	for _, note := range notes {
		t.Log(note)
	}
	for _, problem := range problems {
		t.Error(problem)
	}
}

// wavParitySubset is what every pull request renders. It holds every score
// whose WAV differed between amd64 and arm64 on main at fa08d308 (sweep of
// 2026-11-10, 42 of 584 renders) except the four full-length songs, which
// take minutes each and are left to the nightly sweep. The master limiter
// path, which only a song uses, is covered by the TestExportLoudness tests
// that the arm64 parity job also runs.
var wavParitySubset = []string{
	"examples/fx-bus.cicada",
	"examples/continuous-automation.cicada",
	"examples/expressive-guitar.cicada",
	"examples/std-fx/main.cicada",
	"examples/modeled-kit/jazz-ride-after-room.cicada",
	"examples/modeled-kit/reggae-one-drop-after-room.cicada",
	"examples/modeled-kit/rock-after-room.cicada",
	"examples/modeled-kit/soca-after-room.cicada",
}

// knownWAVDifference is a render that is known to differ between amd64 and
// arm64: when it was listed and what causes it.
type knownWAVDifference struct {
	listed string
	cause  string
}

// knownWAVDifferences lists the renders ("score mode") whose bytes still
// differ between amd64 and arm64. A listed render that differs is reported,
// not hidden; a listed render that has become identical fails the test until
// its entry is removed, so the list can only shrink. The next pull request of
// the determinism series (library math on the render path) must leave it
// empty.
//
// 2026-11-10: the full sweep at 8061013f (668 renders: 167 scores, four modes
// each) found no render that differs, so the list starts empty.
var knownWAVDifferences = map[string]knownWAVDifference{}

var wavParityModes = []struct {
	name string
	args []string
}{
	{"plain", nil},
	{"normalize", []string{"--normalize"}},
	{"lufs-14", []string{"--loudness", "-14", "--true-peak-max", "-1"}},
	{"lufs-9", []string{"--loudness", "-9", "--true-peak-max", "-1.5"}},
}

// wavHash is one render's result: the SHA-256 of the WAV it wrote ("none" when
// it wrote no file) and its exit status.
type wavHash struct {
	sum    string
	status int
}

func (h wavHash) String() string { return fmt.Sprintf("%s exit=%d", h.sum, h.status) }

func exampleScores(t *testing.T, root string) []string {
	t.Helper()
	var scores []string
	err := filepath.WalkDir(filepath.Join(root, "examples"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".cicada") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			scores = append(scores, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(scores)
	return scores
}

// renderWAVHashes renders every score in every mode with the real main, in
// parallel child processes, and returns the results keyed by "score mode".
func renderWAVHashes(t *testing.T, root string, scores []string) map[string]wavHash {
	t.Helper()
	var mu sync.Mutex
	got := map[string]wavHash{}
	t.Run("render", func(t *testing.T) {
		for _, score := range scores {
			for _, mode := range wavParityModes {
				key := score + " " + mode.name
				t.Run(strings.ReplaceAll(key, "/", "_"), func(t *testing.T) {
					t.Parallel()
					wav := filepath.Join(t.TempDir(), "out.wav")
					args := append([]string{"render", score, "-o", wav}, mode.args...)
					_, status := runMain(t, root, args...)
					result := wavHash{sum: "none", status: status}
					if data, err := os.ReadFile(wav); err == nil {
						sum := sha256.Sum256(data)
						result.sum = hex.EncodeToString(sum[:])
					} else if !os.IsNotExist(err) {
						t.Fatal(err)
					}
					mu.Lock()
					got[key] = result
					mu.Unlock()
				})
			}
		}
	})
	return got
}

func writeWAVHashes(path string, hashes map[string]wavHash) error {
	keys := make([]string, 0, len(hashes))
	for key := range hashes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&b, "%s %s\n", key, hashes[key])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// readWAVHashes reads lines of "score mode sum exit=status".
func readWAVHashes(path string) (map[string]wavHash, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	hashes := map[string]wavHash{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 4 || !strings.HasPrefix(fields[3], "exit=") {
			return nil, fmt.Errorf("%s:%d: want \"score mode sum exit=status\", got %q", path, line, scanner.Text())
		}
		status, err := strconv.Atoi(strings.TrimPrefix(fields[3], "exit="))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", path, line, err)
		}
		hashes[fields[0]+" "+fields[1]] = wavHash{sum: fields[2], status: status}
	}
	return hashes, scanner.Err()
}

// compareWAVHashes compares this machine's renders with the reference. It
// returns one problem per render that breaks the contract and one note per
// render on the known-differences list, then a summary note. all is true when
// every example was rendered, so a listed render that no longer exists is a
// problem too.
func compareWAVHashes(got, want map[string]wavHash, known map[string]knownWAVDifference, all bool) (problems, notes []string) {
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	same, listed := 0, 0
	for _, key := range keys {
		reference, ok := want[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: the reference has no entry; both machines must render the same set", key))
			continue
		}
		entry, isKnown := known[key]
		switch {
		case got[key] == reference && isKnown:
			problems = append(problems, fmt.Sprintf("%s: now identical on both machines (%s); remove it from knownWAVDifferences (listed %s: %s)", key, reference, entry.listed, entry.cause))
		case got[key] == reference:
			same++
		case isKnown:
			listed++
			notes = append(notes, fmt.Sprintf("%s: known difference, listed %s (%s): reference %s, here %s", key, entry.listed, entry.cause, reference, got[key]))
		default:
			problems = append(problems, fmt.Sprintf("%s: differs from the reference: reference %s, here %s. Find the first stage whose bits differ: an unrounded product or a math library call (docs/audio/mix-chain.md)", key, reference, got[key]))
		}
	}
	if all {
		var stale []string
		for key := range known {
			if _, ok := got[key]; !ok {
				stale = append(stale, key)
			}
		}
		sort.Strings(stale)
		for _, key := range stale {
			problems = append(problems, fmt.Sprintf("%s: listed in knownWAVDifferences but not an example render; remove it", key))
		}
	}
	notes = append(notes, fmt.Sprintf("%d of %d renders identical to the reference, %d known differences", same, len(keys), listed))
	return problems, notes
}

// The rules of the known-differences list, without rendering anything.
func TestCompareWAVHashesKeepsTheKnownListHonest(t *testing.T) {
	a, b := wavHash{sum: "aa", status: 0}, wavHash{sum: "bb", status: 0}
	failed := wavHash{sum: "none", status: 1}
	known := map[string]knownWAVDifference{
		"s.cicada listed-differs": {listed: "2026-11-10", cause: "test"},
		"s.cicada listed-same":    {listed: "2026-11-10", cause: "test"},
		"s.cicada listed-gone":    {listed: "2026-11-10", cause: "test"},
	}
	got := map[string]wavHash{
		"s.cicada same":           a,
		"s.cicada differs":        a,
		"s.cicada status":         failed,
		"s.cicada listed-differs": a,
		"s.cicada listed-same":    a,
		"s.cicada no-reference":   a,
	}
	want := map[string]wavHash{
		"s.cicada same":           a,
		"s.cicada differs":        b,
		"s.cicada status":         {sum: "none", status: 2},
		"s.cicada listed-differs": b,
		"s.cicada listed-same":    a,
	}
	for _, all := range []bool{false, true} {
		problems, notes := compareWAVHashes(got, want, known, all)
		joined := strings.Join(problems, "\n")
		for _, key := range []string{"s.cicada differs", "s.cicada status", "s.cicada listed-same", "s.cicada no-reference"} {
			if !strings.Contains(joined, key+":") {
				t.Errorf("all=%v: no problem reported for %q in:\n%s", all, key, joined)
			}
		}
		if strings.Contains(joined, "s.cicada same:") || strings.Contains(joined, "s.cicada listed-differs:") {
			t.Errorf("all=%v: a matching render or a listed difference was reported as a problem:\n%s", all, joined)
		}
		if gone := strings.Contains(joined, "s.cicada listed-gone:"); gone != all {
			t.Errorf("all=%v: a listed render that was not rendered reported=%v, want %v", all, gone, all)
		}
		if len(problems) != 4+map[bool]int{false: 0, true: 1}[all] {
			t.Errorf("all=%v: %d problems, want %d:\n%s", all, len(problems), 4+map[bool]int{false: 0, true: 1}[all], joined)
		}
		if summary := notes[len(notes)-1]; summary != "1 of 6 renders identical to the reference, 1 known differences" {
			t.Errorf("all=%v: summary %q", all, summary)
		}
	}
}
