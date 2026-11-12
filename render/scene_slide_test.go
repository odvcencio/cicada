package render

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// sceneSlideSong is a bass track that plays pattern ax in scene x and pattern
// ay in scene y, in the given song order.
func sceneSlideSong(t *testing.T, ax, ay, song string) *notation.Score {
	t.Helper()
	source := "cicada 2\ntempo 120\ntrack bass acid {}\npattern ax { " + ax + " }\npattern ay { " + ay + " }\nscene x { bass = ax }\nscene y { bass = ay }\nsong { " + song + " }\n"
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	return score
}

// sceneSlideScore is the reviewer's case: a quarter-note pattern whose last
// note slides (or not) into the next scene's pattern at bar 3.
func sceneSlideScore(t *testing.T, last string) *notation.Score {
	t.Helper()
	return sceneSlideSong(t, "step = 1/4 . . . "+last, "step = 1/4 5 . . .", "x*2 y*2")
}

// renderSceneSlide renders score on the direct mixer or on the engine.
func renderSceneSlide(t *testing.T, score *notation.Score, scheduled bool) []byte {
	t.Helper()
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

func sceneSlideHash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b))[:12] }

// A slide on a pattern's last note glides into the next scene's first note
// (docs/spec/edition-1.md: a pattern or scene switch supplies the target). The
// direct mixer counted the sliding note on the fixed sixteenth grid, so with a
// quarter-note grid it never matched the note that played and dropped the
// slide: its render with the slide was byte-identical to the one without,
// while the engine glided. Both paths must render the slide, and the same
// bytes.
func TestSlideAcrossASceneSwitchOnBothRenderPaths(t *testing.T) {
	slide, plain := sceneSlideScore(t, "1~"), sceneSlideScore(t, "1")
	directSlide, directPlain := renderSceneSlide(t, slide, false), renderSceneSlide(t, plain, false)
	engineSlide := renderSceneSlide(t, slide, true)
	if bytes.Equal(directSlide, directPlain) {
		t.Errorf("direct mixer: the slide rendered like no slide (%s)", sceneSlideHash(directSlide))
	}
	if !bytes.Equal(directSlide, engineSlide) {
		t.Errorf("slide across the switch: direct %s, engine %s", sceneSlideHash(directSlide), sceneSlideHash(engineSlide))
	}
}

// On a 1/2t or 1/1 grid a bar line can fall on an odd step of the new
// pattern, and a swung pattern starts that step late by its swing. The glide
// aims at that later start on the engine (switchOnsetSample in
// kernel/engine/pattern.go); the direct mixer aimed at the bar line and
// dropped the slide. In this song the switches into y fall on target steps 3
// and 9 (1/2t) or 1 and 3 (1/1), all odd.
func TestSlideIntoASwungOddStepAcrossASceneSwitch(t *testing.T) {
	for _, c := range []struct{ name, ax, ay string }{
		{"half-note triplets", "step = 1/2t . . 1~", "step = 1/2t swing = 60% 5 . ."},
		{"whole notes", "step = 1/1 1~", "step = 1/1 swing = 60% 5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			const song = "x*1 y*1 x*1 y*1"
			slide := sceneSlideSong(t, c.ax, c.ay, song)
			plain := sceneSlideSong(t, strings.TrimSuffix(c.ax, "~"), c.ay, song)
			directSlide, directPlain := renderSceneSlide(t, slide, false), renderSceneSlide(t, plain, false)
			engineSlide := renderSceneSlide(t, slide, true)
			if bytes.Equal(directSlide, directPlain) {
				t.Errorf("direct mixer: the slide rendered like no slide (%s)", sceneSlideHash(directSlide))
			}
			if !bytes.Equal(directSlide, engineSlide) {
				t.Errorf("slide into a swung odd step: direct %s, engine %s", sceneSlideHash(directSlide), sceneSlideHash(engineSlide))
			}
		})
	}
}

// At gate = 100% the sliding note's own release lands on the bar line, the
// same sample as the next scene's first note. The engine keeps a glide's
// source note until its target starts; the direct mixer released it first
// from the outgoing pattern's pending gate and played the target unslid.
func TestSlideAtFullGateAcrossASceneSwitch(t *testing.T) {
	slide := sceneSlideSong(t, "gate = 100% . . . . . . . . . . . . . . . 1~", "5 . . . . . . . . . . . . . . .", "x*2 y*1 x*1 y*2")
	direct, engine := renderSceneSlide(t, slide, false), renderSceneSlide(t, slide, true)
	if !bytes.Equal(direct, engine) {
		t.Errorf("slide at full gate: direct %s, engine %s", sceneSlideHash(direct), sceneSlideHash(engine))
	}
}

// chainSlideScore is a bass track whose chain plays ca, then cb (whose one
// note slides), so cb starts at bar 2, before a scene switches the track to ay
// at bar 3. ay's grid is a whole note, so bar 3 is its step 2.
func chainSlideScore(t *testing.T, last, ay string) *notation.Score {
	t.Helper()
	source := "cicada 2\ntempo 120\ntrack bass acid { chain = ca cb }\npattern ca { step = 1/1 3 }\npattern cb { step = 1/1 " + last + " }\npattern ay { " + ay + " }\nscene x {}\nscene y { bass = ay }\nsong { x*2 y*2 }\n"
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	return score
}

// A slide from a chained pattern into the next scene glides on both paths. The
// direct mixer planned the transition at the start of the bar, before the chain
// moved on to cb inside it, so it never saw the sliding note; the engine counted
// the new pattern's step from cb's start (bar 2) instead of from the song's
// start, so with a swung target it aimed the glide one swing late and dropped
// it.
func TestSlideFromAChainAcrossASceneSwitch(t *testing.T) {
	for _, c := range []struct{ name, ay string }{
		{"straight target", "step = 1/1 5"},
		{"swung target", "step = 1/1 swing = 60% 5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			slide, plain := chainSlideScore(t, "1~", c.ay), chainSlideScore(t, "1", c.ay)
			direct, engine := renderSceneSlide(t, slide, false), renderSceneSlide(t, slide, true)
			if bytes.Equal(direct, renderSceneSlide(t, plain, false)) {
				t.Errorf("direct mixer: the slide rendered like no slide (%s)", sceneSlideHash(direct))
			}
			if bytes.Equal(engine, renderSceneSlide(t, plain, true)) {
				t.Errorf("engine: the slide rendered like no slide (%s)", sceneSlideHash(engine))
			}
			if !bytes.Equal(direct, engine) {
				t.Errorf("direct %s, engine %s", sceneSlideHash(direct), sceneSlideHash(engine))
			}
		})
	}
}
