package scoreaudition

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
)

// These tests check that the report follows the notes the renderer plays:
// section chains, the phase of scene-selected patterns, mid-bar placements
// and audio clips.

func analyzeIn(t *testing.T, source, dir string) *Analysis {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatalf("parse: %s", d.Error())
		}
	}
	a, err := Analyze(score, Options{TempDir: t.TempDir(), AssetDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSectionChainWrittenNotesAndSections(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "section-chain.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	a := analyzeIn(t, string(source), "")
	t.Logf("%s", textOf(a))
	written := 0
	for _, b := range a.Report.Bars {
		if b.Written != "" {
			written++
		}
	}
	if written == 0 {
		t.Error("no written notes: the chained patterns were not seen")
	}
	bass := track(t, a, "bass")
	var sum float64
	for _, v := range bass.Rhythm {
		sum += v
	}
	if sum == 0 {
		t.Errorf("bass rhythm profile is all zero: %v", bass.Rhythm)
	}
	if len(a.Report.Sections) == 0 || !slices.Contains(a.Report.Sections[0].Tracks, "bass") {
		t.Errorf("sections %+v do not list bass", a.Report.Sections)
	}
}

const introThenChords = `cicada 2
title "Phase"
tempo 100
key c major
seed 1

instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 200ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}

track keys pad {}

pattern intro-chord notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
}
pattern two-bar notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
  [f4 a4 c5] - - - - - - - - - - - - - - -
}
scene intro { keys = intro-chord }
scene main { keys = two-bar }
song {
  intro
  main*4
}
`

// A two-bar pattern started by a scene change keeps the global grid: it
// enters at its second bar, so the bars read C, F, C, F, C.
func TestScenePatternPhaseFollowsTheRenderer(t *testing.T) {
	a := analyze(t, introThenChords)
	t.Logf("%s", textOf(a))
	want := []string{"C", "F", "C", "F", "C"}
	for i, b := range a.Report.Bars {
		if b.Written != want[i] || b.Match != "agree" {
			t.Errorf("bar %d written %q heard %q match %s, want written %s agree", b.Bar, b.Written, b.Heard, b.Match, want[i])
		}
	}
	if len(a.Report.Bars) != len(want) {
		t.Fatalf("%d bars", len(a.Report.Bars))
	}
}

const midBarPlacement = `cicada 2
title "Mid bar"
tempo 120
key c major
seed 1

instrument tick {
  voice mono {
    out = square(pitch) * env(gate, 80ms) * velocity * 0.4
  }
}

track hits tick {}

pattern beats notes {
  c4 . . . c4 . . . c4 . . . c4 . . .
}
arrange {
  place run hits beats {
    at = @1.2.1
    length = 2bars
  }
  marker start { at = @1.1.1 }
}
`

// The placement runs from beat 2 of bar 1 to beat 2 of bar 3: 8 hits over
// three bars. The first bar counts as active.
func TestMidBarPlacementCountsEveryBar(t *testing.T) {
	a := analyze(t, midBarPlacement)
	t.Logf("%s", textOf(a))
	hits := track(t, a, "hits")
	if hits.ActiveBars != 3 {
		t.Errorf("active bars %d, want 3", hits.ActiveBars)
	}
	if hits.OnsetsPerBar < 2.2 || hits.OnsetsPerBar > 3.2 {
		t.Errorf("onsets per bar %.2f, want about 8/3", hits.OnsetsPerBar)
	}
	if got := a.Report.Bars[0].Written; got != "C" {
		t.Errorf("bar 1 written %q: its notes were dropped", got)
	}
}

func clipSource(t *testing.T, body string) (string, string) {
	t.Helper()
	return clipSourceWith(t, body, testwav.Bytes(48000, 2, 16, 96000, 1))
}

// stereoSine is a 16-bit stereo 220 Hz clip of 96000 frames at about -22 dBFS
// per channel. With invert the right channel has the opposite polarity.
func stereoSine(invert bool) []byte {
	data := testwav.Bytes(48000, 2, 16, 96000, 1)
	for i := 0; i < 96000; i++ {
		v := int16(math.Round(0.08 * 32767 * math.Sin(2*math.Pi*220*float64(i)/48000)))
		r := v
		if invert {
			r = -v
		}
		binary.LittleEndian.PutUint16(data[44+i*4:], uint16(v))
		binary.LittleEndian.PutUint16(data[46+i*4:], uint16(r))
	}
	return data
}

func clipSourceWith(t *testing.T, body string, asset []byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "clip.wav"), asset, 0o600); err != nil {
		t.Fatal(err)
	}
	head := fmt.Sprintf(`cicada 2
title "Clips"
tempo 120
key c major
seed 1
asset a "clip.wav" { sha256 = "%x" format = wav frames = 96000 rate = 48000Hz channels = 2 }
clip region a { start = 3frames end = 95000frames }
instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 200ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}
track vox audio {}
track keys pad {}
pattern chord notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
}
`, sha256.Sum256(asset))
	return head + body, dir
}

func TestSceneAudioClipListedInSection(t *testing.T) {
	source, dir := clipSource(t, `
scene verse { vox = region keys = chord }
song { verse*2 }
`)
	a := analyzeIn(t, source, dir)
	t.Logf("%s", textOf(a))
	if len(a.Report.Sections) != 1 || !slices.Contains(a.Report.Sections[0].Tracks, "vox") {
		t.Errorf("sections %+v do not list the clip track", a.Report.Sections)
	}
}

func TestArrangementAudioClipListedInSection(t *testing.T) {
	source, dir := clipSource(t, `
arrange {
  place v vox region { at = @1.1.1 length = 1bar }
  place k keys chord { at = @2.1.1 length = 1bar }
  marker one { at = @1.1.1 }
  marker two { at = @2.1.1 }
}
`)
	a := analyzeIn(t, source, dir)
	t.Logf("%s", textOf(a))
	if len(a.Report.Sections) != 2 {
		t.Fatalf("sections %+v", a.Report.Sections)
	}
	one, two := a.Report.Sections[0], a.Report.Sections[1]
	if !slices.Contains(one.Tracks, "vox") || slices.Contains(two.Tracks, "vox") || !slices.Contains(two.Tracks, "keys") {
		t.Errorf("section tracks: one %v two %v", one.Tracks, two.Tracks)
	}
	if !strings.Contains(textOf(a), "vox") {
		t.Error("text lacks vox")
	}
}

// Opposite-polarity stereo cancels when channels are averaged. The report must
// measure each channel, so the clip reads the same level as its in-phase twin.
func TestOppositePolarityStereoClipIsAudible(t *testing.T) {
	body := `
scene verse { vox = region keys = chord }
song { verse*2 }
`
	var reports [2]*Analysis
	for i, invert := range []bool{false, true} {
		source, dir := clipSourceWith(t, "", stereoSine(invert))
		reports[i] = analyzeIn(t, source+body, dir)
	}
	normal, inverted := track(t, reports[0], "vox"), track(t, reports[1], "vox")
	t.Logf("%s", textOf(reports[1]))
	if inverted.Silent || inverted.RMSdBFS < -60 {
		t.Fatalf("inverted clip reads silent: %+v", inverted)
	}
	if math.Abs(inverted.RMSdBFS-normal.RMSdBFS) > 0.5 || math.Abs(inverted.LevelVsLoudestDB-normal.LevelVsLoudestDB) > 0.5 {
		t.Errorf("levels differ: in phase %.1f/%.1f dB, opposite %.1f/%.1f dB", normal.RMSdBFS, normal.LevelVsLoudestDB, inverted.RMSdBFS, inverted.LevelVsLoudestDB)
	}
	if math.Abs(inverted.CentroidHz-normal.CentroidHz) > 50 {
		t.Errorf("centroid %.0f vs %.0f Hz", inverted.CentroidHz, normal.CentroidHz)
	}
	if !slices.Contains(reports[1].Report.Sections[0].Tracks, "vox") {
		t.Errorf("sections %+v do not list the inverted clip", reports[1].Report.Sections)
	}
	if got, want := reports[1].Report.Sections[0].RMSdBFS, reports[0].Report.Sections[0].RMSdBFS; math.Abs(got-want) > 0.5 {
		t.Errorf("section loudness %.1f vs %.1f dBFS", got, want)
	}
}

const tiedChord = `cicada 2
title "Tied"
tempo 120
key c major
seed 1

instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 200ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}

track keys pad {}

pattern held notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
  - - - - - - - - - - - - - - - -
  - - - - - - - - - - - - - - - -
  - - - - - - - - - - - - - - - -
}
scene s { keys = held }
song { s*4 }
`

// A four-bar tied chord has one tapped start and no release before the render
// stops. It is active in all four bars.
func TestSustainedNoteRunsToRenderEnd(t *testing.T) {
	score, _ := notation.Parse([]byte(tiedChord))
	for _, tc := range []struct{ from, bars, active int }{{0, 0, 4}, {1, 2, 2}, {2, 0, 2}} {
		a, err := Analyze(score, Options{From: tc.from, Bars: tc.bars, TempDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		keys := track(t, a, "keys")
		if keys.ActiveBars != tc.active {
			t.Errorf("from %d bars %d: active bars %d, want %d", tc.from, tc.bars, keys.ActiveBars, tc.active)
		}
		if tc.from == 0 && keys.OnsetsPerBar > 0.6 {
			t.Errorf("onsets per bar %.2f, want about 0.25", keys.OnsetsPerBar)
		}
		if !slices.Contains(a.Report.Sections[0].Tracks, "keys") {
			t.Errorf("section lacks keys: %+v", a.Report.Sections)
		}
	}
}

const heldThenOff = `cicada 2
title "Held then off"
tempo 120
key c major
seed 1

instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 100ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}

track keys pad {}
track vox audio {}

pattern held notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
  - - - - - - - - - - - - - - - -
}
pattern idle notes {
  . . . .
}
`

// A scene that stops a two-bar held note after one bar must end its activity there, on the scene
// renderer and on the engine renderer (an audio track selects the engine).
func TestSceneOffEndsHeldNote(t *testing.T) {
	scenes := `
scene a { keys = held }
scene b { keys = off }
song {
  a
  b*3
}
`
	t.Run("scene renderer", func(t *testing.T) {
		source := strings.Replace(heldThenOff, "track vox audio {}\n", "", 1) + scenes
		a := analyze(t, source)
		t.Logf("%s", textOf(a))
		checkHeldThenSilent(t, a, 1)
	})
	t.Run("engine renderer", func(t *testing.T) {
		full, dir := clipSource(t, "")
		body := strings.Replace(full, "pattern chord notes {\n  [c4 e4 g4] - - - - - - - - - - - - - - -\n}\n", "pattern held notes {\n  [c4 e4 g4] - - - - - - - - - - - - - - -\n  - - - - - - - - - - - - - - - -\n}\n", 1)
		a := analyzeIn(t, body+"\nscene a { keys = held }\nscene b { keys = off }\nsong {\n  a\n  b*3\n}\n", dir)
		t.Logf("%s", textOf(a))
		checkHeldThenSilent(t, a, 1)
	})
}

func checkHeldThenSilent(t *testing.T, a *Analysis, heldBars int) {
	t.Helper()
	keys := track(t, a, "keys")
	if keys.ActiveBars != heldBars {
		t.Errorf("keys active bars %d, want %d", keys.ActiveBars, heldBars)
	}
	for _, s := range a.Report.Sections {
		if s.Name == "b" && slices.Contains(s.Tracks, "keys") {
			t.Errorf("section b lists keys: %+v", s)
		}
	}
}

const placedThenSilent = `cicada 2
title "Placed"
tempo 120
key c major
seed 1

instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 100ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}
instrument quiet {
  voice mono {
    out = square(pitch) * env(gate, 60ms) * velocity * 0.0
  }
}

track keys pad {}
track later quiet {}

pattern held notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
  - - - - - - - - - - - - - - - -
}
pattern idle notes {
  c4 . . .
}
arrange {
  place a keys held { at = @1.1.1 length = 1bar }
  place b later idle { at = @3.1.1 length = 2bars }
  marker one { at = @1.1.1 }
  marker two { at = @3.1.1 }
}
`

func TestPlacementEndEndsHeldNote(t *testing.T) {
	a := analyze(t, placedThenSilent)
	t.Logf("%s", textOf(a))
	keys := track(t, a, "keys")
	if keys.ActiveBars != 1 {
		t.Errorf("keys active bars %d, want 1", keys.ActiveBars)
	}
	if two := a.Report.Sections[1]; slices.Contains(two.Tracks, "keys") {
		t.Errorf("section two lists keys: %+v", two)
	}
}

const mutedPad = `cicada 2
title "Mute"
tempo 120
key c major
seed 1

instrument pad {
  voice poly {
    out = saw(pitch) * env(gate, 200ms) * velocity * 0.0
  }
}
track keys pad {}
pattern chords notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
}
scene s { keys = chords }
song { s*2 }
`

// Written notes with no sound are `silent`, not a wrong chord.
func TestSilentWrittenBarIsNotADisagreement(t *testing.T) {
	a := analyze(t, mutedPad)
	t.Logf("%s", textOf(a))
	for _, b := range a.Report.Bars {
		if b.Written != "C" || b.Match != "silent" {
			t.Errorf("bar %d written %q match %q, want written C, silent", b.Bar, b.Written, b.Match)
		}
	}
	if a.Report.Mismatches != 0 {
		t.Errorf("%d mismatches, want 0", a.Report.Mismatches)
	}
}

func TestHeardFitStaysInRange(t *testing.T) {
	var v [12]float64
	v[0], v[4], v[7] = 1, 1, 1
	m := bestChord(v)
	if m.Name != "C" || m.Score < 0.99 || m.Score > 1 {
		t.Errorf("exact C triad: %+v", m)
	}
	for _, source := range []string{skankMajor, hornsMinor} {
		for _, b := range analyze(t, source).Report.Bars {
			if b.HeardFit < 0 || b.HeardFit > 1 {
				t.Errorf("bar %d heard_fit %v outside 0..1", b.Bar, b.HeardFit)
			}
		}
	}
}

const stabRestStab = `
pattern stab notes {
  [c4 e4 g4] . . . . . . . . . . . . . . .
}
pattern idle notes {
  . . . .
}
scene hit { keys = stab }
scene rest { keys = idle }
song {
  hit
  rest
  hit
}
`

// A note released before the selected range must not mark the range's first
// bar active, on the scene renderer and on the engine renderer (an audio track
// selects the engine).
func TestNoteReleasedBeforeRangeIsNotActive(t *testing.T) {
	check := func(t *testing.T, source, dir string) {
		t.Helper()
		score, ds := notation.Parse([]byte(source))
		for _, d := range ds {
			if d.Severity == "error" {
				t.Fatalf("parse: %s", d.Error())
			}
		}
		a, err := Analyze(score, Options{From: 1, Bars: 2, TempDir: t.TempDir(), AssetDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s", textOf(a))
		if keys := track(t, a, "keys"); keys.ActiveBars != 1 {
			t.Errorf("keys active bars %d, want 1", keys.ActiveBars)
		}
		for _, s := range a.Report.Sections {
			if s.Name == "rest" && slices.Contains(s.Tracks, "keys") {
				t.Errorf("section rest lists keys: %+v", s)
			}
		}
	}
	t.Run("scene renderer", func(t *testing.T) {
		source := strings.Replace(heldThenOff, "track vox audio {}\n", "", 1)
		source = source[:strings.Index(source, "pattern held notes")] + stabRestStab
		check(t, source, "")
	})
	t.Run("engine renderer", func(t *testing.T) {
		source, dir := clipSource(t, stabRestStab)
		check(t, source, dir)
	})
}

const midBarSections = `
track bass pad {}
pattern hit notes {
  [c4 e4 g4] . . . . . . . . . . . . . . .
}
arrange {
  place a keys hit { at = @1.1.1 length = 1920ticks }
  place b bass hit { at = @1.3.1 length = 1bar }
  marker intro { at = @1.1.1 }
  marker main { at = @1.3.1 }
}
`

const sceneHeader = `cicada 2
title "Sections"
tempo 120
key c major
seed 1
instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 200ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}
track keys pad {}
`

func checkMidBarSections(t *testing.T, source, dir string) {
	t.Helper()
	a := analyzeIn(t, source, dir)
	t.Logf("%s", textOf(a))
	secs := a.Report.Sections
	if len(secs) != 2 || secs[0].Name != "intro" || secs[1].Name != "main" {
		t.Fatalf("sections %+v, want intro and main", secs)
	}
	intro, main := secs[0], secs[1]
	if !slices.Contains(intro.Tracks, "keys") || slices.Contains(intro.Tracks, "bass") {
		t.Errorf("intro tracks %v, want keys only", intro.Tracks)
	}
	if !slices.Contains(main.Tracks, "bass") || slices.Contains(main.Tracks, "keys") {
		t.Errorf("main tracks %v, want bass only", main.Tracks)
	}
	if intro.FromBar != 1 || intro.ToBar != 1 || main.FromBar != 1 || main.ToBar != 2 {
		t.Errorf("bars intro %d-%d main %d-%d", intro.FromBar, intro.ToBar, main.FromBar, main.ToBar)
	}
	if intro.ToTick != 1920 || main.FromTick != 1920 {
		t.Errorf("ticks intro to %d main from %d, want 1920", intro.ToTick, main.FromTick)
	}
	// One chord in half a bar is 2 starts per bar; one in 1.5 bars is 0.67.
	if intro.OnsetsPerBar < 1.2 || intro.OnsetsPerBar > 2.8 || main.OnsetsPerBar < 0.3 || main.OnsetsPerBar > 1.0 {
		t.Errorf("onsets per bar intro %.2f main %.2f", intro.OnsetsPerBar, main.OnsetsPerBar)
	}
}

// A marker may sit inside a bar. Sections are measured between the marker
// ticks, so the half bar before the marker is its own section.
func TestMidBarMarkerSections(t *testing.T) {
	t.Run("scene renderer", func(t *testing.T) {
		checkMidBarSections(t, sceneHeader+midBarSections, "")
	})
	t.Run("engine renderer", func(t *testing.T) {
		source, dir := clipSource(t, midBarSections)
		checkMidBarSections(t, source, dir)
	})
}

const sparseDrums = `
track kit drums {}
pattern hit drums {
  ch: ..x. .... .... ....
}
pattern none drums {
  ch: .... .... .... ....
}
scene hit { kit = hit }
scene rest { kit = none }
song {
  hit
  rest
  rest
  hit
}
`

// A drum hit lasts no time. Silent bars between hits are not active, and the
// hits do not extend over them.
func TestSparseDrumsAreNotSustained(t *testing.T) {
	check := func(t *testing.T, source, dir string) {
		t.Helper()
		a := analyzeIn(t, source, dir)
		t.Logf("%s", textOf(a))
		kit := track(t, a, "kit")
		if kit.ActiveBars != 2 {
			t.Errorf("kit active bars %d, want 2", kit.ActiveBars)
		}
		if kit.OnsetsPerBar < 0.9 || kit.OnsetsPerBar > 1.1 {
			t.Errorf("kit onsets per bar %.2f, want 1", kit.OnsetsPerBar)
		}
		for _, s := range a.Report.Sections {
			if s.Name == "rest" && slices.Contains(s.Tracks, "kit") {
				t.Errorf("section rest lists kit: %+v", s)
			}
		}
	}
	t.Run("scene renderer", func(t *testing.T) {
		check(t, sceneHeader+sparseDrums, "")
	})
	t.Run("engine renderer", func(t *testing.T) {
		source, dir := clipSource(t, sparseDrums)
		check(t, source, dir)
	})
}

const quietPad = `cicada 2
title "Quiet"
tempo 120
key c major
seed 1
instrument pad {
  voice poly {
    out = saw(pitch) * env(gate, 200ms) * velocity * 0.0001
  }
}
track keys pad {}
pattern chords notes {
  [c4 e4 g4] - - - - - - - - - - - - - - -
}
scene s { keys = chords }
song { s*2 }
`

// A track under -80 dBFS with a nonzero stem is silent: every measure except
// rms_dbfs is zero, including its energy share.
func TestSilentTrackHasZeroShare(t *testing.T) {
	a := analyze(t, quietPad)
	t.Logf("%s", textOf(a))
	keys := track(t, a, "keys")
	if !keys.Silent || keys.RMSdBFS <= -120 {
		t.Fatalf("keys not a nonzero silent stem: %+v", keys)
	}
	if keys.EnergySharePct != 0 || keys.LevelVsLoudestDB != 0 {
		t.Errorf("silent track share %.1f level %.1f, want 0", keys.EnergySharePct, keys.LevelVsLoudestDB)
	}
}
