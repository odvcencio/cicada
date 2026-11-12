package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// A score must sound the same whichever path renders it: the direct mixer
// (WAV, when UsesScheduledEngine is false) or the scheduled engine
// (renderScheduleWAV, which Studio playback also uses). TestRenderPathsAgree
// renders the first pathsMaxBars bars of every example score the direct mixer
// can render, and a set of small scores for the constructs most likely to
// differ, through both paths at pathsRate, and compares two things:
//
//  1. Note events, from Options.NoteTap. Per track, every note start (tick,
//     pitch or chord, velocity, accent, slide; a drum hit by its lane) and,
//     except for drum hits, the tick at which the note ends. A note ends at
//     its first release or at the next note start on its track, whichever
//     comes first, and at the end of the render at the latest. Both paths are
//     read that way because they tap releases differently: the engine taps
//     only the release that takes effect, including the one a new note causes
//     (tapRelease before each note start, kernel/engine/pattern.go), while the
//     direct mixer taps every release the scheduler emits and the releases it
//     forces at a scene boundary (the NoteTap loop and tapRelease in
//     render/wav.go), but not the one a new note causes.
//  2. Audio: the bytes of both float32 renders, header and timing chunk
//     included. This sees what the events cannot: expression rows (neither
//     path taps seq.NoteExpression), sample playback, and every difference in
//     the voices, effects and mix. No render is kept: each worker holds the
//     direct render of one score while it compares the engine's render with it
//     as the engine writes it.
//
// Every difference must match pathsKnownDifferences exactly: a new difference
// fails, and so does a listed one that has gone (remove its entry then) or
// changed (update its signatures). Scores only the engine can render must
// match pathsEngineOnly exactly, so a routing change cannot remove a score from
// the comparison without a failure.
//
// What it cannot see: the scores in pathsEngineOnly; bars after pathsMaxBars
// (none today: every example is shorter, and the test logs how many scene
// switches the cap leaves out); other sample rates; and every render option it
// does not set (From, Normalize, dither, PCM bit depths, stems, MasterCache,
// export gains). TestRenderPathsAudio compares whole songs at 48 kHz and only
// reports (nightly, `make test-render-paths-audio`).

// pathsKnown is one difference between the paths that is understood and owned.
// Events and Audio are the signatures the comparison prints for the score, so
// a second difference in the same score does not hide behind the entry.
type pathsKnown struct {
	Score      string // example path under examples/, or "targeted: " and the case name
	Construct  string
	Difference string
	Owner      string // the pull request or note that owns the fix
	Events     string // the event signature, "" when the note events agree
	Audio      string // the audio signature, "" when the renders are byte-identical
}

// pathsKnownDifferences can only shrink: delete an entry in the pull request
// that fixes its difference.
var pathsKnownDifferences = []pathsKnown{
	// Slide (~) on the last step of a pattern, into the first note of the next
	// scene. With sixteenth-note steps both paths glide; with quarter-note
	// steps the engine holds the note to the bar line and glides, and the
	// direct mixer releases it at its gate and plays a plain note:
	// sceneTransitionFor (render/wav.go) finds the last step with
	// seq.TicksPerStep, not the pattern's own step length. Pull request #347
	// fixes it; delete this entry when #347 is on main, and nothing else changes.
	{"targeted: slide across a scene switch, quarter-note steps", "slide across a scene switch, pattern step other than a sixteenth", "the engine holds the note and slides into the next scene's first note, the direct mixer does not", "#347 (open)",
		"4 only direct, 4 only engine, notes 00b2ebc8", "68005 frames differ, first at frame 166478 (3.775 s), last at frame 441065"},
	// The same slide with gate = 100%: both paths tap the same notes with the
	// same ends, but the audio differs from the first scene switch (bar 3).
	// Probable cause, not proven: the pending pattern's release at the bar line
	// (render/wav.go, the hasPendingGate loop) reaches the voice before the
	// slide, while the engine suppresses that release (pendingSwitchSlides,
	// kernel/engine/pattern.go). #347 does not change it.
	{"targeted: slide across a scene switch, full gate", "slide across a scene switch with gate = 100%", "audio differs, cause not yet known (the note events agree)", "none yet (found reviewing #347)",
		"", "29996 frames differ, first at frame 176400 (4.000 s), last at frame 441056"},
	// Drum humanize: the velocity draw takes the step's note as an input
	// (kernel/seq/schedule.go:168). The engine's lane steps carry the lane
	// number (project/runtime.go:408, packProjectStep), as
	// kernel/seq/schedule.go:73-78 documents; the direct mixer's carry the
	// General MIDI pitch (project/compile.go:268-274, drumNotes at
	// compile.go:36), so every humanized hit gets a different velocity. Every
	// other input (seed, track, slot, iteration, step) agrees.
	{"targeted: drum humanize with accents and chance steps", "drum pattern with humanize", "every humanized hit has a different velocity", "none yet (root cause in pull request #344)",
		"49 only direct, 49 only engine, notes fbe68979", "353019 frames differ, first at frame 0 (0.000 s), last at frame 353019"},
	{"targeted: drum humanize across two scenes", "drum pattern with humanize", "every humanized hit has a different velocity", "none yet (root cause in pull request #344)",
		"66 only direct, 66 only engine, notes fc471b29", "529420 frames differ, first at frame 0 (0.000 s), last at frame 529419"},
	// A single-asset sampler plays bend and vibrato rows on the engine; the
	// direct mixer's sampler voice has no note expression and plays the notes
	// plain. Pull request #337 routes such scores to the engine
	// (hasSamplerExpression), after which only the engine can render this case:
	// delete the case and this entry then.
	{"targeted: expression rows on a sampler", "single-asset sampler with bend: and vibrato: rows", "the direct mixer drops the bend and vibrato", "#337 (open)",
		"", "265296 frames differ, first at frame 5514 (0.125 s), last at frame 347375"},
}

// pathsEngineOnly lists every example score that only the engine can render,
// with the reason scheduledEngineReason gives. The comparison skips them. The
// test fails when the set or a reason changes: list a score here only when its
// routing to the engine is intended, and remove it when the direct mixer can
// render it again.
var pathsEngineOnly = map[string]string{
	"arrangement-audition.cicada":         "performance pattern picked",
	"arrangement-tempo.cicada":            "an arrangement",
	"arrangement/named.cicada":            "an arrangement",
	"detail/instrument-modulation.cicada": "modulators",
	"detail/modulators.cicada":            "modulators",
	"drum-machines/main.cicada":           "an arrangement",
	"keys/sampled/bell_keys.cicada":       "sample packs",
	"keys/sampled/brass_stab.cicada":      "sample packs",
	"keys/sampled/clav.cicada":            "sample packs",
	"keys/sampled/clav_hollow.cicada":     "sample packs",
	"keys/sampled/clav_muted.cicada":      "sample packs",
	"keys/sampled/fm_bass.cicada":         "sample packs",
	"keys/sampled/fm_ep.cicada":           "sample packs",
	"keys/sampled/organ_full.cicada":      "sample packs",
	"keys/sampled/organ_jazz.cicada":      "sample packs",
	"keys/sampled/organ_soft.cicada":      "sample packs",
	"keys/sampled/poly_keys.cicada":       "sample packs",
	"keys/sampled/reed_ep.cicada":         "sample packs",
	"keys/sampled/reed_tremolo.cicada":    "sample packs",
	"keys/sampled/soft_pad.cicada":        "sample packs",
	"keys/sampled/string_machine.cicada":  "sample packs",
	"keys/sampled/sync_lead.cicada":       "sample packs",
	"keys/sampled/tine_bark.cicada":       "sample packs",
	"keys/sampled/tine_bell.cicada":       "sample packs",
	"keys/sampled/tine_ep.cicada":         "sample packs",
	"keys/sampled/tine_tremolo.cicada":    "sample packs",
	"keys/sampled/tonewheel_organ.cicada": "sample packs",
	"shared-gestures/main.cicada":         "performance pattern phrase",
	"songs/anywhere-you-are.cicada":       "audio clips",
	"songs/tidal-glass.cicada":            "an arrangement",
}

type pathsCase struct {
	name, source string
	asset        bool // the source uses take.wav: TAKE_SHA256 is replaced with its hash
}

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
	{name: "slide across a scene switch", source: `pattern ax { . . . . . . . . . . . . . . . 1~ }
pattern ay { 5 . . . . . . . . . . . . . . . }
scene x { bass = ax }
scene y { bass = ay }
song { x*2 y*1 x*1 y*2 }`},
	{name: "slide across a scene switch, full gate", source: `pattern ax { gate = 100% . . . . . . . . . . . . . . . 1~ }
pattern ay { 5 . . . . . . . . . . . . . . . }
scene x { bass = ax }
scene y { bass = ay }
song { x*2 y*1 x*1 y*2 }`},
	{name: "slide across a scene switch, quarter-note steps", source: `pattern ax { step = 1/4 . . . 1~ }
pattern ay { step = 1/4 5 . . . }
scene x { bass = ax }
scene y { bass = ay }
song { x*2 y*1 x*1 y*2 }`},
	{name: "drum humanize with accents and chance steps", source: `pattern hh drums { humanize = 12
  bd: X...x...x?60...x...
  sd: ....X.......x?50...
  ch: x.x.X.x.x?70x.x.x..
}
scene x { kit = hh }
song { x*4 }`},
	{name: "drum humanize across two scenes", source: `pattern a drums { humanize = 20
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
	// The chord makes the track a four-voice pool (project.GraphPolyphony),
	// which takes expression; without one, both paths play a poly voice's
	// expression rows as if they were absent, and the case would prove nothing.
	{name: "expression rows on a poly voice", source: `pattern e { [c4 e4] - - - g4 - - .
  bend: 0ct +50ct +100ct 0ct -50ct . 0ct .
  vibrato: 0ct . 12ct . 4ct . 0ct .
  pressure: 0 0.3 0.8 . 0.6 . 0.2 0
  timbre: . 0.6 0.9 . 0.4 . 0.5 .
}
scene x { lead = e }
song { x*4 }`},
	{name: "expression rows on a mono voice", source: `pattern e { c3 - - - g3 - - .
  bend: 0ct +50ct +100ct 0ct -50ct . 0ct .
  vibrato: 0ct . 12ct . 4ct . 0ct .
  pressure: 0 0.3 0.8 . 0.6 . 0.2 0
}
scene x { mono = e }
song { x*4 }`},
	{name: "expression rows on acid", source: `pattern e { c2 - - - g2 - - .
  bend: 0ct -25ct -50ct 0ct +25ct . 0ct .
  vibrato: 0ct . 4ct . 2ct . 0ct .
}
scene x { bass = e }
song { x*4 }`},
	{name: "expression rows on a sampler", asset: true, source: `asset take "take.wav" { sha256 = "TAKE_SHA256" format = wav frames = 120000 rate = 48000Hz channels = 2 }
sampler hit { asset = take root = c3 mode = loop voices = 2 }
track keys hit {}
pattern e { c3 - - - g3 - - .
  bend: 0ct +50ct +100ct 0ct -50ct . 0ct .
  vibrato: 0ct . 12ct . 4ct . 0ct .
}
scene x { keys = e }
song { x*4 }`},
	{name: "tied first note at a scene change", source: `pattern tf { step = 1/4 - . c4 - }
scene x { lead = idle }
scene y { lead = tf }
song { x*2 y*2 }`},
	{name: "tied first note at a tempo change", source: `pattern one { step = 1/4 c4 - - - }
scene x { lead = one }
scene y { lead = one tempo = 100 }
song { x*2 y*2 }`},
	{name: "tie into a pattern whose first step is a tie", source: `pattern ox { step = 1/4 . . c4 - }
pattern tf { step = 1/4 - . c4 - }
scene x { lead = ox }
scene y { lead = tf }
song { x*2 y*2 }`},
	{name: "mono voice with overlapping notes", source: `pattern o { gate = 100% c3 d3 - e3 g3 - - a3 }
scene x { mono = o }
song { x*4 }`},
	{name: "swing at a section change", source: `pattern s1 { swing = 60% c3 c3 c3 c3 c3 c3 c3 c3 }
pattern s2 { swing = 54% e3 e3 e3 e3 e3 e3 e3 e3 }
scene x { lead = s1 }
scene y { lead = s2 }
song { x*2 y*2 x*1 }`},
	{name: "pattern chain", source: `track chained m { chain = ca cb }
pattern ca { step = 1/4 c3 - - - }
pattern cb { step = 1/2 e3 - }
scene main {}
song { main*4 }`},
	{name: "full gate into a new pattern", source: `pattern full { step = 1/4 gate = 100% . . . g3 }
pattern start { step = 1/4 e4 . . . }
scene x { lead = full }
scene y { lead = start }
song { x*2 y*2 }`},
}

// pathsMaxBars bounds the gate's cost if a long example arrives. It is above
// the longest example today, so the gate renders every song whole: about 85 s
// of CPU for all of them, against 62 s for the first six bars, because most
// examples are short. pathsRate is a common export rate; the nightly report
// uses 48 kHz.
const (
	pathsMaxBars = 64
	pathsRate    = 44100
)

func pathsSongBars(score *notation.Score) int {
	bars := 0
	for _, entry := range score.Song {
		bars += entry.Bars
	}
	return bars
}

type pathsTapped struct {
	track int
	event seq.Event
}

// pathsRender renders the first bars of score through one path into w and
// returns the note starts and releases it tapped. engineOnly is the reason the
// direct mixer cannot render the score; it renders nothing then.
func pathsRender(score *notation.Score, scheduled bool, assetRoot string, bars, rate int, w io.Writer) (tapped []pathsTapped, engineOnly string, err error) {
	tap := func(track int, ev seq.Event) {
		if ev.Kind == seq.NoteOn || ev.Kind == seq.NoteOff {
			tapped = append(tapped, pathsTapped{track, ev})
		}
	}
	dither := false
	opts := Options{Bits: 32, SampleRate: rate, Block: 4096, TailSec: 0.1, Bars: bars, Dither: &dither, NoteTap: tap, AssetRoot: assetRoot}
	if scheduled {
		_, err = renderScheduleWAV(score, opts, w, "", 1)
		return tapped, "", err
	}
	if reason := scheduledEngineReason(score, opts); reason != "" {
		return nil, reason, nil
	}
	_, err = WAV(score, opts, w)
	return tapped, "", err
}

// pathsLaneOf maps a drum's General MIDI pitch to its lane.
var pathsLaneOf = func() map[uint8]uint8 {
	m := map[uint8]uint8{}
	for lane, note := range drum.MIDINotes {
		m[note] = uint8(lane)
	}
	return m
}()

func pathsDrumTracks(score *notation.Score, rate int) map[int]bool {
	drums := map[int]bool{}
	if p, _ := project.FromScore(score); p != nil {
		if cfg, err := project.CompileEngine(p, rate, 128); err == nil {
			for i := 0; i < cfg.Tracks; i++ {
				drums[i] = cfg.Track[i].Kind == engine.VoiceDrums
			}
		}
	}
	return drums
}

// pathsNotes turns tapped events into counted keys: one per note start, with
// the tick at which the note ends (see the comment at the top of the file).
func pathsNotes(tapped []pathsTapped, drums map[int]bool, endTick int64) map[string]int {
	type note struct {
		track int
		on    seq.Event
		end   int64
	}
	var notes []*note
	open := map[int]map[int64]*note{} // track -> note ID -> sounding note
	keys := map[string]int{}
	for _, tp := range tapped {
		ev := tp.event
		if drums[tp.track] {
			if ev.Kind == seq.NoteOn {
				// The engine taps a drum hit with its lane in Note, the direct
				// mixer with its General MIDI pitch; the sound is the same.
				lane := ev.Note
				if l, ok := pathsLaneOf[lane]; ok {
					lane = l
				}
				keys[fmt.Sprintf("track %d tick %d lane %d vel %d accent %v", tp.track, ev.Tick, lane, ev.Velocity, ev.Accent)]++
			}
			continue
		}
		switch ev.Kind {
		case seq.NoteOn:
			for id, n := range open[tp.track] {
				n.end = min(n.end, ev.Tick)
				delete(open[tp.track], id)
			}
			n := &note{track: tp.track, on: ev, end: endTick}
			if open[tp.track] == nil {
				open[tp.track] = map[int64]*note{}
			}
			open[tp.track][ev.NoteID] = n
			notes = append(notes, n)
		case seq.NoteOff:
			if n := open[tp.track][ev.NoteID]; n != nil {
				n.end = min(n.end, ev.Tick)
				delete(open[tp.track], ev.NoteID)
			}
		}
	}
	for _, n := range notes {
		ev := n.on
		keys[fmt.Sprintf("track %d tick %d note %d chord %v vel %d accent %v slide %v end %d", n.track, ev.Tick, ev.Note, ev.Notes[:ev.NoteCount], ev.Velocity, ev.Accent, ev.Slide, n.end)]++
	}
	return keys
}

// pathsEventDiff returns "" when both paths played the same notes. Otherwise it
// returns the signature: how many notes each path played that the other did
// not, and a hash of the sorted list of those notes.
func pathsEventDiff(direct, engine map[string]int) (signature string, detail []string) {
	keys := map[string]bool{}
	for k := range direct {
		keys[k] = true
	}
	for k := range engine {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	onlyDirect, onlyEngine := 0, 0
	for _, k := range sorted {
		switch d := direct[k] - engine[k]; {
		case d > 0:
			onlyDirect += d
			detail = append(detail, fmt.Sprintf("only direct x%d: %s", d, k))
		case d < 0:
			onlyEngine -= d
			detail = append(detail, fmt.Sprintf("only engine x%d: %s", -d, k))
		}
	}
	if len(detail) == 0 {
		return "", nil
	}
	sum := sha256.Sum256([]byte(strings.Join(detail, "\n")))
	return fmt.Sprintf("%d only direct, %d only engine, notes %s", onlyDirect, onlyEngine, hex.EncodeToString(sum[:4])), detail
}

// pathsAudioDiff compares a float32 stereo WAV, as it is written, with a
// reference render, and keeps only the comparison.
type pathsAudioDiff struct {
	ref                []byte
	n                  int
	dataStart, dataEnd int
	frames             int // data frames that differ
	first, last        int // first and last differing frame, -1 when none
	other              int // header or timing-chunk bytes that differ
	peak               float64
	lastSample         int
}

func newPathsAudioDiff(ref []byte) *pathsAudioDiff {
	d := &pathsAudioDiff{ref: ref, first: -1, last: -1, lastSample: -1}
	if len(ref) >= 44 && string(ref[36:40]) == "data" {
		d.dataStart = 44
		d.dataEnd = 44 + int(binary.LittleEndian.Uint32(ref[40:44]))
	}
	return d
}

func (d *pathsAudioDiff) Write(p []byte) (int, error) {
	start := d.n
	d.n += len(p)
	end := min(d.n, len(d.ref))
	if start >= end || bytes.Equal(p[:end-start], d.ref[start:end]) {
		return len(p), nil
	}
	for i := start; i < end; i++ {
		if p[i-start] == d.ref[i] {
			continue
		}
		if i < d.dataStart || i >= d.dataEnd {
			d.other++
			continue
		}
		if frame := (i - d.dataStart) / 8; frame != d.last {
			d.frames++
			if d.first < 0 {
				d.first = frame
			}
			d.last = frame
		}
		// The peak, for the log only: both copies of the sample must be in
		// this write, which holds for the renderers' frame-aligned writes.
		if s := d.dataStart + (i-d.dataStart)/4*4; s != d.lastSample && s >= start && s+4 <= end {
			d.lastSample = s
			a := math.Float32frombits(binary.LittleEndian.Uint32(d.ref[s:]))
			b := math.Float32frombits(binary.LittleEndian.Uint32(p[s-start:]))
			d.peak = max(d.peak, math.Abs(float64(a)-float64(b)))
		}
	}
	return len(p), nil
}

// signature is "" when the two renders are byte-identical.
func (d *pathsAudioDiff) signature(rate int) string {
	var parts []string
	if d.n != len(d.ref) {
		parts = append(parts, fmt.Sprintf("lengths differ: %d bytes direct, %d engine", len(d.ref), d.n))
	}
	if d.frames > 0 {
		parts = append(parts, fmt.Sprintf("%d frames differ, first at frame %d (%.3f s), last at frame %d", d.frames, d.first, float64(d.first)/float64(rate), d.last))
	}
	if d.other > 0 {
		parts = append(parts, fmt.Sprintf("%d header bytes differ", d.other))
	}
	return strings.Join(parts, "; ")
}

type pathsScore struct {
	name      string
	score     *notation.Score
	assetRoot string
}

type pathsResult struct {
	engineOnly string // why only the engine can render the score; nothing was compared
	err        string
	events     string
	detail     []string
	audio      string
	peak       float64
}

// pathsCompare renders the first bars of one score through both paths at rate
// and compares them.
func pathsCompare(s pathsScore, bars, rate int) pathsResult {
	var direct bytes.Buffer
	directEvents, reason, err := pathsRender(s.score, false, s.assetRoot, bars, rate, &direct)
	if reason != "" {
		return pathsResult{engineOnly: reason}
	}
	if err != nil {
		return pathsResult{err: "direct mixer: " + err.Error()}
	}
	audio := newPathsAudioDiff(direct.Bytes())
	engineEvents, _, err := pathsRender(s.score, true, s.assetRoot, bars, rate, audio)
	if err != nil {
		return pathsResult{err: "engine: " + err.Error()}
	}
	drums := pathsDrumTracks(s.score, rate)
	endTick := int64(bars) * seq.TicksPerBar
	events, detail := pathsEventDiff(pathsNotes(directEvents, drums, endTick), pathsNotes(engineEvents, drums, endTick))
	return pathsResult{events: events, detail: detail, audio: audio.signature(rate), peak: audio.peak}
}

// pathsCompareAll runs pathsCompare over scores on up to eight goroutines.
func pathsCompareAll(scores []pathsScore, bars func(*notation.Score) int, rate int) []pathsResult {
	results := make([]pathsResult, len(scores))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < max(1, min(runtime.GOMAXPROCS(0), 8)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				results[i] = pathsCompare(scores[i], bars(scores[i].score), rate)
			}
		}()
	}
	for i := range scores {
		next <- i
	}
	close(next)
	wg.Wait()
	return results
}

func pathsExamples(t *testing.T) []pathsScore {
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
	var scores []pathsScore
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
			t.Errorf("example %s does not load on its own, so neither path can be compared: %v %v", rel, err, diagnostics)
			continue
		}
		scores = append(scores, pathsScore{name: rel, score: score, assetRoot: filepath.Dir(file)})
	}
	return scores
}

func pathsTargeted(t *testing.T) []pathsScore {
	t.Helper()
	dir := t.TempDir()
	take := testwav.Bytes(48000, 2, 32, 120000, 3)
	for i := 0; i < 120000; i++ {
		sample := float32(math.Sin(float64(i)*.09) * .2)
		binary.LittleEndian.PutUint32(take[44+i*8:], math.Float32bits(sample))
		binary.LittleEndian.PutUint32(take[48+i*8:], math.Float32bits(-sample*.3))
	}
	if err := os.WriteFile(filepath.Join(dir, "take.wav"), take, 0o600); err != nil {
		t.Fatal(err)
	}
	takeSum := sha256.Sum256(take)
	var out []pathsScore
	for _, c := range pathsCases {
		source := c.source
		if c.asset {
			source = strings.ReplaceAll(source, "TAKE_SHA256", hex.EncodeToString(takeSum[:]))
		}
		score, diagnostics := notation.Parse([]byte(pathsHead + source + "\n"))
		for _, d := range diagnostics {
			if d.Severity == "error" {
				t.Fatalf("targeted score %q does not compile: %v", c.name, d)
			}
		}
		out = append(out, pathsScore{name: "targeted: " + c.name, score: score, assetRoot: dir})
	}
	return out
}

// pathsSceneSwitches counts the bars at which the song changes scene, and how
// many of them come at or after bar limit (zero-based), where the gate does not
// render.
func pathsSceneSwitches(score *notation.Score, limit int) (total, beyond int) {
	bar := 0
	for i, entry := range score.Song {
		if i > 0 && entry.Scene != score.Song[i-1].Scene {
			total++
			if bar >= limit {
				beyond++
			}
		}
		bar += entry.Bars
	}
	return total, beyond
}

func TestRenderPathsAgree(t *testing.T) {
	all := append(pathsExamples(t), pathsTargeted(t)...)
	results := pathsCompareAll(all, func(score *notation.Score) int { return min(pathsMaxBars, pathsSongBars(score)) }, pathsRate)
	known := map[string]pathsKnown{}
	for _, k := range pathsKnownDifferences {
		if _, dup := known[k.Score]; dup {
			t.Errorf("pathsKnownDifferences lists %q twice", k.Score)
		}
		if k.Construct == "" || k.Difference == "" || k.Owner == "" || k.Events+k.Audio == "" {
			t.Errorf("pathsKnownDifferences entry %q must name the construct, the difference, the owner and at least one signature", k.Score)
		}
		known[k.Score] = k
	}
	found := map[string]bool{}
	engineOnly := map[string]string{}
	compared, examples, switches, unchecked, uncheckedScores := 0, 0, 0, 0, 0
	for i, s := range all {
		r := results[i]
		targeted := strings.HasPrefix(s.name, "targeted: ")
		switch {
		case r.engineOnly != "" && targeted:
			t.Errorf("%s: a targeted score must render on both paths, but only the engine can render it (%s). The paths can no longer disagree on it: delete the case and its pathsKnownDifferences entry", s.name, r.engineOnly)
			continue
		case r.engineOnly != "":
			engineOnly[s.name] = r.engineOnly
			continue
		case r.err != "":
			t.Errorf("%s: a render path failed: %s", s.name, r.err)
			continue
		}
		compared++
		if !targeted {
			examples++
			total, beyond := pathsSceneSwitches(s.score, pathsMaxBars)
			switches += total
			unchecked += beyond
			if beyond > 0 {
				uncheckedScores++
			}
		}
		if r.events == "" && r.audio == "" {
			continue
		}
		found[s.name] = true
		k, listed := known[s.name]
		switch {
		case !listed:
			t.Errorf("new difference between the render paths in %q:\n  events: %q\n  audio: %q (peak difference %.6g)\n  %s", s.name, r.events, r.audio, r.peak, strings.Join(firstN(r.detail, 8), "\n  "))
		case k.Events != r.events || k.Audio != r.audio:
			t.Errorf("the known difference in %q changed:\n  listed events %q\n  now    events %q\n  listed audio %q\n  now    audio %q (peak difference %.6g)\n  %s", s.name, k.Events, r.events, k.Audio, r.audio, r.peak, strings.Join(firstN(r.detail, 8), "\n  "))
		}
	}
	for _, k := range pathsKnownDifferences {
		if !found[k.Score] {
			t.Errorf("the known difference in %q (%s, owned by %s) no longer appears: remove its entry from pathsKnownDifferences", k.Score, k.Difference, k.Owner)
		}
	}
	var names []string
	for name := range engineOnly {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		switch listed, ok := pathsEngineOnly[name]; {
		case !ok:
			t.Errorf("%s renders only on the engine (%s), so the comparison no longer covers it: fix the routing, or add %q: %q to pathsEngineOnly", name, engineOnly[name], name, engineOnly[name])
		case listed != engineOnly[name]:
			t.Errorf("%s renders only on the engine for a new reason: listed %q, now %q", name, listed, engineOnly[name])
		}
	}
	names = names[:0]
	for name := range pathsEngineOnly {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := engineOnly[name]; !ok {
			t.Errorf("pathsEngineOnly lists %s (%s), but it is no longer an example that only the engine renders: remove it so the comparison covers it", name, pathsEngineOnly[name])
		}
	}
	t.Logf("compared %d scores (%d examples, %d targeted) over their first %d bars at %d Hz; %d examples render only on the engine", compared, examples, compared-examples, pathsMaxBars, pathsRate, len(engineOnly))
	t.Logf("%d of %d scene switches in the compared examples come after bar %d and are not checked (in %d scores)", unchecked, switches, pathsMaxBars, uncheckedScores)
}

func firstN(lines []string, n int) []string {
	if len(lines) > n {
		return append(append([]string(nil), lines[:n]...), fmt.Sprintf("... and %d more", len(lines)-n))
	}
	return lines
}

// TestRenderPathsAudio renders every example and targeted score through both
// paths over the whole song at 48 kHz and reports each difference. It never
// fails. Run it with CICADA_PATHS_AUDIO=full (`make test-render-paths-audio`).
func TestRenderPathsAudio(t *testing.T) {
	if os.Getenv("CICADA_PATHS_AUDIO") != "full" {
		t.Skip("set CICADA_PATHS_AUDIO=full to compare whole songs at 48 kHz")
	}
	all := append(pathsExamples(t), pathsTargeted(t)...)
	results := pathsCompareAll(all, pathsSongBars, 48000)
	same, differ := 0, 0
	for i, s := range all {
		r := results[i]
		switch {
		case r.engineOnly != "":
			continue
		case r.err != "":
			t.Logf("AUDIO %s: %s", s.name, r.err)
		case r.events == "" && r.audio == "":
			same++
		default:
			differ++
			t.Logf("AUDIO %s: events %q; audio %q (peak difference %.6g)", s.name, r.events, r.audio, r.peak)
		}
	}
	t.Logf("AUDIO %d scores identical, %d differ", same, differ)
}
