package scoreaudition

import (
	"crypto/sha256"
	"fmt"
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
	dir := t.TempDir()
	asset := testwav.Bytes(48000, 2, 16, 96000, 1)
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
