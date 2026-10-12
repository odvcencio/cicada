package keyboard

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

type restoreCase struct{ patch, name string }

// restoreCases lists every live control of every keyboard family. Organ
// controls run on organ_full (percussion off, so drawbar 9 sounds); the rotary
// speed also runs on tonewheel_organ, which starts slow.
func restoreCases() []restoreCase {
	cases := []restoreCase{
		{"tine_tremolo", "tremolo"}, {"tine_tremolo", "tremolo_rate"}, {"tine_tremolo", "auto_pan"}, {"tine_tremolo", "gain"}, {"tine_tremolo", "drive"},
		{"clav", "gain"}, {"clav", "drive"},
		{"organ_full", "gain"}, {"organ_full", "drive"}, {"organ_full", "rotary_mix"}, {"organ_full", "rotary_fast"}, {"tonewheel_organ", "rotary_fast"},
		{"fm_ep", "gain"},
		{"poly_keys", "drive"}, {"poly_keys", "output"},
		{"string_machine", "output"},
	}
	for n := 1; n <= 9; n++ {
		cases = append(cases, restoreCase{"organ_full", fmt.Sprintf("drawbar%d", n)})
	}
	return cases
}

// farFrom picks a value of the control well away from the authored one.
func farFrom(name string, authored, min, max float32) float32 {
	switch {
	case strings.HasPrefix(name, "drawbar"):
		if authored+4 > max {
			return authored - 4
		}
		return authored + 4
	case name == "rotary_fast":
		return 1 - authored
	}
	delta := (max - min) / 3
	if authored+delta <= max {
		return authored + delta
	}
	return authored - delta
}

// Seeking to the start must put every control at its authored value from the
// first frame, whatever the control and the moment of the seek. Two moments:
// shortly after a scene targeted the authored value, so a glide is unfinished;
// and well inside a scene that moved the control away, so the voice has settled
// on that value and the seek's voice reset has captured it. The sound
// afterwards must match a fresh engine sample for sample.
func TestSeekToStartRestoresEveryLiveControlImmediately(t *testing.T) {
	for _, c := range restoreCases() {
		for _, moment := range []struct {
			name   string
			frames int
		}{{"unfinished-glide", 96000 + 1280}, {"settled-in-moved-scene", 90000}} {
			t.Run(c.patch+"/"+c.name+"/"+moment.name, func(t *testing.T) {
				spec, err := DefaultSpec(c.patch)
				if err != nil {
					t.Fatal(err)
				}
				spec.Controls[127] = 8
				slot := slotOf(t, c.patch, c.name)
				authored := spec.Controls[slot]
				id, ok := kernel.FindParam("keys." + FamilyName(c.patch) + "." + c.name)
				if !ok {
					t.Fatalf("no registry row for %s", c.name)
				}
				descriptor := kernel.Params[id]
				far := farFrom(c.name, authored, descriptor.Min, descriptor.Max)
				if far == authored || far < descriptor.Min || far > descriptor.Max {
					t.Fatalf("no distinct value for %s (authored %g)", c.name, authored)
				}
				window := 1024
				if c.name == "rotary_fast" {
					window = 96000 // The rotor takes seconds to reach speed.
				}
				newEngine := func() *engine.Engine {
					s := spec
					cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 8, BPMMilli: 120000}
					cfg.Track[0].Kind, cfg.Track[0].Keys = engine.VoiceKeys, &s
					for _, v := range []float32{far, authored} {
						cfg.Scenes = append(cfg.Scenes, engine.Scene{Settings: []engine.SceneSetting{{Track: 0, ID: id, Value: v}}})
						cfg.Song = append(cfg.Song, engine.SongEntry{Scene: uint16(len(cfg.Song)), Bars: 1})
					}
					e, err := engine.New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					return e
				}
				history, fresh := newEngine(), newEngine()
				var left, right [128]float32
				history.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
				for frame := 0; frame < moment.frames; frame += len(left) {
					history.Render(left[:], right[:])
				}
				fresh.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
				history.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
				for _, e := range []*engine.Engine{history, fresh} {
					for _, note := range []uint32{48, 60, 64, 67} {
						e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: note | 100<<8})
					}
				}
				var worst, peak float64
				var message cmd.Message
				for done := 0; done < window; done += len(left) {
					var hl, hr, fl, fr [128]float32
					history.Render(hl[:], hr[:])
					fresh.Render(fl[:], fr[:])
					for i := range hl {
						worst = math.Max(worst, math.Max(math.Abs(float64(hl[i]-fl[i])), math.Abs(float64(hr[i]-fr[i]))))
						peak = math.Max(peak, math.Abs(float64(fl[i])))
					}
				}
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
					t.Fatalf("after seeking to the start the voice differs from a fresh one by %g (peak %g)", worst, peak)
				}
			})
		}
	}
}
