package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	got := renderWAVHashes(t, root, append(scoreCases(scores), wavFlushCases...))
	if t.Failed() {
		return
	}
	// Two renders that fail alike compare as identical; a flush case must
	// write a file in every mode, so it cannot pass that way.
	for _, c := range wavFlushCases {
		for _, mode := range wavParityModes {
			if h := got[c.name+" "+mode.name]; h.sum == "none" {
				t.Errorf("%s %s wrote no file (exit %d); a flush case must render", c.name, mode.name, h.status)
			}
		}
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
	// Songs in 3/4 and 6/8: time-signatures phase 2 requires them to render
	// identically on every target.
	"examples/meter-waltz.cicada",
	"examples/meter-jig.cicada",
}

// wavFlushCases drive state and smoothed controls into the subnormal numbers
// that kernel/dsp/subnormal flushes, with each processor that flushes in the
// signal path at non-default settings and sound after the stall, so a flush
// that changes the output changes a file. Every run of TestExampleWAVParity
// renders them, so the arm64 job compares them with amd64, and make
// test-flush-identity compares them, with the parity subset, with and
// without the flush (the build tag cicada_noflush). Each case must write a
// file in every mode.
//
//   - comp-breakdown: a sidechain compressor (makeup 0 dB, 10 ms release)
//     whose gain reduction releases to zero while the bass plays on. Flushing
//     the reduction or the makeup changed this file (fastmath.Exp2 is exactly
//     1 only at 0), which is why the compressor does not flush them.
//   - acid-controls: resonance, envelope modulation and accent glide to zero
//     while the notes go on; flushing envelope modulation or accent changed
//     this file the same way.
//   - deesser-gap: a de-esser releases under quiet hats (its reduction must
//     not be flushed either), then a silent gap stalls its detector, then
//     hats again.
//   - piano-pedal: a sustain pedal pressed and released, which without the
//     flush leaves the smoothed pedal at 1.4e-43 under 88 resonators, then a
//     chord after the stall.
//   - keys-tails: electric piano, poly keys and string machine tails through
//     ten bars of rest, then the chord again.
//   - reverb-hard-left: a hard-left track alone on the reverb send after
//     bar 1, so the reverb's right input is an exact zero while the left
//     plays and its right diffusers decay on their own; then the reverb mix
//     glides to zero while the track plays on.
//   - delay-controls, tape-controls: feedback, width, ping-pong, wow,
//     flutter and saturation glide to zero while notes play through them.
//   - drive-gap: a dark drive's tone filters decay in silence, notes return,
//     then its mix glides to zero while they play.
//   - chorus-gap and ampcab-gap: silence, then notes, through a chorus (at
//     96 kHz, where its input lowpass stalls) and an amp and cabinet (whose
//     DC blocker and tone filter stall). Scores cannot change a chorus's or
//     an amp's controls live, so their control flushes are covered only by
//     the processors' own tests.
//   - drum-pan: a kick panned hard left, then turned off, while the snare
//     plays.
//   - string-tail-from-bar-5: bars 5 to 24 of a string machine chord that
//     ends in bar 2: a range that holds only the tail (peak 1.7e-6), which
//     normalization raises by about 2^19. The loudness modes add nothing
//     here: the tail is below their -70 LUFS gate, so they exit 1 and write
//     the plain render's bytes.
//
// The sample voice's gain sweep has no case: sampler packs are not part of
// these renders, and its flush changes only the sign of a zero.
var wavFlushCases = []wavCase{
	{name: "flush:comp-breakdown", score: "cmd/cicada/testdata/flush/comp-breakdown.cicada"},
	{name: "flush:acid-controls", score: "cmd/cicada/testdata/flush/acid-controls.cicada"},
	{name: "flush:deesser-gap", score: "cmd/cicada/testdata/flush/deesser-gap.cicada"},
	{name: "flush:piano-pedal", score: "cmd/cicada/testdata/flush/piano-pedal.cicada"},
	{name: "flush:keys-tails", score: "cmd/cicada/testdata/flush/keys-tails.cicada"},
	{name: "flush:reverb-hard-left", score: "cmd/cicada/testdata/flush/reverb-hard-left.cicada"},
	{name: "flush:delay-controls", score: "cmd/cicada/testdata/flush/delay-controls.cicada"},
	{name: "flush:tape-controls", score: "cmd/cicada/testdata/flush/tape-controls.cicada"},
	{name: "flush:drive-gap", score: "cmd/cicada/testdata/flush/drive-gap.cicada"},
	{name: "flush:chorus-gap-96k", score: "cmd/cicada/testdata/flush/chorus-gap.cicada", args: []string{"--rate", "96000"}},
	{name: "flush:ampcab-gap", score: "cmd/cicada/testdata/flush/ampcab-gap.cicada"},
	{name: "flush:drum-pan", score: "cmd/cicada/testdata/flush/drum-pan.cicada"},
	{name: "flush:string-tail-from-bar-5", score: "cmd/cicada/testdata/flush/string-tail.cicada", args: []string{"--from", "5"}},
}

// wavCase is one score to render in every mode of wavParityModes. name is the
// first field of its hash key: the score's path, or for a case with extra
// render arguments a name without spaces. args go before the mode's.
type wavCase struct {
	name, score string
	args        []string
}

// scoreCases renders each score as it is, keyed by its path.
func scoreCases(scores []string) []wavCase {
	cases := make([]wavCase, len(scores))
	for i, score := range scores {
		cases[i] = wavCase{name: score, score: score}
	}
	return cases
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

// renderWAVHashes renders every case in every mode with the real main, in
// parallel child processes, and returns the results keyed by "name mode".
func renderWAVHashes(t *testing.T, root string, cases []wavCase) map[string]wavHash {
	t.Helper()
	var mu sync.Mutex
	got := map[string]wavHash{}
	t.Run("render", func(t *testing.T) {
		for _, c := range cases {
			for _, mode := range wavParityModes {
				key := c.name + " " + mode.name
				t.Run(strings.ReplaceAll(key, "/", "_"), func(t *testing.T) {
					t.Parallel()
					wav := filepath.Join(t.TempDir(), "out.wav")
					args := append(append([]string{"render", c.score, "-o", wav}, c.args...), mode.args...)
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

// wavPins pins the WAV bytes of renders ("score mode", modes as in
// wavParityModes) that a deterministic-math change moved on purpose, so the
// next change to them is visible. Each value was rendered on amd64 and equals
// the arm64 render (full sweep); the engine's samples also match WebAssembly
// (PCM24 parity). Renders of the full-length songs are checked only with
// CICADA_WAV_PARITY_ALL=1 (the nightly sweep): they take about a minute each.
// After an intended change, print the new values with
//
//	CICADA_WAV_PINS_PRINT=1 go test ./cmd/cicada -run '^TestExampleWAVPins$' -count=1 -v
//
// and update them only with the reason in the pull request.
var wavPins = map[string]string{
	// Changed by #329 (the voices on detmath): the modeled kit voice's strike
	// coefficients. Checked on every pull request.
	"examples/modeled-kit/reggae-one-drop-after-room.cicada normalize": "d1387c31c8ede816b48bd3a9c6e2b7335384d4f60bca3326db5375413ec9b142",
	"examples/modeled-kit/reggae-one-drop-after.cicada normalize":      "aa06e6dd89ec8b71e660bd942387b1dfdfe1929aaa0f83227176639783c101ff",
	"examples/modeled-kit/rock-after-room.cicada lufs-14":              "011de3edb780081434bd19a1cc53c116ee891c09c1db83bdf23ee6c08e36d843",
	"examples/modeled-kit/rock-after-room.cicada lufs-9":               "011de3edb780081434bd19a1cc53c116ee891c09c1db83bdf23ee6c08e36d843",
	"examples/modeled-kit/rock-after-room.cicada normalize":            "02f81a43ffd43160a32e727f08ef0166e19a67c26d4a663fbb3fa359875a37ba",
	"examples/modeled-kit/rock-after.cicada normalize":                 "f8a105950958dc1bbec9c444250f77e11e75db383eec894a78b6dac5ea2e7354",
	"examples/modeled-kit/soca-after-room.cicada lufs-14":              "62ae6108a0d14125d422a23ad205a44561d85a426ede05c81f3dbf68875ef444",
	"examples/modeled-kit/soca-after-room.cicada normalize":            "6f3a41dbac95498e3cefae4b5869cdabff013abb88d277c31276235e5559efcf",
	"examples/modeled-kit/soca-after.cicada lufs-9":                    "40a62d90e909ee984e1fdb310ff31204f96d98ee49f3a0654b97eb629db78d21",
	// Changed by #327 (track effects and the mix on detmath, the reverb's
	// modulation on SinPi); the songs use those effects. Checked nightly.
	// anywhere-you-are changed again by #338: 22 note starts at section
	// changes now sound (the engine dropped the first note of a scene-bound
	// pass whose previous pass released at the switch).
	"examples/songs/anywhere-you-are.cicada lufs-14":      "1f1aa87558deec7a80ab5082a4468ca4edfce7c06afb1f45042590a6bca0db35",
	"examples/songs/anywhere-you-are.cicada lufs-9":       "1f1aa87558deec7a80ab5082a4468ca4edfce7c06afb1f45042590a6bca0db35",
	"examples/songs/anywhere-you-are.cicada normalize":    "5c4ebeb4803f2d45f1a0db8a77644e250008c3a794e4c19db1cc6dc4422267d2",
	"examples/songs/anywhere-you-are.cicada plain":        "f084bbbf40601d3e8206af4ecc46586e29599a273815b6cc60bcfa665da45610",
	"examples/songs/lanterns-after-rain.cicada lufs-14":   "68123218cd22ed9e74092a7099b728a79909a82be408061b7eb9c96da89a4f07",
	"examples/songs/lanterns-after-rain.cicada lufs-9":    "a4003b204b58473ae51df34cd91a0617cfa8ae4aba092d56aed084bc7f4d3a56",
	"examples/songs/lanterns-after-rain.cicada normalize": "6d3fdb76d7c65450a67e6f61fe06bdc48b163c86df6913d9da455a0e27f3cd59",
	"examples/songs/lanterns-after-rain.cicada plain":     "60804c0ad760d91a37bab057e6b41bd141b76358f2cbb5f9128cb6a91f839963",
	"examples/songs/neon-causeway.cicada lufs-14":         "549247a83b59edca4368368c126cbc2e236a882f50209cc685e8772d035d6c7e",
	"examples/songs/tidal-glass.cicada lufs-14":           "a4154b36c1826be587058327a6252f2e8807387c13eb0528f1e8c7e91ca978ff",
	"examples/songs/tidal-glass.cicada normalize":         "b93d7287cc90965500295f8a76d854049f477495366000b067cc885fa8a2de5f",
}

func TestExampleWAVPins(t *testing.T) {
	if len(wavPins) == 0 {
		t.Log("no pinned renders")
		return
	}
	all := os.Getenv("CICADA_WAV_PARITY_ALL") == "1"
	byScore := map[string][]string{}
	for key := range wavPins {
		score, mode, _ := strings.Cut(key, " ")
		if strings.HasPrefix(score, "examples/songs/") && !all {
			continue
		}
		byScore[score] = append(byScore[score], mode)
	}
	var scores []string
	for score := range byScore {
		scores = append(scores, score)
	}
	sort.Strings(scores)
	root := repositoryRoot()
	var mu sync.Mutex
	got := map[string]wavHash{}
	t.Run("render", func(t *testing.T) {
		for _, score := range scores {
			for _, mode := range wavParityModes {
				if !slices.Contains(byScore[score], mode.name) {
					continue
				}
				key := score + " " + mode.name
				t.Run(strings.ReplaceAll(key, "/", "_"), func(t *testing.T) {
					t.Parallel()
					wav := filepath.Join(t.TempDir(), "out.wav")
					_, status := runMain(t, root, append([]string{"render", score, "-o", wav}, mode.args...)...)
					result := wavHash{sum: "none", status: status}
					if data, err := os.ReadFile(wav); err == nil {
						sum := sha256.Sum256(data)
						result.sum = hex.EncodeToString(sum[:])
					}
					mu.Lock()
					got[key] = result
					mu.Unlock()
				})
			}
		}
	})
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if os.Getenv("CICADA_WAV_PINS_PRINT") == "1" {
			t.Logf("\t%q: %q,", key, got[key].sum)
			continue
		}
		if got[key].sum != wavPins[key] {
			t.Errorf("%s: WAV sha256 %s (exit %d), pinned %s", key, got[key].sum, got[key].status, wavPins[key])
		}
	}
}
