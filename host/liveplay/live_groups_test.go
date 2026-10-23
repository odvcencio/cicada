package liveplay

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func sharedLivePlayer(t *testing.T, voices int) *Player {
	t.Helper()
	cfg := engine.Config{SampleRate: 48000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: voices, BPMMilli: 120000}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceGraph, Graph: graph.Program{Len: 10, Output: 9, Nodes: [graph.MaxNodes]graph.Node{
		{Op: graph.Pressure}, {Op: graph.Gate}, {Op: graph.Constant, Value: 1}, {Op: graph.Constant}, {Op: graph.Constant, Value: 1800},
		{Op: graph.ADSR, A: 1, B: 3, C: 3, D: 2, E: 4}, {Op: graph.Multiply, A: 0, B: 5},
		{Op: graph.Pitch}, {Op: graph.Sine, A: 7}, {Op: graph.Multiply, A: 6, B: 8},
	}}}
	if voices == 4 {
		cfg.Track[0].Polyphony = 4
	} else if voices == 8 {
		cfg.Track[0].Kind = engine.VoiceGraphPoly
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Tracks: []TrackSlots{{ID: "voice", Kind: "custom", Pitched: true}}}, 48000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestLiveGroupOwnershipThroughReleaseAndSlotReuse(t *testing.T) {
	for _, voices := range []int{1, 4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) {
			p := sharedLivePlayer(t, voices)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			var pcm [blockFrames * 8]byte
			read := func() {
				t.Helper()
				_, err := p.Read(pcm[:])
				must(err)
			}
			must(p.NoteOwnerID("voice", 60, 100, true, "note-a", 7))
			must(p.NoteExpressionOwner("voice", 7, "note-a", 0, .3, .5))
			must(p.BindNoteGroupOwner("voice", 7, "foreign", "foreign-group"))
			must(p.BindNoteGroupOwner("voice", 7, "note-a", "input-a"))
			must(p.GroupExpressionOwner("voice", "input-a", 0, .2, .5))
			read()
			if p.liveGroups[0].Owner != "input-a" || p.liveGroups[1].Owner != "" {
				t.Fatal("foreign owner created a binding", p.liveGroups[:2])
			}
			must(p.NoteOwnerID("voice", 60, 0, false, "note-a", 7))
			read()
			must(p.GroupExpressionOwner("voice", "foreign-group", 0, 1, .5))
			must(p.GroupExpressionOwner("voice", "input-a", 0, .8, .5))
			read()
			if !p.current.Engine.LiveGroupActive(0, 1) || math.Float32frombits(p.liveGroups[0].Controls.Arg1) != .8 {
				t.Fatal("released voice lost owned shared controls")
			}
			// A burst still closes its gates and never binds an unsounded press.
			must(p.NoteOwnerID("voice", 64, 100, true, "burst", 8))
			must(p.BindNoteGroupOwner("voice", 8, "burst", "input-b"))
			must(p.NoteOwnerID("voice", 64, 0, false, "burst", 8))
			read()
			if p.current.Engine.LiveGroupActive(0, 2) {
				t.Fatal("same-read release reopened a group")
			}
			// Wait for actual voices, rather than a timer or arbitrary note count,
			// before reclaiming group IDs. Reused IDs cannot address a prior tail.
			for range 1000 {
				read()
			}
			must(p.NoteOwnerID("voice", 67, 100, true, "new-note", 7))
			must(p.BindNoteGroupOwner("voice", 7, "new-note", "input-new"))
			must(p.GroupExpressionOwner("voice", "input-new", 0, .25, .5))
			read()
			must(p.GroupExpressionOwner("voice", "input-a", 0, 1, .5))
			read()
			for _, group := range p.liveGroups {
				if group.Owner == "input-new" && math.Float32frombits(group.Controls.Arg1) != .25 {
					t.Fatal("retired input addressed reused group")
				}
			}
			for i := 0; i < len(pcm); i += 4 {
				if value := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:])); math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					t.Fatal("nonfinite shared audio")
				}
			}
		})
	}
}

func TestLiveGroupAudioChangesOnlyAfterReleasedMasterMovement(t *testing.T) {
	for _, voices := range []int{1, 4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) {
			render := func(move bool) []byte {
				p := sharedLivePlayer(t, voices)
				p.NoteOwnerID("voice", 60, 100, true, "note", 7)
				p.BindNoteGroupOwner("voice", 7, "note", "master")
				p.NoteExpressionOwner("voice", 7, "note", 0, .2, .5)
				p.GroupExpressionOwner("voice", "master", 0, .3, .5)
				result := make([]byte, blockFrames*8*12)
				for block := range 12 {
					if block == 2 {
						p.NoteOwnerID("voice", 60, 0, false, "note", 7)
					}
					if block == 4 {
						p.NoteExpressionOwner("voice", 7, "note", 0, 1, .5) // ignored released member
						if move {
							p.GroupExpressionOwner("voice", "master", 0, .8, .5)
						}
					}
					if _, err := p.Read(result[block*blockFrames*8 : (block+1)*blockFrames*8]); err != nil {
						t.Fatal(err)
					}
				}
				return result
			}
			a, b := render(false), render(true)
			first, peak := -1, float64(0)
			for i := 0; i < len(a); i += 4 {
				delta := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(a[i:])) - math.Float32frombits(binary.LittleEndian.Uint32(b[i:]))))
				if delta > 0 && first < 0 {
					first = i / 8
				}
				peak = max(peak, delta)
			}
			if first < blockFrames*4 || first >= blockFrames*5 || peak < .01 {
				t.Fatal("late master movement was lost or changed earlier audio", first, peak)
			}
			t.Logf("gate released at frame %d; first shared difference %d; peak delta %.6f", blockFrames*2, first, peak)
		})
	}
}

func TestLiveGroupTurnoverKeepsAnOlderHeldInput(t *testing.T) {
	for _, voices := range []int{4, 8} {
		t.Run(fmt.Sprint(voices), func(t *testing.T) {
			p := sharedLivePlayer(t, voices)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			var pcm [blockFrames * 8]byte
			read := func() {
				_, err := p.Read(pcm[:])
				must(err)
			}
			must(p.NoteOwnerID("voice", 48, 100, true, "held", 1))
			must(p.BindNoteGroupOwner("voice", 1, "held", "old-input"))
			read()
			for index := range 600 {
				group := fmt.Sprint("new-input-", index)
				must(p.NoteOwnerID("voice", 72, 100, true, "new-note", 2))
				must(p.BindNoteGroupOwner("voice", 2, "new-note", group))
				must(p.GroupExpressionOwner("voice", group, 0, .2, .5))
				read()
				must(p.NoteOwnerID("voice", 72, 0, false, "new-note", 2))
				read()
			}
			must(p.GroupExpressionOwner("voice", "old-input", 0, .9, .5))
			read()
			if p.liveGroups[0].Owner != "old-input" || !p.current.Engine.LiveGroupActive(0, 1) || math.Float32frombits(p.liveGroups[0].Controls.Arg1) != .9 {
				t.Fatal("group turnover evicted an older sounding input")
			}
			active := 0
			for _, group := range p.liveGroups {
				if group.Controls.Op != 0 {
					active++
				}
			}
			if active > voices || p.liveGroupLimit > voices+1 {
				t.Fatal("retired input groups accumulated", active, p.liveGroupLimit)
			}
			if allocs := testing.AllocsPerRun(100, read); allocs != 0 {
				t.Fatal("shared live audio reader allocated", allocs)
			}
		})
	}
}
