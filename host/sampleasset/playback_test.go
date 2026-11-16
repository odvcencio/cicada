package sampleasset

import (
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The rule that check applies must be the voice's own: for every source rate
// at the edges and in common use, every root a sampler may declare, every
// note and every output rate, Playable agrees with what NoteOn does.
func TestPlayableAgreesWithTheSampleVoice(t *testing.T) {
	sourceRates := []int{MinSourceRate, 11_025, 16_000, 22_050, 32_000, 44_100, 48_000, 88_200, 96_000, 176_400, MaxSourceRate}
	pcm := make([]float32, 64)
	for _, sourceRate := range sourceRates {
		for _, outputRate := range OutputRates {
			for root := 12; root <= 95; root++ {
				voice, err := sample.New(outputRate, sample.Region{Left: pcm, SampleRate: sourceRate, RootKey: uint8(root), End: len(pcm)})
				if err != nil {
					t.Fatal(err)
				}
				for note := 0; note <= 127; note++ {
					played := voice.NoteOn(uint8(note), 100) == nil
					if got := Playable(sourceRate, outputRate, root, note); got != played {
						t.Fatalf("source %d Hz, output %d Hz, root %d, note %d: Playable says %v, NoteOn says %v (ratio %v)", sourceRate, outputRate, root, note, got, played, PlaybackRatio(sourceRate, outputRate, root, note))
					}
				}
			}
		}
	}
}

func TestPlayableRangeAtEveryOutputRate(t *testing.T) {
	for _, tc := range []struct {
		sourceRate, root int
		low, high        int
	}{
		// A 48 kHz source plays 24 semitones down (ratio 1/8 at 96 kHz) and
		// 34 up (48000/44100 * 2^(34/12) is 7.7; 35 up is 8.2).
		{48_000, 48, 24, 82},
		{44_100, 60, 38, 96},
		// An 8 kHz source plays at 1/12 of 96 kHz unpitched: it needs at
		// least 8 semitones up (ratio 0.132), and 65 up is 7.4 at 44.1 kHz.
		{8_000, 60, 68, 125},
	} {
		low, high, ok := PlayableRange(tc.sourceRate, tc.root, OutputRates)
		if !ok || low != tc.low || high != tc.high {
			t.Errorf("source %d Hz, root %d: got %d to %d (ok %v), want %d to %d", tc.sourceRate, tc.root, low, high, ok, tc.low, tc.high)
		}
	}
}

func rangeProject(t *testing.T, text string) *project.Project {
	t.Helper()
	score, ds := notation.Parse([]byte(text))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("parse/compile: %+v %+v", ds, more)
	}
	return p
}

const rangeScore = `cicada 2
tempo 120
asset vocal "audio/vocal.wav" { sha256 = "555a3d5cddfe5a85dfe01fb88b27b12b10ef7445dd23554852e3a2be2b7e0508" format = wav frames = 4800 rate = 48000Hz channels = 1 }
sampler hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops hit {}
track lead acid {}
pattern hits notes { a2 . b6'' . [c3 c4'''] . }
pattern low { c3 . }
pattern line { c1 . c4''' . }
scene verse { chops = hits lead = line }
scene drop { chops = low }
song { verse drop }
`

func TestSamplerRangeFindingsNameEachNoteTheVoiceRefuses(t *testing.T) {
	p := rangeProject(t, rangeScore)
	got := SamplerRangeFindings(p, OutputRates)
	base := RangeFinding{Track: "chops", Pattern: "hits", Sampler: "hit", Asset: "vocal", AssetRate: 48_000, Root: 48, Low: 24, High: 82}
	b8 := base
	b8.Step, b8.Note = 2, 119
	b8.Rates = []int{44_100, 48_000, 96_000}
	b8.Ratios = []float64{PlaybackRatio(48_000, 44_100, 48, 119), PlaybackRatio(48_000, 48_000, 48, 119), PlaybackRatio(48_000, 96_000, 48, 119)}
	c7 := base
	c7.Step, c7.Chord, c7.Note = 4, 1, 96
	// 48 semitones up is ratio 8 at 96 kHz, the largest the voice plays.
	c7.Rates = []int{44_100, 48_000}
	c7.Ratios = []float64{PlaybackRatio(48_000, 44_100, 48, 96), PlaybackRatio(48_000, 48_000, 48, 96)}
	want := []RangeFinding{b8, c7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings\n got %+v\nwant %+v", got, want)
	}
	// At 48 kHz alone, the same two notes, with one rate each.
	if at48 := SamplerRangeFindings(p, []int{48_000}); len(at48) != 2 || at48[0].Low != 12 || at48[0].High != 84 {
		t.Fatalf("at 48 kHz: %+v", at48)
	}
}

// A note 25 semitones below the root plays at 44.1 and 48 kHz and not at
// 96 kHz: the finding names that rate alone.
func TestSamplerRangeFindingNamesOnlyTheRatesThatFail(t *testing.T) {
	p := rangeProject(t, `cicada 2
tempo 120
asset vocal "audio/vocal.wav" { sha256 = "555a3d5cddfe5a85dfe01fb88b27b12b10ef7445dd23554852e3a2be2b7e0508" format = wav frames = 4800 rate = 48000Hz channels = 1 }
sampler hit { asset = vocal root = c4 mode = oneshot voices = 8 }
track chops hit {}
pattern hits { b1 . c2 . }
scene verse { chops = hits }
song { verse }
`)
	got := SamplerRangeFindings(p, OutputRates)
	if len(got) != 1 || got[0].Note != 35 || !reflect.DeepEqual(got[0].Rates, []int{96_000}) {
		t.Fatalf("findings %+v", got)
	}
}

func TestAssetRateFindingsNameWhatCannotReadItsAsset(t *testing.T) {
	p := rangeProject(t, `cicada 2
tempo 120
asset hi "audio/hi.wav" { sha256 = "555a3d5cddfe5a85dfe01fb88b27b12b10ef7445dd23554852e3a2be2b7e0508" format = wav frames = 9600 rate = 384000Hz channels = 1 }
asset lo "audio/lo.wav" { sha256 = "555a3d5cddfe5a85dfe01fb88b27b12b10ef7445dd23554852e3a2be2b7e0508" format = wav frames = 8000 rate = 8000Hz channels = 1 }
asset ok "audio/ok.wav" { sha256 = "555a3d5cddfe5a85dfe01fb88b27b12b10ef7445dd23554852e3a2be2b7e0508" format = wav frames = 4800 rate = 48000Hz channels = 1 }
sampler high { asset = hi root = c4 mode = oneshot voices = 2 }
sampler low { asset = lo root = c4 mode = oneshot voices = 2 }
sampler unused { asset = hi root = c4 mode = oneshot voices = 2 }
clip far hi { start = 0frames end = 9600frames }
clip phone lo { start = 0frames end = 8000frames }
clip fine ok { start = 0frames end = 4800frames }
clip idle hi { start = 0frames end = 9600frames }
track a high {}
track b low {}
track v audio {}
pattern p { c4 . }
scene one { a = p b = p v = far }
scene two { v = phone }
scene three { v = fine }
song { one two three }
`)
	got := AssetRateFindings(p, OutputRates)
	want := []AssetFinding{
		// A sampler reads 8 to 192 kHz; low's 8 kHz asset is in, and its
		// notes are SamplerRangeFindings' to check.
		{Sampler: "high", Asset: "hi", AssetRate: 384_000, Low: 8_000, High: 192_000, Rates: OutputRates},
		{Clip: "far", Asset: "hi", AssetRate: 384_000, Low: 12_000, High: 192_000, Rates: OutputRates},
		// 8 kHz is ratio 1/12 at 96 kHz.
		{Clip: "phone", Asset: "lo", AssetRate: 8_000, Low: 12_000, High: 192_000, Rates: []int{96_000}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings\n got %+v\nwant %+v", got, want)
	}
}
