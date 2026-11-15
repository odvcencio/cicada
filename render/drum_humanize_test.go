package render

import (
	"bytes"
	"crypto/sha256"
	"io"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func drumHumanizeScore(t *testing.T, body string) *notation.Score {
	t.Helper()
	score, ds := notation.Parse([]byte("cicada 2\ntempo 120\ntrack kit drums {}\n" + body + "\n"))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	if UsesScheduledEngine(score, Options{}) {
		t.Fatal("the score must render on the direct mixer")
	}
	return score
}

// A drum lane step carries its lane number as its note on every render path,
// as the engine's lane patterns do (project/runtime.go, packProjectStep). The
// direct mixer used the General MIDI pitch from project.CompilePattern.
func TestDirectMixerDrumLaneStepsCarryTheirLane(t *testing.T) {
	score := drumHumanizeScore(t, "pattern beat drums { humanize = 12\n  bd: x...x...\n  sd: ....X...\n  ch: x.x?50.x.x.\n}\nscene x { kit = beat }\nsong { x }")
	semantic, ds := project.FromScore(score)
	if semantic == nil {
		t.Fatal(ds)
	}
	tracks, err := compileTracks(score, semantic, 48000)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for lane, pattern := range tracks[0].drumPatterns["beat"] {
		if pattern == nil {
			continue
		}
		for i := 0; i < int(pattern.Len); i++ {
			step, err := seq.UnpackStep(pattern.Steps[i])
			if err != nil {
				t.Fatal(err)
			}
			if step.Note != uint8(lane) {
				t.Fatalf("lane %s step %d has note %d, want its lane %d", drum.Names[lane], i, step.Note, lane)
			}
			checked++
		}
	}
	if checked != 24 {
		t.Fatalf("checked %d steps, want 24 (three lanes of eight)", checked)
	}
}

// drumHits renders score on one path and returns each hit's velocity, keyed by
// tick and lane. A hit's note is its lane; a General MIDI pitch (what the
// direct mixer reported before) is mapped back to its lane.
func drumHits(t *testing.T, score *notation.Score, scheduled bool) map[[2]int64]uint8 {
	t.Helper()
	lanes := map[uint8]uint8{}
	for lane, note := range drum.MIDINotes {
		lanes[note] = uint8(lane)
	}
	hits := map[[2]int64]uint8{}
	opts := Options{Bits: 32, TailSec: 0.1, NoteTap: func(_ int, event seq.Event) {
		if event.Kind != seq.NoteOn {
			return
		}
		lane := event.Note
		if mapped, ok := lanes[lane]; ok && drum.Lane(lane) >= drum.LaneCount {
			lane = mapped
		}
		key := [2]int64{event.Tick, int64(lane)}
		if _, ok := hits[key]; ok {
			t.Fatalf("two hits at tick %d on lane %d", event.Tick, lane)
		}
		hits[key] = event.Velocity
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
	return hits
}

// The same humanized drum score gives every hit the same velocity on both
// render paths: Studio plays the engine, and `cicada render` writes the direct
// mixer. Before the fix all 96 hits differed, because the humanize draw takes
// the step's note and the two paths stored different notes.
func TestDrumHumanizeDrawsTheSameVelocityOnBothPaths(t *testing.T) {
	const groove = "pattern groove drums { humanize = AMOUNT\n  bd: x...x...x...x...\n  sd: ....X.......x...\n  ch: xxxxxxxxxxxxxxxx\n  oh: ..x.......x.....\n}\nscene x { kit = groove }\nsong { x*4 }"
	score := drumHumanizeScore(t, strings.Replace(groove, "AMOUNT", "12", 1))
	written := drumHits(t, drumHumanizeScore(t, strings.Replace(groove, "AMOUNT", "0", 1)), true)
	direct, engine := drumHits(t, score, false), drumHits(t, score, true)
	if len(direct) != 96 || len(engine) != 96 || len(written) != 96 {
		t.Fatalf("hits: direct %d, engine %d, written %d, want 96 each", len(direct), len(engine), len(written))
	}
	same, humanized := 0, 0
	for key, velocity := range engine {
		if direct[key] == velocity {
			same++
		}
		if written[key] != velocity {
			humanized++
		}
	}
	if same != 96 {
		t.Fatalf("%d of 96 hits have the same velocity on both paths", same)
	}
	if humanized < 48 {
		t.Fatalf("only %d of 96 hits moved off the written velocity; the test needs humanize to act", humanized)
	}
}

// The render-paths gate's two drum humanize scores (#344), a kit of modelled
// percussion and a drum chain render the same bytes on both paths. (Kit lanes
// bound to a sample pack render only on the engine.)
func TestDrumHumanizeRendersTheSameBytesOnBothPaths(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"accents and chance steps", "pattern hh drums { humanize = 12\n  bd: X...x...x?60...x...\n  sd: ....X.......x?50...\n  ch: x.x.X.x.x?70x.x.x..\n}\nscene x { kit = hh }\nsong { x*4 }"},
		{"two scenes", "pattern a drums { humanize = 20\n  bd: x...x...x...x...\n  ch: x.x.x.x.x.x.x.x.\n}\npattern b drums { humanize = 20\n  bd: x.x.x.x.x.x.x.x.\n  sd: ....x.......x...\n}\nscene x { kit = a }\nscene y { kit = b }\nsong { x*2 y*2 x*2 }"},
		{"modelled kit", "kit hands { bd = model.kick sd = model.snare }\ntrack perc hands {}\npattern p drums { humanize = 16\n  bd: x...x...x...x...\n  sd: ..x...x...x...x.\n}\nscene x { perc = p }\nsong { x*2 }"},
		{"chain", "track loop drums { chain = a b }\npattern a drums { humanize = 16\n  bd: x...x...x...x...\n}\npattern b drums { humanize = 16\n  sd: x.x.x.x.x.x.x.x.\n}\nscene main {}\nsong { main*4 }"},
	} {
		t.Run(c.name, func(t *testing.T) {
			score := drumHumanizeScore(t, c.body)
			var direct, engine bytes.Buffer
			if _, err := WAV(score, Options{Bits: 32, TailSec: 0.5}, &direct); err != nil {
				t.Fatal(err)
			}
			if _, err := renderScheduleWAV(score, Options{Bits: 32, TailSec: 0.5}, &engine, "", 1); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(direct.Bytes(), engine.Bytes()) {
				t.Fatalf("direct %x, engine %x", sha256.Sum256(direct.Bytes()), sha256.Sum256(engine.Bytes()))
			}
		})
	}
}
