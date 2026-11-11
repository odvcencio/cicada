package render

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"

	"m31labs.dev/cicada/notation"
)

// sceneSlideScore is the reviewer's case: a quarter-note pattern whose last
// note slides (or not) into the next scene's pattern at bar 3.
func sceneSlideScore(t *testing.T, last string) *notation.Score {
	t.Helper()
	source := "cicada 2\ntempo 120\ntrack bass acid {}\npattern ax { step = 1/4 . . . " + last + " }\npattern ay { step = 1/4 5 . . . }\nscene x { bass = ax }\nscene y { bass = ay }\nsong { x*2 y*2 }\n"
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	return score
}

// A slide on a pattern's last note glides into the next scene's first note
// (docs/spec/edition-1.md: a pattern or scene switch supplies the target). The
// direct mixer counted the sliding note on the fixed sixteenth grid, so with a
// quarter-note grid it never matched the note that played and dropped the
// slide: its render with the slide was byte-identical to the one without,
// while the engine glided. Both paths must render the slide, and the same
// bytes.
func TestSlideAcrossASceneSwitchOnBothRenderPaths(t *testing.T) {
	render := func(score *notation.Score, scheduled bool) []byte {
		var out bytes.Buffer
		opts := Options{Bits: 32, TailSec: 0.1}
		var err error
		if scheduled {
			_, err = renderScheduleWAV(score, opts, &out, "", 1)
		} else {
			if UsesScheduledEngine(score, opts) {
				t.Fatal("the score must suit the direct mixer")
			}
			_, err = WAV(score, opts, &out)
		}
		if err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	hash := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b))[:12] }
	slide, plain := sceneSlideScore(t, "1~"), sceneSlideScore(t, "1")
	directSlide, directPlain := render(slide, false), render(plain, false)
	engineSlide := render(slide, true)
	if bytes.Equal(directSlide, directPlain) {
		t.Errorf("direct mixer: the slide rendered like no slide (%s)", hash(directSlide))
	}
	if !bytes.Equal(directSlide, engineSlide) {
		t.Errorf("slide across the switch: direct %s, engine %s", hash(directSlide), hash(engineSlide))
	}
}
