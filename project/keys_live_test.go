package project

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

// renderKeysScore plays a score from its start and returns both channels.
func renderKeysScore(t *testing.T, source string, frames int) (left, right []float32) {
	t.Helper()
	p := keysScore(t, source)
	if err := ValidateProject(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play was not accepted")
	}
	var l, r [128]float32
	var message cmd.Message
	for done := 0; done < frames; done += len(l) {
		e.Render(l[:], r[:])
		left, right = append(left, l[:]...), append(right, r[:]...)
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault %d", message.A)
			}
		}
	}
	return left, right
}

// windowDifference returns the peak difference between two renderings inside
// one frame window.
func windowDifference(a, b []float32, from, to int) float64 {
	var peak float64
	for i := from; i < to && i < len(a); i++ {
		peak = math.Max(peak, math.Abs(float64(a[i]-b[i])))
	}
	return peak
}

// One bar at 120 BPM lasts two seconds, which is 96000 frames at 48 kHz.
const keysLiveFrames = 96000

func keysLiveScore(patch, sceneSettings, extra string) string {
	return fmt.Sprintf("cicada 2\ntempo 120\ntrack keys %s {}\npattern p notes { [c4 e4 g4] . . . [d4 f4 a4] . . . [c4 e4 g4] . . . [e4 g4 b4] . . . }\n%sscene main { keys=p %s }\nsong { main*2 }\n", patch, extra, sceneSettings)
}

// Every scene-settable keyboard control must change the rendered audio, and
// leave it bit-identical to a score without the setting while it is unset.
func TestKeysSceneSettingChangesRenderedAudio(t *testing.T) {
	cases := []struct{ patch, path, value string }{
		{"tine_ep", "tremolo", "0.9"},
		{"tine_tremolo", "tremolo_rate", "9hz"},
		{"tine_tremolo", "auto_pan", "0"},
		{"reed_ep", "gain", "1.8"},
		{"tine_bark", "drive", "0.9"},
		{"clav", "gain", "1.6"},
		{"clav", "drive", "0.9"},
		{"organ_full", "gain", "0.3"},
		{"organ_full", "drive", "0"},
		{"organ_full", "rotary_mix", "0"},
		{"organ_full", "rotary_fast", "slow"},
		{"organ_full", "drawbar1", "0"},
		{"organ_full", "drawbar9", "8"},
		{"fm_ep", "gain", "0.1"},
		{"poly_keys", "drive", "0.9"},
		{"poly_keys", "output", "-12db"},
		{"string_machine", "output", "-12db"},
	}
	for _, c := range cases {
		t.Run(c.patch+"/"+c.path, func(t *testing.T) {
			baseL, baseR := renderKeysScore(t, keysLiveScore(c.patch, "", ""), keysLiveFrames)
			sameL, sameR := renderKeysScore(t, keysLiveScore(c.patch, "", ""), keysLiveFrames)
			if windowDifference(baseL, sameL, 0, len(baseL)) != 0 || windowDifference(baseR, sameR, 0, len(baseR)) != 0 {
				t.Fatal("two renderings of the same score differ")
			}
			setL, setR := renderKeysScore(t, keysLiveScore(c.patch, "keys."+c.path+" = "+c.value, ""), keysLiveFrames)
			if d := math.Max(windowDifference(baseL, setL, 0, len(baseL)), windowDifference(baseR, setR, 0, len(baseR))); d < 1e-4 {
				t.Fatalf("keys.%s = %s changed the audio by only %g", c.path, c.value, d)
			}
		})
	}
}

// An automation lane on a keyboard control moves the audio over time: no
// change at its first point (the value equals the patch value), then a
// growing change as the control moves.
func TestKeysAutomationChangesAudioOverTime(t *testing.T) {
	lane := "automate keys.tremolo { @1.1.1 0 @3.1.1 1 }\n"
	baseL, _ := renderKeysScore(t, keysLiveScore("tine_ep", "", ""), 2*keysLiveFrames)
	autoL, _ := renderKeysScore(t, keysLiveScore("tine_ep", "", lane), 2*keysLiveFrames)
	bar := keysLiveFrames // one bar
	early := windowDifference(baseL, autoL, 0, bar/8)
	late := windowDifference(baseL, autoL, 3*bar/2, 2*bar)
	if late < 1e-3 {
		t.Fatalf("an automated tremolo changed the late audio by only %g", late)
	}
	if early >= late/4 {
		t.Fatalf("tremolo automation changed the audio at its start (%g) nearly as much as at its end (%g)", early, late)
	}
}
