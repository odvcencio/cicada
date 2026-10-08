package notation

import (
	"strings"
	"testing"
)

// sceneSettingScore puts one scene setting on line 7, after a delay effect that
// gives the delay.* paths an owner.
func sceneSettingScore(setting string) []byte {
	return []byte("cicada 1\nfx delay { feedback = 0.2 }\ntrack bass acid {}\npattern riff acid steps=1 { 1 }\nscene main {\n  bass = riff\n  " + setting + "\n}\nsong { main }\n")
}

// A scene value that the registry rejects must say what the setting accepts.
// The message used to be the text of the Go strconv error behind the check:
// "invalid syntax" for a wrong unit, "value out of range" for a bad number, and
// a "strconv.ParseFloat: parsing ..." line for a word.
func TestSceneSettingErrorsSayWhatIsExpected(t *testing.T) {
	for _, tc := range []struct {
		name, setting string
		want          []string
	}{
		{"number without a unit", "bass.cutoff = 5", []string{"invalid value for bass.cutoff", "expected a frequency in Hz or kHz", "such as 600Hz", "got 5"}},
		{"wrong unit", "bass.cutoff = 3dB", []string{"expected a frequency in Hz or kHz", "got 3dB"}},
		{"word instead of a number", "bass.cutoff = banana", []string{"expected a frequency in Hz or kHz", "got banana"}},
		{"above the range", "bass.cutoff = 9000Hz", []string{"9000Hz is outside the range 20Hz to 8000Hz"}},
		{"below the range in kHz", "bass.cutoff = 0.01kHz", []string{"0.01kHz is outside the range 20Hz to 8000Hz"}},
		{"time with a plain number", "delay.time = 7", []string{"expected a time in ms or s", "such as 250ms", "or one of 1/32, 1/16", "1/2", "got 7"}},
		{"time above the range", "delay.time = 3s", []string{"3s is outside the range 1ms to 2000ms"}},
		{"toggle with another word", "delay.pingpong = yes", []string{"expected on or off", "got yes"}},
		{"toggle with another number", "delay.pingpong = 2", []string{"expected on or off", "got 2"}},
		{"level without a unit", "bass.level = 5", []string{"expected a level in dB", "got 5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diagnostics := Parse(sceneSettingScore(tc.setting))
			if len(diagnostics) != 1 {
				t.Fatalf("want one diagnostic, got %+v", diagnostics)
			}
			d := diagnostics[0]
			if d.Code != "CICADA-UNIT" || d.Severity != "error" || d.Position.Line != 7 {
				t.Fatalf("want a CICADA-UNIT error on line 7, got %+v", d)
			}
			for _, want := range tc.want {
				if !strings.Contains(d.Message, want) {
					t.Errorf("message %q does not contain %q", d.Message, want)
				}
			}
			for _, leak := range []string{"invalid syntax", "value out of range", "strconv", "ParseFloat"} {
				if strings.Contains(d.Message, leak) {
					t.Errorf("message %q leaks the Go parse error %q", d.Message, leak)
				}
			}
		})
	}
}

// Rewording the errors must not change which values the registry accepts.
func TestSceneSettingsStillAcceptRegistryValues(t *testing.T) {
	for _, setting := range []string{
		"bass.cutoff = 900Hz",
		"bass.cutoff = 0.9kHz",
		"bass.cutoff = 20Hz",
		"bass.cutoff = 8000Hz",
		"bass.decay = 0.4s",
		"bass.level = -6dB",
		"delay.pingpong = on",
		"delay.pingpong = off",
		"delay.pingpong = true",
		"delay.pingpong = 1",
		"delay.time = 1/4",
		"delay.time = 300ms",
		"delay.time = 1s",
	} {
		t.Run(setting, func(t *testing.T) {
			if _, diagnostics := Parse(sceneSettingScore(setting)); len(diagnostics) != 0 {
				t.Fatalf("registry value was rejected: %+v", diagnostics)
			}
		})
	}
}
