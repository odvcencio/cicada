package main

import (
	"fmt"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

func deltaCommands(t *testing.T, delta planDelta, tick int64) []cmd.Command {
	t.Helper()
	var commands []cmd.Command
	for _, edit := range delta.edits {
		c, _ := editCommand(edit)
		c.Tick = tick
		commands = append(commands, c)
		if step, ok := edit.(stepChange); ok && step.chord != nil {
			chord, err := chordCommand(step)
			if err != nil {
				t.Fatal(err)
			}
			chord.Tick = tick
			commands = append(commands, chord)
		}
	}
	for _, change := range delta.bindings {
		commands = append(commands, cmd.Command{Op: cmd.OpSetSceneBinding, Track: 0xff, Index: change.scene, Arg0: uint32(change.track) | uint32(change.binding.Mode)<<8 | uint32(change.binding.Slot)<<16, Arg1: uint32(change.binding.Clip)})
	}
	return commands
}

// Shortening a pattern leaves its later steps in the engine. The plan must
// model that state, or a later transpose and growth validates against zeros
// while the engine sees a retained note out of range and faults.
func TestPlanDiffModelsStepsRetainedBeyondAShortenedLength(t *testing.T) {
	wav := testwav.Bytes(48000, 1, 16, 480, 1)
	base := editKindsSource(wav)
	for _, pattern := range []struct{ name, id, track string }{
		{"piano", "ppiano", "grand"}, {"keys", "pkeys", "keysx"}, {"graph", "plead", "lead"}, {"modal", "pmodal", "marimba"},
	} {
		t.Run(pattern.name, func(t *testing.T) {
			line := func(steps int, transpose bool, body string) string {
				attr := ""
				if transpose {
					attr = "transpose = 24\n"
				}
				return fmt.Sprintf("pattern %s notes steps=%d { %s%s }", pattern.id, steps, attr, body)
			}
			var old string
			for _, l := range strings.Split(base, "\n") {
				if strings.HasPrefix(l, "pattern "+pattern.id+" ") {
					old = l
				}
			}
			// Step 2 holds b6 (107): playable now, out of range once transposed up 24.
			original := line(4, false, "c4 . b6 .")
			source := strings.Replace(base, old, original, 1)
			stages := []string{
				line(2, false, "c4 ."), // shorten: step 2 stays in the engine
				line(2, true, "c4 ."),  // transpose: the retained b6 is now out of range
				line(4, true, "c4 . e4 ."),
			}
			cur, _ := editKindsPlan(t, source)
			patched, err := engine.New(cur.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !patched.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				t.Fatal("play rejected")
			}
			var left, right [liveBlockFrames]float32
			var message cmd.Message
			barBlocks := 96_000 / liveBlockFrames
			run := func(e *engine.Engine, blocks int, notes map[string]int) {
				for range blocks {
					e.Render(left[:], right[:])
					for e.Poll(&message) {
						if message.Kind == cmd.Fault {
							t.Fatalf("fault %d at tick %d", message.A, message.Tick)
						}
						if notes != nil && message.Kind == cmd.NoteOn && message.Track == cur.tracks[pattern.track] {
							notes[fmt.Sprintf("%d/%d", message.Tick%seq.TicksPerBar, message.A)]++
						}
					}
				}
			}
			tick := int64(seq.TicksPerBar)
			for _, stage := range stages {
				next, _ := editKindsPlan(t, strings.Replace(source, original, stage, 1))
				delta := planDiff(cur, next)
				if delta.structural {
					t.Fatalf("stage %q is structural: %s", stage, delta.reason)
				}
				delta.adopt(next)
				if !patched.PushBatch(deltaCommands(t, delta, tick)) {
					t.Fatal("edit batch rejected")
				}
				run(patched, 2*barBlocks, nil)
				tick += 2 * seq.TicksPerBar
				cur = next
			}
			fresh, err := engine.New(cur.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !fresh.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				t.Fatal("play rejected")
			}
			// One more bar on each engine, compared by position in the bar.
			got, want := map[string]int{}, map[string]int{}
			run(patched, barBlocks, got)
			run(fresh, barBlocks, want)
			if len(want) == 0 {
				t.Fatal("no notes to compare")
			}
			for k := range want {
				if got[k] == 0 {
					t.Fatalf("patched engine lacks note %s\n got %v\nwant %v", k, got, want)
				}
			}
			for k := range got {
				if want[k] == 0 {
					t.Fatalf("patched engine plays extra note %s", k)
				}
			}
		})
	}
}
