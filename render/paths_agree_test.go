package render

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// A score must sound the same whichever path renders it: the direct mixer
// (WAV, when UsesScheduledEngine is false) or the scheduled engine
// (renderScheduleWAV, which Studio playback also uses). TestRenderPathsAgree
// renders every example score the direct mixer can render, and a set of small
// scores for the constructs most likely to differ, through both paths and
// compares what each plays. It is always on and takes a few seconds.
//
// Level (a), the gate: the note events each renderer plays, from Options.NoteTap.
// Per track, every note start (tick, pitch or chord, velocity, accent, slide;
// a drum hit by lane). Releases are not gated: the direct mixer reports only
// the releases it forces at a scene boundary, the engine every note end, so a
// note that happens to end on a bar line shows up on one side only. The test
// logs how many scores differ in releases on bar lines. Differences must match
// pathsKnownDifferences exactly: a new difference fails, and so does a listed
// one that has gone (remove its entry then) or changed (update its counts).
//
// Level (b), audio: CICADA_PATHS_AUDIO=1 also compares the float32 bytes of
// both renders. It only reports (t.Log) and never fails; see
// TestRenderPathsAudio and `make test-render-paths-audio`.

// pathsKnown is one difference between the paths that is understood and owned.
// Signature is what the comparison prints for the score (counts of events
// played by one path only), so a different difference in the same score does
// not hide behind the entry.
type pathsKnown struct {
	Score      string // example path under examples/, or a targeted case name
	Construct  string
	Difference string
	Owner      string // the pull request or note that owns the fix
	Signature  string
}

// pathsKnownDifferences can only shrink: delete an entry in the pull request
// that fixes its difference.
var pathsKnownDifferences = []pathsKnown{
	// Slide (~) on the last step of a pattern, into the first note of the next
	// scene. With sixteenth-note steps both paths glide; with quarter-note
	// steps the engine glides and the direct mixer plays a plain note. Probable
	// cause: sceneTransitionFor (render/wav.go, near line 1507) finds the last
	// step with seq.TicksPerStep, not the pattern's own step length. Not proven.
	{"targeted: slide across a scene switch, quarter-note steps", "slide across a scene switch, pattern step other than a sixteenth", "the engine slides into the note at each scene switch, the direct mixer does not", "none yet (found while reviewing #338)", "start: 2 only direct, 2 only engine"},
	// Drum humanize: the velocity draw takes the step's note as an input. The
	// engine's lane steps carry the lane number (project/runtime.go:408,
	// packProjectStep), the direct mixer's carry the General MIDI pitch
	// (project/compile.go:275 with drumNotes, compile.go:39), so every humanized
	// hit gets a different velocity. kernel/seq/schedule.go:73 documents the lane
	// number. Every other input (seed, track, slot, iteration, step) agrees.
	{"targeted: drum humanize with accents and chance steps", "drum pattern with humanize", "every humanized hit has a different velocity", "none yet (root cause in the pull request that added this test)", "start: 49 only direct, 49 only engine"},
	{"targeted: drum humanize across two scenes", "drum pattern with humanize", "every humanized hit has a different velocity", "none yet (root cause in the pull request that added this test)", "start: 66 only direct, 66 only engine"},
}

type pathsCase struct{ name, source string }

const pathsHead = `cicada 2
tempo 120
key a minor
instrument m { voice poly { out = sine(pitch) * adsr(gate, 5ms, 100ms, 0.8, 100ms) * 0.3 } }
instrument mo { voice mono { out = saw(pitch) * adsr(gate, 5ms, 100ms, 0.8, 100ms) * 0.2 } }
track lead m {}
track mono mo {}
track bass acid {}
track kit drums {}
pattern idle { c3 . . . }
`

// pathsCases are targeted scores. Each uses only what the direct mixer can
// render, so both paths run.
var pathsCases = []pathsCase{
	{"slide across a scene switch", `pattern ax { . . . . . . . . . . . . . . . 1~ }
pattern ay { 5 . . . . . . . . . . . . . . . }
scene x { bass = ax }
scene y { bass = ay }
song { x*2 y*1 x*1 y*2 }`},
	{"slide across a scene switch, quarter-note steps", `pattern ax { step = 1/4 . . . 1~ }
pattern ay { step = 1/4 5 . . . }
scene x { bass = ax }
scene y { bass = ay }
song { x*2 y*1 x*1 y*2 }`},
	{"drum humanize with accents and chance steps", `pattern hh drums { humanize = 12
  bd: X...x...x?60...x...
  sd: ....X.......x?50...
  ch: x.x.X.x.x?70x.x.x..
}
scene x { kit = hh }
song { x*4 }`},
	{"drum humanize across two scenes", `pattern a drums { humanize = 20
  bd: x...x...x...x...
  ch: x.x.x.x.x.x.x.x.
}
pattern b drums { humanize = 20
  bd: x.x.x.x.x.x.x.x.
  sd: ....x.......x...
}
scene x { kit = a }
scene y { kit = b }
song { x*2 y*2 x*2 }`},
	{"expression rows on a poly voice", `pattern e { c4 - - - g4 - - .
  bend: 0ct +50ct +100ct 0ct -50ct . 0ct .
  vibrato: 0ct . 12ct . 4ct . 0ct .
  pressure: 0 0.3 0.8 . 0.6 . 0.2 0
  timbre: . 0.6 0.9 . 0.4 . 0.5 .
}
scene x { lead = e }
song { x*4 }`},
	{"expression rows on a mono voice", `pattern e { c3 - - - g3 - - .
  bend: 0ct +50ct +100ct 0ct -50ct . 0ct .
  vibrato: 0ct . 12ct . 4ct . 0ct .
  pressure: 0 0.3 0.8 . 0.6 . 0.2 0
}
scene x { mono = e }
song { x*4 }`},
	{"expression rows on acid", `pattern e { c2 - - - g2 - - .
  bend: 0ct -25ct -50ct 0ct +25ct . 0ct .
  vibrato: 0ct . 4ct . 2ct . 0ct .
}
scene x { bass = e }
song { x*4 }`},
	{"tied first note at a scene change", `pattern tf { step = 1/4 - . c4 - }
scene x { lead = idle }
scene y { lead = tf }
song { x*2 y*2 }`},
	{"tied first note at a tempo change", `pattern one { step = 1/4 c4 - - - }
scene x { lead = one }
scene y { lead = one tempo = 100 }
song { x*2 y*2 }`},
	{"tie into a pattern whose first step is a tie", `pattern ox { step = 1/4 . . c4 - }
pattern tf { step = 1/4 - . c4 - }
scene x { lead = ox }
scene y { lead = tf }
song { x*2 y*2 }`},
	{"mono voice with overlapping notes", `pattern o { gate = 100% c3 d3 - e3 g3 - - a3 }
scene x { mono = o }
song { x*4 }`},
	{"swing at a section change", `pattern s1 { swing = 60% c3 c3 c3 c3 c3 c3 c3 c3 }
pattern s2 { swing = 54% e3 e3 e3 e3 e3 e3 e3 e3 }
scene x { lead = s1 }
scene y { lead = s2 }
song { x*2 y*2 x*1 }`},
	{"pattern chain", `track chained m { chain = ca cb }
pattern ca { step = 1/4 c3 - - - }
pattern cb { step = 1/2 e3 - }
scene main {}
song { main*4 }`},
	{"full gate into a new pattern", `pattern full { step = 1/4 gate = 100% . . . g3 }
pattern start { step = 1/4 e4 . . . }
scene x { lead = full }
scene y { lead = start }
song { x*2 y*2 }`},
}

// pathsEvent is one normalized event.
type pathsEvents struct {
	on, off map[string]int // key -> count, keyed per track
}

type pathsRun struct {
	events pathsEvents
	wav    []byte
	err    string
}

// pathsMaxBars and pathsRate keep the sweep short. Note events depend on the
// score and the tempo, not on the sample rate.
const (
	pathsMaxBars = 6
	pathsRate    = 44100
)

// pathsFull makes pathsRender use the whole song at 48 kHz (the nightly audio
// report, CICADA_PATHS_AUDIO=full) instead of the first six bars at 44.1 kHz.
var pathsFull bool

func pathsSongBars(score *notation.Score) int {
	bars := 0
	for _, entry := range score.Song {
		bars += entry.Bars
	}
	if pathsFull {
		return bars
	}
	return min(pathsMaxBars, bars)
}

// pathsRender renders score through one path with a note tap. Bars is limited
// to the first eight so the whole sweep stays fast; assetRoot is the score's
// directory.
func pathsRender(score *notation.Score, scheduled bool, assetRoot string) pathsRun {
	drums := map[int]bool{}
	if p, _ := project.FromScore(score); p != nil {
		if cfg, err := project.CompileEngine(p, pathsRate, 128); err == nil {
			for i := 0; i < cfg.Tracks; i++ {
				drums[i] = cfg.Track[i].Kind == engine.VoiceDrums
			}
		}
	}
	var laneOf = map[uint8]uint8{}
	for lane, note := range drum.MIDINotes {
		laneOf[note] = uint8(lane)
	}
	run := pathsRun{events: pathsEvents{on: map[string]int{}, off: map[string]int{}}}
	tap := func(track int, ev seq.Event) {
		switch ev.Kind {
		case seq.NoteOn:
			note := ev.Note
			if drums[track] {
				// The engine plays a drum hit as its General MIDI pitch, the
				// direct mixer as its lane number; the sound is the same.
				if lane, ok := laneOf[note]; ok {
					note = lane
				}
			}
			run.events.on[fmt.Sprintf("track %d tick %d note %d chord %v vel %d accent %v slide %v", track, ev.Tick, note, ev.Notes[:ev.NoteCount], ev.Velocity, ev.Accent, ev.Slide)]++
		case seq.NoteOff:
			// Only releases on a bar line: the direct mixer reports no others.
			if ev.Tick%seq.TicksPerBar == 0 && !drums[track] {
				run.events.off[fmt.Sprintf("track %d tick %d", track, ev.Tick)]++
			}
		}
	}
	dither := false
	rate := pathsRate
	if pathsFull {
		rate = 48000
	}
	opts := Options{Bits: 32, SampleRate: rate, Block: 4096, TailSec: 0.1, Bars: pathsSongBars(score), Dither: &dither, NoteTap: tap, AssetRoot: assetRoot}
	var buf bytes.Buffer
	var err error
	if scheduled {
		_, err = renderScheduleWAV(score, opts, &buf, "", 1)
	} else {
		if UsesScheduledEngine(score, opts) {
			run.err = "engine only: " + scheduledEngineReason(score, opts)
			return run
		}
		_, err = WAV(score, opts, &buf)
	}
	if err != nil {
		run.err = err.Error()
		return run
	}
	run.wav = buf.Bytes()
	return run
}

// pathsDiff returns "" when the two runs played the same events, else the
// signature: how many events one path played that the other did not.
func pathsDiff(direct, engine pathsEvents) (signature string, detail []string) {
	var parts []string
	for _, level := range []struct {
		name string
		a, b map[string]int
	}{{"start", direct.on, engine.on}} {
		onlyDirect, onlyEngine := 0, 0
		keys := map[string]bool{}
		for k := range level.a {
			keys[k] = true
		}
		for k := range level.b {
			keys[k] = true
		}
		var sorted []string
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			d := level.a[k] - level.b[k]
			switch {
			case d > 0:
				onlyDirect += d
				detail = append(detail, fmt.Sprintf("%s only direct x%d: %s", level.name, d, k))
			case d < 0:
				onlyEngine -= d
				detail = append(detail, fmt.Sprintf("%s only engine x%d: %s", level.name, -d, k))
			}
		}
		if onlyDirect+onlyEngine > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d only direct, %d only engine", level.name, onlyDirect, onlyEngine))
		}
	}
	return strings.Join(parts, "; "), detail
}

type pathsScore struct {
	name      string
	score     *notation.Score
	assetRoot string
}

func pathsExamples(t *testing.T) (scores []pathsScore, unreadable []string) {
	t.Helper()
	root := filepath.Join("..", "examples")
	var files []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, ".cicada") {
			files = append(files, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		rel, _ := filepath.Rel(root, file)
		rel = filepath.ToSlash(rel)
		score, diagnostics, err := project.LoadScore(file, nil)
		bad := err != nil || score == nil
		for _, d := range diagnostics {
			if d.Severity == "error" {
				bad = true
			}
		}
		if bad {
			unreadable = append(unreadable, rel)
			continue
		}
		scores = append(scores, pathsScore{name: rel, score: score, assetRoot: filepath.Dir(file)})
	}
	return scores, unreadable
}

func pathsTargeted(t *testing.T) []pathsScore {
	t.Helper()
	var out []pathsScore
	for _, c := range pathsCases {
		score, diagnostics := notation.Parse([]byte(pathsHead + c.source + "\n"))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatalf("targeted score %q does not compile: %v", c.name, d)
			}
		}
		out = append(out, pathsScore{name: "targeted: " + c.name, score: score, assetRoot: "."})
	}
	return out
}

func TestRenderPathsAgree(t *testing.T) {
	examples, unreadable := pathsExamples(t)
	all := append(examples, pathsTargeted(t)...)
	var found = map[string]string{}
	details := map[string][]string{}
	var compared, engineOnly, failed []string
	releaseOnly := 0
	reasons := map[string][]string{}
	type outcome struct{ direct, engine pathsRun }
	outcomes := make([]outcome, len(all))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < max(1, min(runtime.GOMAXPROCS(0), 8)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				s := all[i]
				outcomes[i].direct = pathsRender(s.score, false, s.assetRoot)
				if !strings.HasPrefix(outcomes[i].direct.err, "engine only: ") {
					outcomes[i].engine = pathsRender(s.score, true, s.assetRoot)
				}
			}
		}()
	}
	for i := range all {
		next <- i
	}
	close(next)
	wg.Wait()
	for i, s := range all {
		direct, engine := outcomes[i].direct, outcomes[i].engine
		if strings.HasPrefix(direct.err, "engine only: ") {
			engineOnly = append(engineOnly, s.name)
			reasons[direct.err] = append(reasons[direct.err], s.name)
			if strings.HasPrefix(s.name, "targeted: ") {
				t.Errorf("%s: targeted scores must render on both paths, got %s", s.name, direct.err)
			}
			continue
		}
		if direct.err != "" || engine.err != "" {
			failed = append(failed, fmt.Sprintf("%s: direct %q engine %q", s.name, direct.err, engine.err))
			continue
		}
		compared = append(compared, s.name)
		if sig, _ := pathsDiff(pathsEvents{on: direct.events.off}, pathsEvents{on: engine.events.off}); sig != "" {
			releaseOnly++
		}
		if sig, detail := pathsDiff(direct.events, engine.events); sig != "" {
			found[s.name] = sig
			details[s.name] = detail
		}
	}
	t.Logf("%d scores differ in releases on bar lines (not gated, see above)", releaseOnly)
	t.Logf("compared %d scores (%d examples and %d targeted); %d examples render only on the engine; %d examples unreadable without their project", len(compared), len(compared)-countPrefix(compared, "targeted: "), countPrefix(compared, "targeted: "), len(engineOnly)-countPrefix(engineOnly, "targeted: "), len(unreadable))
	var reasonNames []string
	for r := range reasons {
		reasonNames = append(reasonNames, r)
	}
	sort.Strings(reasonNames)
	for _, r := range reasonNames {
		t.Logf("out of scope (%s): %d scores", r, len(reasons[r]))
	}
	for _, f := range failed {
		t.Errorf("a path could not render: %s", f)
	}
	known := map[string]pathsKnown{}
	for _, k := range pathsKnownDifferences {
		if _, dup := known[k.Score]; dup {
			t.Errorf("pathsKnownDifferences lists %q twice", k.Score)
		}
		if k.Construct == "" || k.Difference == "" || k.Owner == "" || k.Signature == "" {
			t.Errorf("pathsKnownDifferences entry %q must name the construct, the difference, the owner and the signature", k.Score)
		}
		known[k.Score] = k
	}
	var names []string
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		k, listed := known[name]
		switch {
		case !listed:
			t.Errorf("new difference between the render paths in %q: %s\n  %s", name, found[name], strings.Join(firstN(details[name], 8), "\n  "))
		case k.Signature != found[name]:
			t.Errorf("the known difference in %q changed: listed %q, now %q\n  %s", name, k.Signature, found[name], strings.Join(firstN(details[name], 8), "\n  "))
		}
	}
	for _, k := range pathsKnownDifferences {
		if _, still := found[k.Score]; !still {
			t.Errorf("the known difference in %q (%s, owned by %s) no longer appears: remove its entry from pathsKnownDifferences", k.Score, k.Difference, k.Owner)
		}
	}
}

func countPrefix(names []string, prefix string) int {
	n := 0
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			n++
		}
	}
	return n
}

func firstN(lines []string, n int) []string {
	if len(lines) > n {
		return append(append([]string(nil), lines[:n]...), fmt.Sprintf("... and %d more", len(lines)-n))
	}
	return lines
}

// TestRenderPathsAudio is level (b). It renders every comparable score through
// both paths with the same options and compares the float32 output byte for
// byte, reporting the peak difference. It never fails: the result is a report.
// Run it with CICADA_PATHS_AUDIO=1 or =full.
func TestRenderPathsAudio(t *testing.T) {
	mode := os.Getenv("CICADA_PATHS_AUDIO")
	if mode == "" {
		t.Skip("set CICADA_PATHS_AUDIO=1 (first six bars at 44.1 kHz) or =full (whole songs at 48 kHz) for the audio comparison report")
	}
	pathsFull = mode == "full"
	examples, _ := pathsExamples(t)
	all := append(examples, pathsTargeted(t)...)
	same, differ := 0, 0
	for _, s := range all {
		direct := pathsRender(s.score, false, s.assetRoot)
		if direct.err != "" {
			continue
		}
		engine := pathsRender(s.score, true, s.assetRoot)
		if engine.err != "" {
			t.Logf("AUDIO %s: engine error %s", s.name, engine.err)
			continue
		}
		if bytes.Equal(direct.wav, engine.wav) {
			same++
			continue
		}
		differ++
		t.Logf("AUDIO %s: %s", s.name, pathsAudioSummary(direct.wav, engine.wav))
	}
	t.Logf("AUDIO %d scores byte-identical, %d differ", same, differ)
}

func pathsAudioSummary(a, b []byte) string {
	if len(a) != len(b) || len(a) < 44 {
		return fmt.Sprintf("lengths differ: %d and %d bytes", len(a), len(b))
	}
	var peak float64
	first := -1
	count := 0
	for i := 44; i+4 <= len(a); i += 4 {
		x := math.Float32frombits(binary.LittleEndian.Uint32(a[i:]))
		y := math.Float32frombits(binary.LittleEndian.Uint32(b[i:]))
		if x != y {
			count++
			if first < 0 {
				first = (i - 44) / 8
			}
			peak = max(peak, math.Abs(float64(x)-float64(y)))
		}
	}
	return fmt.Sprintf("%d samples differ, first at frame %d, peak difference %.6g", count, first, peak)
}
