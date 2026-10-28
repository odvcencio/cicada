//go:build wasm_integration

package main

import "testing"

func TestAudioWASMModeledPercussionParity(t *testing.T) {
	for _, name := range []string{"steelpan", "marimba", "vibraphone", "conga", "shaker", "wood", "zinc", "felt", "marble"} {
		t.Run(name, func(t *testing.T) { compareWASMFixture(t, "modeled/"+name+".cicada", 1) })
	}
}

// The pedal reaches the kernel three ways: the track's own setting (a marked
// profile byte and a value in the image), a scene setting and an automation
// lane. Each score keeps bars ringing past their gates, so a kernel that
// dropped the pedal would differ from the native engine.
func TestAudioWASMModalPedalParity(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		bars         int
	}{
		{"track", `cicada 2
tempo 92
track vibes model_vibraphone { level = -9dB sustain = on }
pattern arpeggio notes { gate = 50% step = 1/8 c4 e4 g4 b4 c5 b4 g4 e4 }
arrange { place a vibes arpeggio { at = @1.1.1 length = 2bars } }
`, 2},
		{"scene", `cicada 2
tempo 92
track vibes model_vibraphone { level = -9dB }
pattern line notes { gate = 100% step = 1/8 c4 - - - g4 - - - }
scene dry { vibes = line vibes.sustain = 0 }
scene lush { vibes = line vibes.sustain = 1 }
song { dry lush dry }
`, 3},
		{"automation", `cicada 2
tempo 92
track vibes model_vibraphone { level = -9dB }
pattern line notes { gate = 100% step = 1/8 c4 - - - g4 - - - | e4 - - - c4 - - - }
automate vibes.sustain {
  @1.1.1 1
  @1.3.1 0 step
  @1.3.2 1 step
  @2.1.1 0 step
  @2.1.2 1 step
  @2.3.1 0 step
  @2.3.2 1 step
}
arrange { place a vibes line { at = @1.1.1 length = 2bars } }
`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compareWASMSource(t, "modal-pedal-"+tc.name+".cicada", []byte(tc.source), tc.bars, false)
		})
	}
}
