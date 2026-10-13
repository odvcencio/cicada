package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// editKindsSource has one four-step pattern for each pattern voice kind: acid,
// drums, piano, modal, sampler, graph, graph chords and keyboard.
func editKindsSource(wav []byte) string {
	return fmt.Sprintf(`cicada 2
asset wave "wave.wav" { sha256="%x" format=wav frames=480 rate=48000Hz channels=1 }
sampler hit { asset=wave root=c3 mode=oneshot voices=4 }
instrument pluck { voice mono { out = sine(pitch) * env(gate, 200ms) * velocity * 0.1 } }
instrument pad { voice poly { out = sine(pitch) * env(gate, 300ms) * velocity * 0.1 } }
track acid1 acid {}
track kit drums {}
track grand piano {}
track marimba model_marimba {}
track smp hit {}
track lead pluck {}
track chords pad {}
track keysx clav {}
pattern pacid acid steps=4 { 1 . 5 . }
pattern pkit drums steps=4 { bd: x... sd: .... }
pattern ppiano notes steps=4 { c4 . e4 . }
pattern pmodal notes steps=4 { c4 . e4 . }
pattern psample notes steps=4 { c3 . g3 . }
pattern plead notes steps=4 { c4 . e4 . }
pattern pchords notes steps=4 { [c4 e4 g4] . e4 . }
pattern pkeys notes steps=4 { [c4 e4 g4] . e4 . }
scene main { acid1=pacid kit=pkit grand=ppiano marimba=pmodal smp=psample lead=plead chords=pchords keysx=pkeys }
song { main }
`, sha256.Sum256(wav))
}

func editKindsPlan(t *testing.T, source string) (*livePlan, string) {
	t.Helper()
	path := studioTestPath(t, source)
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "wave.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := buildLivePlan(path, []byte(source), 48_000)
	if err != nil {
		t.Fatalf("%v\n%s", err, source)
	}
	return plan, path
}

func TestPlanDiffEmitsStepPatchesForEveryPatternKind(t *testing.T) {
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	source := editKindsSource(wav)
	before, path := editKindsPlan(t, source)
	for _, test := range []struct {
		name, pattern, lane string
		track               string
	}{
		{"acid", "pacid", "", "acid1"}, {"drums", "pkit", "bd", "kit"}, {"piano", "ppiano", "", "grand"},
		{"modal", "pmodal", "", "marimba"}, {"sample", "psample", "", "smp"}, {"graph", "plead", "", "lead"},
		{"graph-chords", "pchords", "", "chords"}, {"keys", "pkeys", "", "keysx"},
	} {
		t.Run(test.name, func(t *testing.T) {
			edited, err := toggledSource([]byte(source), test.pattern, test.lane, 1)
			if err != nil {
				t.Fatal(err)
			}
			after, err := buildLivePlan(path, edited, 48_000)
			if err != nil {
				t.Fatal(err)
			}
			delta := planDiff(before, after)
			if delta.structural || len(delta.edits) != 1 || len(delta.params) != 0 || len(delta.bindings) != 0 {
				t.Fatalf("toggle must patch one step: %+v", delta)
			}
			step, ok := delta.edits[0].(stepChange)
			if !ok || step.track != before.tracks[test.track] || step.index != 1 {
				t.Fatalf("edit %+v", delta.edits[0])
			}
			if test.lane != "" {
				if lane := drum.Lane(step.packed & 0x7f); lane != drum.BD || step.packed>>9&1 != 1 {
					t.Fatalf("drum step must carry its lane and a gate: %#x", step.packed)
				}
			} else if step.packed>>9&1 != 1 {
				t.Fatalf("toggled step has no gate: %#x", step.packed)
			}
			assertDeltaReproducesPattern(t, before, after, delta)
		})
	}
}

func TestPlanDiffEmitsChordLengthAndMetaEdits(t *testing.T) {
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	source := editKindsSource(wav)
	before, path := editKindsPlan(t, source)
	for _, test := range []struct {
		name, from, to string
		check          func(t *testing.T, delta planDelta)
	}{
		{"chord on the graph poly track", "pattern pchords notes steps=4 { [c4 e4 g4] . e4 . }", "pattern pchords notes steps=4 { [c4 e4 a4] . e4 . }", func(t *testing.T, delta planDelta) {
			if len(delta.edits) != 1 {
				t.Fatalf("edits %+v", delta.edits)
			}
			if step, ok := delta.edits[0].(stepChange); !ok || step.chord == nil || step.chord.Count != 3 || step.chord.Notes[2] != 69 {
				t.Fatalf("chord edit %+v", delta.edits[0])
			}
		}},
		{"chord removed from the piano", "pattern ppiano notes steps=4 { c4 . e4 . }", "pattern ppiano notes steps=4 { c4 . [e4 g4] . }", func(t *testing.T, delta planDelta) {
			if step, ok := delta.edits[0].(stepChange); !ok || step.chord == nil || step.chord.Count != 2 {
				t.Fatalf("piano chord %+v", delta.edits)
			}
		}},
		{"pattern grows", "pattern pacid acid steps=4 { 1 . 5 . }", "pattern pacid acid steps=8 { 1 . 5 . 3 . 5 . }", func(t *testing.T, delta planDelta) {
			if _, ok := delta.edits[0].(lenChange); !ok {
				t.Fatalf("a length change must come first when a pattern grows: %+v", delta.edits)
			}
		}},
		{"pattern shrinks", "pattern pacid acid steps=4 { 1 . 5 . }", "pattern pacid acid steps=2 { 1 . }", func(t *testing.T, delta planDelta) {
			if _, ok := delta.edits[0].(lenChange); !ok || len(delta.edits) != 1 {
				t.Fatalf("shrinking only changes the length: %+v", delta.edits)
			}
		}},
		{"swing", "pattern pacid acid steps=4 { 1 . 5 . }", "pattern pacid acid steps=4 { swing = 54%\n 1 . 5 . }", func(t *testing.T, delta planDelta) {
			if meta, ok := delta.edits[0].(metaChange); !ok || meta.swing != 80 || len(delta.edits) != 1 {
				t.Fatalf("swing must be one metadata edit: %+v", delta.edits)
			}
		}},
		{"drum pattern grows", "pattern pkit drums steps=4 { bd: x... sd: .... }", "pattern pkit drums steps=8 { bd: x... ..x. sd: .... .... }", func(t *testing.T, delta planDelta) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !strings.Contains(source, test.from) {
				t.Fatalf("fixture lost %q", test.from)
			}
			after, err := buildLivePlan(path, []byte(strings.Replace(source, test.from, test.to, 1)), 48_000)
			if err != nil {
				t.Fatalf("fixture syntax: %v", err)
			}
			delta := planDiff(before, after)
			if delta.structural {
				t.Fatalf("structural: %s", delta.reason)
			}
			test.check(t, delta)
			assertDeltaReproducesPattern(t, before, after, delta)
		})
	}
}

func TestPlanDiffKeepsUnpatchableChangesStructural(t *testing.T) {
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	source := editKindsSource(wav)
	before, path := editKindsPlan(t, source)
	for name, edited := range map[string]string{
		"gate":           strings.Replace(source, "pattern pacid acid steps=4 { 1 . 5 . }", "pattern pacid acid steps=4 { gate = 80%\n 1 . 5 . }", 1),
		"song length":    strings.Replace(source, "song { main }", "song { main*2 }", 1),
		"pattern rename": strings.Replace(source, "pacid", "pacid2", -1),
	} {
		after, err := buildLivePlan(path, []byte(edited), 48_000)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if delta := planDiff(before, after); !delta.structural {
			t.Fatalf("%s must stay structural: %+v", name, delta)
		}
	}
}

func TestPlanDiffEmitsSceneBindingChanges(t *testing.T) {
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	source := strings.Replace(editKindsSource(wav), "scene main {", "pattern pacid2 acid steps=4 { 5 . 1 . }\nscene intro { acid1=pacid }\nscene mid { acid1=pacid2 }\nscene alt { acid1=pacid2 }\nscene main {", 1)
	source = strings.Replace(source, "song { main }", "song { intro mid main alt }", 1)
	before, path := editKindsPlan(t, source)
	edited := strings.Replace(source, "scene alt { acid1=pacid2 }", "scene alt { acid1=pacid }", 1)
	after, err := buildLivePlan(path, []byte(edited), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	delta := planDiff(before, after)
	if delta.structural || len(delta.bindings) != 1 || len(delta.edits) != 0 {
		t.Fatalf("binding change must patch alone: %+v", delta)
	}
	change := delta.bindings[0]
	if change.scene != before.scenes["alt"] || change.track != before.tracks["acid1"] || change.binding.Mode != engine.SceneSlot {
		t.Fatalf("binding change %+v", change)
	}
}

// assertDeltaReproducesPattern plays the old engine, sends the delta at a bar
// line and checks that its notes from the next bar on equal a fresh engine
// built from the new plan.
func assertDeltaReproducesPattern(t *testing.T, before, after *livePlan, delta planDelta) {
	t.Helper()
	const editTick = 2 * seq.TicksPerBar
	patched, err := engine.New(before.cfg)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := engine.New(after.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var commands []cmd.Command
	for _, edit := range delta.edits {
		c, _ := editCommand(edit)
		commands = append(commands, c)
		if step, ok := edit.(stepChange); ok && step.chord != nil {
			chord, err := chordCommand(step)
			if err != nil {
				t.Fatal(err)
			}
			commands = append(commands, chord)
		}
	}
	for _, change := range delta.bindings {
		commands = append(commands, cmd.Command{Op: cmd.OpSetSceneBinding, Track: 0xff, Index: change.scene, Arg0: uint32(change.track) | uint32(change.binding.Mode)<<8 | uint32(change.binding.Slot)<<16, Arg1: uint32(change.binding.Clip)})
	}
	for i := range commands {
		if commands[i].Op != cmd.OpSetSceneBinding {
			commands[i].Tick = editTick
		}
	}
	notes := func(e *engine.Engine, push bool) map[string]int {
		if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("play rejected")
		}
		counts := map[string]int{}
		var left, right [liveBlockFrames]float32
		var message cmd.Message
		for block := 0; block < 4*96_000/liveBlockFrames; block++ {
			if push && block == 100 {
				if !e.PushBatch(commands) {
					t.Fatal("edit batch rejected")
				}
			}
			e.Render(left[:], right[:])
			for e.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatalf("fault %d", message.A)
				}
				if message.Kind == cmd.NoteOn && message.Tick >= editTick+seq.TicksPerBar {
					counts[fmt.Sprintf("%d/%d/%d", message.Tick, message.Track, message.A)]++
				}
			}
		}
		return counts
	}
	got, want := notes(patched, true), notes(fresh, false)
	if len(want) == 0 {
		t.Fatal("the fresh engine produced no notes to compare")
	}
	for key, n := range want {
		if got[key] != n {
			t.Fatalf("patched engine lacks note %s (%d/%d)\n got %v\nwant %v", key, got[key], n, got, want)
		}
	}
	for key := range got {
		if want[key] == 0 {
			t.Fatalf("patched engine plays an extra note %s", key)
		}
	}
}
