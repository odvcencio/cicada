package keyboard

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// Songs whose only parameter changes are automation lanes on a keyboard
// control: after a seek to the start, a seek to the middle, a loop restart and
// a replay after the song ended, the voice must sound like fresh playback of
// the same position, and the lane must run again.
func TestKeyboardAutomationOnlySongsRewindLikeFreshPlayback(t *testing.T) {
	for _, placement := range []bool{false, true} {
		name := "song"
		if placement {
			name = "placement"
		}
		t.Run(name, func(t *testing.T) { keyboardAutomationRewind(t, placement) })
	}
}

func keyboardAutomationRewind(t *testing.T, placement bool) {
	const barFrames = 96000
	type family struct{ patch, name string }
	for _, f := range []family{{"tine_ep", "gain"}, {"clav", "drive"}, {"organ_full", "rotary_mix"}, {"fm_ep", "gain"}, {"poly_keys", "output"}, {"string_machine", "output"}} {
		spec, err := DefaultSpec(f.patch)
		if err != nil {
			t.Fatal(err)
		}
		spec.Controls[127] = 8
		authored := spec.Controls[slotOf(t, f.patch, f.name)]
		id, ok := kernel.FindParam("keys." + FamilyName(f.patch) + "." + f.name)
		if !ok {
			t.Fatal("no registry row")
		}
		descriptor := kernel.Params[id]
		other := farFrom(f.name, authored, descriptor.Min, descriptor.Max)
		for _, lane := range []struct {
			name   string
			points []cmd.Command
			skip   int // frames to skip after the restart before comparing
		}{
			{"first point after zero", []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(other), Tick: seq.TicksPerBar}}, 0},
			// The tick-0 point glides on first play but is rebuilt at once on a
			// restart, so compare once the glide has settled.
			{"first point at zero", []cmd.Command{{Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(other), Tick: 0}, {Op: cmd.OpSetParam, Track: 0, Index: uint16(id), Arg0: math.Float32bits(authored), Tick: seq.TicksPerBar}}, 24000},
		} {
			for _, moment := range []string{"seek to the start", "seek to the middle", "listening range (seek and play)", "reset and play", "loop restart", "replay after the end"} {
				t.Run(f.patch+"/"+f.name+"/"+lane.name+"/"+moment, func(t *testing.T) {
					newEngine := func(points []cmd.Command, value float32) *engine.Engine {
						s := spec
						s.Controls[slotOf(t, f.patch, f.name)] = value
						cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 8, BPMMilli: 120000}
						cfg.Track[0].Kind, cfg.Track[0].Keys = engine.VoiceKeys, &s
						if placement {
							// An arrange schedule: one silent placement of three bars.
							cfg.Patterns = []engine.PatternBank{{}}
							cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 16, GatePercent: 50}
							cfg.Schedule = []engine.ScheduleEvent{
								{Kind: engine.SchedulePattern, Track: 0, Index: 0, Tick: 0, EndTick: 3 * seq.TicksPerBar, ID: 1},
								{Kind: engine.SchedulePatternEnd, Track: 0, Tick: 3 * seq.TicksPerBar, EndTick: 3 * seq.TicksPerBar, ID: 1},
							}
						} else {
							cfg.Scenes = []engine.Scene{{}}
							cfg.Song = []engine.SongEntry{{Scene: 0, Bars: 3}}
						}
						cfg.LoopSong = moment == "loop restart"
						cfg.Automation = append([]cmd.Command(nil), points...)
						e, err := engine.New(cfg)
						if err != nil {
							t.Fatal(err)
						}
						return e
					}
					history, fresh := newEngine(lane.points, authored), newEngine(lane.points, authored)
					window := lane.skip + 1024
					var l, r [128]float32
					play := cmd.Command{Op: cmd.OpPlay, Track: 0xff}
					render := func(e *engine.Engine, frames int) {
						for done := 0; done < frames; done += len(l) {
							e.Render(l[:], r[:])
						}
					}
					switch moment {
					case "seek to the start", "seek to the middle", "listening range (seek and play)", "reset and play":
						history.Push(play)
						render(history, barFrames+8000)
						seek := cmd.Command{Op: cmd.OpSeek, Track: 0xff}
						if moment == "seek to the middle" || moment == "listening range (seek and play)" {
							seek = cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1, Arg1: 200}
							fresh.Push(seek)
						}
						if moment == "reset and play" {
							history.Reset() // A host reset returns the voices and the transport to the start.
							history.Push(play)
						} else {
							history.Push(seek)
						}
						if moment == "listening range (seek and play)" {
							history.Push(play) // Hosts queue the pair for a range start.
						}
						fresh.Push(play)
						if lane.name == "first point after zero" {
							window = barFrames + 4096 // Include the lane running again.
						}
					default:
						// Loop restart and replay do not reset the voice, whose
						// oscillators keep running, so a fresh engine cannot match
						// them. Compare with an engine that lived the same history
						// with no automation and the expected restart value held.
						want := authored
						if lane.name == "first point at zero" {
							want = other
						}
						fresh = newEngine(nil, want)
						for _, e := range []*engine.Engine{history, fresh} {
							e.Push(play)
							render(e, barFrames+8000)
							if moment == "loop restart" {
								render(e, 3*barFrames-(barFrames+8000)+(len(l)-(3*barFrames-(barFrames+8000))%len(l))%len(l)) // Three bars: the loop wraps on the next frame.
							} else {
								for i := 0; i < 400000 && e.Playing(); i++ {
									e.Render(l[:], r[:])
								}
								if e.Playing() {
									t.Fatal("the song never ended")
								}
								e.Push(play)
							}
						}
					}
					for _, e := range []*engine.Engine{history, fresh} {
						for _, note := range []uint32{48, 60, 64, 67} {
							e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: note | 100<<8})
						}
					}
					var worst, peak float64
					for done := 0; done < window; done += len(l) {
						var hl, hr, fl, fr [128]float32
						history.Render(hl[:], hr[:])
						fresh.Render(fl[:], fr[:])
						if done < lane.skip {
							continue
						}
						for i := range hl {
							worst = math.Max(worst, math.Max(math.Abs(float64(hl[i]-fl[i])), math.Abs(float64(hr[i]-fr[i]))))
							peak = math.Max(peak, math.Abs(float64(fl[i])))
						}
					}
					var message cmd.Message
					for _, e := range []*engine.Engine{history, fresh} {
						for e.Poll(&message) {
							if message.Kind == cmd.Fault {
								t.Fatalf("engine fault %d", message.A)
							}
						}
					}
					if peak == 0 {
						t.Fatal("no sound to compare")
					}
					if worst > 1e-6 {
						t.Fatalf("differs from fresh playback by %g (peak %g)", worst, peak)
					}
				})
			}
		}
	}
}
