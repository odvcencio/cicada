package render

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
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

// sceneSlideGlides renders score on one path and reports, for each note start,
// whether it glides from the note before (the Slide flag the voice receives).
func sceneSlideGlides(t *testing.T, score *notation.Score, scheduled bool) map[int64]bool {
	t.Helper()
	glides := map[int64]bool{}
	opts := Options{Bits: 32, TailSec: 0.1, NoteTap: func(_ int, event seq.Event) {
		if event.Kind == seq.NoteOn {
			glides[event.Tick] = event.Slide
		}
	}}
	var err error
	if scheduled {
		_, err = renderScheduleWAV(score, opts, io.Discard, "", 1)
	} else {
		_, err = WAV(score, opts, io.Discard)
	}
	if err != nil {
		t.Fatal(err)
	}
	return glides
}

// A slide from a chained pattern into the next scene glides on both paths. The
// direct mixer planned the transition at the start of the bar, before the chain
// moved on to cb inside it, so it never saw the sliding note; the engine counted
// the new pattern's step from cb's start (bar 2) instead of from the song's
// start, so with a swung target it aimed the glide one swing late and dropped
// it. The glide is checked through the note tap: the bytes differ from the
// score without the slide even when the glide is dropped, because the slide
// also lengthens the sliding note.
func TestSlideFromAChainAcrossASceneSwitch(t *testing.T) {
	for _, c := range []struct{ name, ay string }{
		{"straight target", "step = 1/1 5"},
		{"swung target", "step = 1/1 swing = 60% 5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			slide := chainSlideScore(t, "1~", c.ay)
			for _, scheduled := range []bool{false, true} {
				if glides := sceneSlideGlides(t, slide, scheduled); !glides[7680] {
					t.Errorf("scheduled %v: the note at bar 3 (tick 7680) does not glide: %v", scheduled, glides)
				}
			}
			if direct, engine := renderSceneSlide(t, slide, false), renderSceneSlide(t, slide, true); !bytes.Equal(direct, engine) {
				t.Errorf("direct %s, engine %s", sceneSlideHash(direct), sceneSlideHash(engine))
			}
		})
	}
}

// A chain advance is a restart, and the scheduler swings the new pattern's
// first step by its absolute index: cb starts at bar 2, step 1 of a whole-note
// grid, so its note starts at tick 3840 plus the swing (768). The engine must
// aim the glide from ca's sliding note at that start. (The direct mixer does
// not glide across a chain advance at all yet; that is a separate fix.)
func TestEngineSlidesAcrossAChainAdvanceIntoASwungPattern(t *testing.T) {
	score, ds := notation.Parse([]byte("cicada 2\ntempo 120\ntrack bass acid { chain = ca cb }\npattern ca { step = 1/1 3~ }\npattern cb { step = 1/1 swing = 60% 5 }\nscene x {}\nsong { x*4 }\n"))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	if glides := sceneSlideGlides(t, score, true); !glides[4608] {
		t.Fatalf("engine: the swung note at tick 4608 does not glide from ca: %v", glides)
	}
}
