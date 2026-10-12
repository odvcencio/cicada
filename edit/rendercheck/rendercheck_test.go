package rendercheck

import (
	edits "m31labs.dev/cicada/edit"
	"path/filepath"
	"strings"
	"testing"
)

const head = "title \"t\"\ntempo 130\nkey a minor\ntrack bass acid {}\ntrack drums drums {}\n"

const body = `pattern pulse acid {
  1 . . . 5 . . . 1 . . . 7 . . .
}
pattern beat drums {
  bd: X... .... X... ....
  sd: .... X... .... X...
  ch: x.x. x.x. x.x. x.x.
}
`

func hashes(t *testing.T, a, b string) (string, string) {
	t.Helper()
	t.Parallel()
	check := New(filepath.Join(t.TempDir(), "main.cicada"))
	ha, err := check.Hash([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	hb, err := check.Hash([]byte(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(ha) != 64 {
		t.Fatalf("hash %q", ha)
	}
	return ha, hb
}

// Engine rules these cases record (measured, not assumed):
//   - A placement starts its pattern from its own tick origin, so a song entry
//     whose bars are a whole number of pattern lengths renders like the matching
//     placement, and a pattern that does not fit the entry length restarts out of
//     phase at the next entry.
//   - A drums placement boundary is audible even when the next placement binds
//     the same pattern: two adjacent drum placements render differently from
//     one placement (or one song run) covering the same bars. Acid placements do
//     not show this. A converter therefore cannot split a drum run at a scene
//     boundary and keep the render, and the check refuses those conversions.

const templateScenes = "scene main { bass = pulse drums = beat }\nscene break { bass = pulse drums = beat }\n"

func templateArrange(blocks string) string {
	return "cicada 2\n" + head + body + templateScenes + "arrange {\n" + blocks + "}\n"
}

func templateHashes(t *testing.T, sources ...string) []string {
	t.Helper()
	t.Parallel()
	check := New(filepath.Join(t.TempDir(), "main.cicada"))
	var out []string
	for _, source := range sources {
		h, err := check.Hash([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

// Case 1: the new-project template as song and as one placement per contiguous
// same-pattern run (what ConvertSongToArrange writes) render the same.
func TestRenderCheckTemplateSongEqualsMergedArrange(t *testing.T) {
	song := head + body + templateScenes + "song { main*4 break*4 }\n"
	merged := templateArrange("  place main-1-bass bass pulse { at = @1.1.1 length = 8bars }\n  place main-1-drums drums beat { at = @1.1.1 length = 8bars }\n")
	if h := templateHashes(t, song, merged); h[0] != h[1] {
		t.Fatal("merged placements differ from the song")
	}
}

// Case 1 as the plan hand-wrote it (one placement per scene entry and track)
// documents an ENGINE GAP, not a rule to keep: adjacent drums placements render
// differently from one run covering the same bars, with the difference starting
// at the placement boundary. Acid placements are unaffected. When the engine is
// fixed this test fails on purpose; then flip the assertion to equal and drop
// the drums-specific SplitClip render check.
func TestRenderCheckSplitDrumPlacementsDifferFromSongEngineGap(t *testing.T) {
	song := head + body + templateScenes + "song { main*4 break*4 }\n"
	split := templateArrange("  place main-1-bass bass pulse { at = @1.1.1 length = 4bars }\n  place main-1-drums drums beat { at = @1.1.1 length = 4bars }\n" +
		"  place break-2-bass bass pulse { at = @5.1.1 length = 4bars }\n  place break-2-drums drums beat { at = @5.1.1 length = 4bars }\n")
	if h := templateHashes(t, song, split); h[0] == h[1] {
		t.Fatal("split drum placements now render like the song: the engine gap is fixed; flip this test")
	}
}

// Case 1b: the acid-only template converts one placement per scene entry.
func TestRenderCheckAcidSongEqualsArrange(t *testing.T) {
	prefix := "title \"t\"\ntempo 130\ntrack bass acid {}\n" + body[:strings.Index(body, "pattern beat")]
	scenes := "scene main { bass = pulse }\nscene break { bass = pulse }\n"
	song := prefix + scenes + "song { main*4 break*4 }\n"
	arrange := "cicada 2\n" + prefix + scenes + "arrange {\n  place main-1-bass bass pulse { at = @1.1.1 length = 4bars }\n  place break-2-bass bass pulse { at = @5.1.1 length = 4bars }\n}\n"
	a, b := hashes(t, song, arrange)
	if a != b {
		t.Fatalf("hashes differ:\n%s\n%s", a, b)
	}
}

// twoBar is 32 steps: two bars at the default sixteenth step.
const twoBar = "pattern two acid {\n  1 . . . 5 . . . 1 . . . 7 . . . 3 . . . 5 . . . 3 . . . 7 . . .\n}\n"

// Case 2: a 2-bar pattern in a 3-bar entry restarts out of phase at the next entry.
func TestRenderCheckOutOfPhaseDiffers(t *testing.T) {
	prefix := "title \"t\"\ntempo 130\ntrack bass acid {}\n"
	pat := twoBar
	scenes := "scene a { bass = two }\nscene b { bass = two }\n"
	song := prefix + pat + scenes + "song { a*3 b*3 }\n"
	arrange := "cicada 2\n" + prefix + pat + scenes + "arrange {\n  place a-1-bass bass two { at = @1.1.1 length = 3bars }\n  place b-2-bass bass two { at = @4.1.1 length = 3bars }\n}\n"
	a, b := hashes(t, song, arrange)
	if a == b {
		t.Fatal("out-of-phase conversion rendered identically")
	}
}

// Case 3: scene settings do not survive the conversion.
func TestRenderCheckSceneSettingsDiffer(t *testing.T) {
	prefix := "title \"t\"\ntempo 130\ntrack bass acid {}\npattern pulse { 1 . 5 . 1 . 5 . 1 . 5 . 1 . 5 . }\n"
	song := prefix + "scene main { bass = pulse\n  bass.level = -12dB }\nsong { main*2 }\n"
	arrange := "cicada 2\n" + prefix + "scene main { bass = pulse }\narrange {\n  place main-1-bass bass pulse { at = @1.1.1 length = 2bars }\n}\n"
	a, b := hashes(t, song, arrange)
	if a == b {
		t.Fatal("scene setting did not change the render")
	}
}

// Case 4: a 2-bar pattern in 2-bar entries renders the same as placements.
func TestRenderCheckInPhaseEqual(t *testing.T) {
	prefix := "title \"t\"\ntempo 130\ntrack bass acid {}\n"
	pat := twoBar
	scenes := "scene a { bass = two }\nscene b { bass = two }\n"
	song := prefix + pat + scenes + "song { a*2 b*2 }\n"
	arrange := "cicada 2\n" + prefix + pat + scenes + "arrange {\n  place a-1-bass bass two { at = @1.1.1 length = 2bars }\n  place b-2-bass bass two { at = @3.1.1 length = 2bars }\n}\n"
	a, b := hashes(t, song, arrange)
	if a != b {
		t.Fatalf("hashes differ:\n%s\n%s", a, b)
	}
}

func TestRenderCheckRefusesInvalidScore(t *testing.T) {
	t.Parallel()
	_, err := New(filepath.Join(t.TempDir(), "main.cicada")).Hash([]byte("scene a { bass = nope }\nsong { a*1 }\n"))
	if err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatalf("invalid score hashed: %v", err)
	}
}

type countingCheck struct{ calls int }

func (c *countingCheck) Hash(source []byte) (string, error) {
	c.calls++
	return string(source), nil
}

func TestCachedRendersEachRevisionOnce(t *testing.T) {
	inner := &countingCheck{}
	check := CachedBy(func(source []byte) (string, bool) { return edits.Revision(source), true }, inner)
	for _, source := range []string{"a", "b", "a", "b", "a"} {
		if got, err := check.Hash([]byte(source)); err != nil || got != source {
			t.Fatalf("%s: %q, %v", source, got, err)
		}
	}
	if inner.calls != 2 {
		t.Fatalf("inner called %d times, want 2", inner.calls)
	}
	for i := 0; i < 100; i++ {
		if _, err := check.Hash([]byte{byte(i), 1}); err != nil {
			t.Fatal(err)
		}
	}
	if len(check.(*cached).by) > cachedLimit {
		t.Fatalf("cache holds %d results", len(check.(*cached).by))
	}
}
