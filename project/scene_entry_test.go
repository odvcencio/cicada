package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func sceneEntryDiagnostics(t *testing.T, source string) []notation.Diagnostic {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("score does not compile: %+v", ds)
	}
	var out []notation.Diagnostic
	for _, d := range ds {
		if d.Code == "CICADA-SCENE-ENTRY" {
			out = append(out, d)
		}
	}
	return out
}

const sceneEntryHead = `cicada 2
tempo 120
instrument m { voice poly { out = sine(pitch) * adsr(gate, 5ms, 100ms, 0.8, 100ms) * 0.3 } }
track low m {}
pattern idle { c3 . . . }
`

// A scene keeps the song's phase, so a four-bar pattern bound at bar 2 enters
// at its second bar. When that bar continues a note tied from the first, the
// note began before the scene: check says so, with the pattern, track, bar,
// step and the bar where the track next sounds.
func TestSceneEntryInsideATiedNoteWarns(t *testing.T) {
	source := sceneEntryHead + `pattern sub { step = 1/2 c2 - - - | e2 - - . }
scene a { low = idle }
scene b { low = sub }
song { a*1 b*4 }
`
	ds := sceneEntryDiagnostics(t, source)
	if len(ds) != 1 {
		t.Fatalf("got %d CICADA-SCENE-ENTRY diagnostics, want 1: %+v", len(ds), ds)
	}
	d := ds[0]
	t.Log(d.Message)
	for _, want := range []string{"pattern sub on track low enters at bar 2 on step 3 of 8", "inside the note that step 1 starts", "silent until @3.1.1", "arrange"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q lacks %q", d.Message, want)
		}
	}
	if d.Severity != "warning" || d.Position.Line != 8 {
		t.Errorf("severity %q at line %d, want a warning on the scene binding (line 8)", d.Severity, d.Position.Line)
	}
}

// A grid that does not divide the bar enters inside a cell; the warning gives
// the next note's position to the sixteenth, not the beat (here a 3/16 grid
// bound at bar 2 enters 240 ticks into its sixth cell, a c4 still held).
func TestSceneEntryInsideACellGivesTheNextNoteToTheSixteenth(t *testing.T) {
	source := sceneEntryHead + `pattern odd { step = 3/16 c4 d4 e4 f4 g4 }
scene a { low = idle }
scene b { low = odd }
song { a*1 b*2 }
`
	ds := sceneEntryDiagnostics(t, source)
	if len(ds) != 1 {
		t.Fatalf("got %d CICADA-SCENE-ENTRY diagnostics, want 1: %+v", len(ds), ds)
	}
	t.Log(ds[0].Message)
	for _, want := range []string{"enters at bar 2 on step 1 of 5", "after the start of its note", "silent until @2.1.3"} {
		if !strings.Contains(ds[0].Message, want) {
			t.Errorf("message %q lacks %q", ds[0].Message, want)
		}
	}
	for tick, want := range map[int64]string{0: "@1.1.1", 4560: "@2.1.4", 4600: "@2.1.4 plus 40 ticks", 7680 + 2*960 + 3*240: "@3.3.4"} {
		if got := songPosition(tick); got != want {
			t.Errorf("songPosition(%d) = %q, want %q", tick, got, want)
		}
	}
}

// Entries that start a note, a rest, or land after a note's gate are the
// ordinary phase rule and stay quiet, as do arrangements, which restart a
// pattern at each placement.
func TestSceneEntryOnANoteOrRestDoesNotWarn(t *testing.T) {
	for name, body := range map[string]string{
		"phase starts a note": `pattern one { step = 1/4 c4 - - - }
scene a { low = idle }
scene b { low = one }
song { a*2 b*2 }
`,
		"phase lands on a rest": `pattern sub { step = 1/2 c2 - . . }
scene a { low = idle }
scene b { low = sub }
song { a*1 b*2 }
`,
		"same pattern kept": `pattern sub { step = 1/2 c2 - - - | e2 - - . }
scene a { low = sub }
scene b { low = sub }
song { a*1 b*2 }
`,
		"arrangement": `pattern sub { step = 1/2 c2 - - - | e2 - - . }
arrange { place p low sub { at = @2.1.1 length = 2bars } }
`,
	} {
		t.Run(name, func(t *testing.T) {
			if ds := sceneEntryDiagnostics(t, sceneEntryHead+body); len(ds) != 0 {
				t.Errorf("unexpected warning: %+v", ds)
			}
		})
	}
}
