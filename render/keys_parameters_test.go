package render

import (
	"strconv"
	"strings"
	"testing"
)

// Offline rendering moves modeled-keyboard controls exactly as playback does,
// for scene settings and for automation lanes.
func TestOfflineKeysSceneParameterParity(t *testing.T) {
	const base = `tempo 120
seed 42
track keys PATCH {}
pattern chords notes { [c4 e4 g4] . . . [d4 f4 a4] . . . }
scene first { keys = chords }
scene second { keys = chords SETTINGS }
scene third { keys = keep }
song { first second*2 third }
AUTOMATION`
	for _, tc := range []struct{ name, patch, settings, automation string }{
		{"ep tremolo", "tine_ep", "keys.tremolo = 0.9 keys.tremolo_rate = 7hz keys.auto_pan = 0.8", ""},
		{"ep gain and drive", "tine_bark", "keys.gain = 1.4 keys.drive = 0.8", ""},
		{"clav", "clav", "keys.gain = 1.5 keys.drive = 0.7", ""},
		{"organ", "organ_full", "keys.gain = 0.5 keys.drive = 0.6 keys.rotary_mix = 0.2 keys.rotary_fast = slow keys.drawbar1 = 2 keys.drawbar9 = 6", ""},
		{"fm", "fm_ep", "keys.gain = 0.4", ""},
		{"analog", "poly_keys", "keys.drive = 0.9 keys.output = -9dB", ""},
		{"strings", "string_machine", "keys.output = -9dB", ""},
		{"ep tremolo lane", "tine_ep", "", "automate keys.tremolo { @1.1.1 0 @3.1.1 1 }\n"},
		{"ep gain lane starting after zero", "tine_ep", "", "automate keys.gain { @2.1.1 0.1 @3.1.1 1.2 }\n"},
		{"strings output lane starting after zero", "string_machine", "", "automate keys.output { @2.1.1 -20db @3.1.1 -3db }\n"},
		{"organ gain lane", "tonewheel_organ", "", "automate keys.gain { @1.1.1 0.8 @3.1.1 0.2 }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := strings.NewReplacer("PATCH", tc.patch, "SETTINGS", tc.settings, "AUTOMATION", tc.automation).Replace(base)
			for _, rate := range []int{44_100, 48_000} {
				t.Run(strconv.Itoa(rate), func(t *testing.T) {
					score, wav, report := renderSceneScore(t, source, Options{SampleRate: rate, Bits: 32})
					assertSceneEngineMatchesWAV(t, score, wav, rate, 0, int(report.Frames))
				})
			}
		})
	}
}
