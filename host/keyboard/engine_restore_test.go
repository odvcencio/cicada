package keyboard

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

// A scene can set a control to the very value the track was authored with while
// the voice still glides toward it. Seeking to the start must then put the
// voice at the authored value from the first frame, not leave the glide
// running: the target equals the authored value, so only a change record, not
// a comparison, finds the control.
func TestSeekToStartFinishesAnUnfinishedGlideForEveryFamily(t *testing.T) {
	for _, c := range []struct{ patch, name string }{
		{"tine_tremolo", "tremolo"}, {"tine_ep", "gain"}, {"tine_ep", "drive"}, {"clav", "gain"}, {"tonewheel_organ", "gain"},
		{"organ_full", "rotary_mix"}, {"fm_ep", "gain"}, {"poly_keys", "output"}, {"poly_keys", "drive"}, {"string_machine", "output"},
	} {
		t.Run(c.patch+"/"+c.name, func(t *testing.T) {
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
			far := authored + 0.5
			if descriptor := kernel.Params[id]; far > descriptor.Max {
				far = authored - 0.5
				if far < descriptor.Min {
					far = descriptor.Max
				}
			}
			if c.name == "output" {
				far = authored - 20
			}
			newEngine := func(scenes ...engine.Scene) *engine.Engine {
				s := spec
				cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 8, BPMMilli: 120000}
				cfg.Track[0].Kind, cfg.Track[0].Keys = engine.VoiceKeys, &s
				cfg.Scenes = scenes
				for n := range scenes {
					cfg.Song = append(cfg.Song, engine.SongEntry{Scene: uint16(n), Bars: 1})
				}
				e, err := engine.New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				return e
			}
			set := func(v float32) engine.Scene {
				return engine.Scene{Settings: []engine.SceneSetting{{Track: 0, ID: id, Value: v}}}
			}
			history := newEngine(set(far), set(authored))
			fresh := newEngine(set(far), set(authored))
			var left, right [128]float32
			history.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			// One bar is 96000 frames at 120 BPM: stop a little into the second scene.
			for frame := 0; frame < 96000+1280; frame += len(left) {
				history.Render(left[:], right[:])
			}
			// Seek to the start while the voice still glides toward the authored
			// value, and sound the same chord as a fresh engine's first frames.
			fresh.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
			history.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff})
			var message cmd.Message
			for _, e := range []*engine.Engine{history, fresh} {
				for _, note := range []uint32{48, 60, 64, 67} {
					e.Push(cmd.Command{Op: cmd.OpNoteOn, Track: 0, Arg0: note | 100<<8})
				}
			}
			var worst, peak float64
			for block := 0; block < 8; block++ {
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
				t.Fatalf("after seeking to the start the voice still differs from a fresh one by %g (peak %g)", worst, peak)
			}
		})
	}
}
