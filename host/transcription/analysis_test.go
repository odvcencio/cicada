package transcription

import (
	"errors"
	"math"
	"testing"

	"m31labs.dev/cicada/host/recording"
)

func pitchedFixture(rate int, midi, vibrato float64, seconds float64) []float32 {
	pcm := make([]float32, int(seconds*float64(rate)))
	var phase float64
	for i := range pcm {
		time := float64(i) / float64(rate)
		pitch := midi + vibrato*math.Cos(2*math.Pi*5*time)
		phase += 2 * math.Pi * 440 * math.Exp2((pitch-69)/12) / float64(rate)
		gain := min(1.0, min(time/.008, (seconds-time)/.008))
		pcm[i] = float32(gain * .2 * (math.Sin(phase) + .2*math.Sin(2*phase)) / 1.2)
	}
	return pcm
}

func TestPitchRangeAndSampleRates(t *testing.T) {
	for _, rate := range []int{8000, 16000, 44100, 96000} {
		for _, midi := range []float64{33, 36, 48, 60, 72, 84, 96} {
			result, err := Analyze(pitchedFixture(rate, midi, 0, .3), rate, DefaultOptions())
			if err != nil {
				t.Errorf("rate=%d MIDI=%.0f: %v", rate, midi, err)
				continue
			}
			if len(result.Notes) != 1 {
				t.Errorf("rate=%d MIDI=%.0f: got %d notes", rate, midi, len(result.Notes))
				continue
			}
			if cents := math.Abs(result.Notes[0].MIDI-midi) * 100; cents > 15 {
				t.Errorf("rate=%d MIDI=%.0f: pitch error %.1f cents", rate, midi, cents)
			}
			if midi == 33 || midi == 96 {
				t.Logf("rate=%d MIDI=%.0f: measured=%.5f error=%.2f cents", rate, midi, result.Notes[0].MIDI, math.Abs(result.Notes[0].MIDI-midi)*100)
			}
		}
	}
}

func TestDetuningVibratoAndPitchTrace(t *testing.T) {
	result, err := Analyze(pitchedFixture(44100, 60.17, .5, 1.3), 44100, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 1 {
		t.Fatalf("vibrato split into %d notes", len(result.Notes))
	}
	note := result.Notes[0]
	if math.Abs(note.MIDI-60.17) > .15 || math.Abs(note.Cents-100*(note.MIDI-math.Round(note.MIDI))) > 1e-8 {
		t.Errorf("detuning not preserved: MIDI=%v cents=%v", note.MIDI, note.Cents)
	}
	if len(note.Pitch) == 0 || len(note.Pitch) > 64 {
		t.Fatalf("pitch trace contains %d points", len(note.Pitch))
	}
	low, high := math.Inf(1), math.Inf(-1)
	for i, point := range note.Pitch {
		if point.Time < note.Start || point.Time > note.End || i > 0 && point.Time <= note.Pitch[i-1].Time {
			t.Errorf("pitch trace has invalid time: %+v", point)
		}
		low, high = min(low, point.Cents), max(high, point.Cents)
	}
	if high-low < 70 {
		t.Errorf("pitch movement lost: range %.1f cents", high-low)
	}
}

func TestRepeatedDecayAttacks(t *testing.T) {
	const rate = 16000
	pcm := make([]float32, rate)
	for i := range pcm {
		time := float64(i) / rate
		var gain float64
		for _, onset := range []float64{.05, .35, .65} {
			if time >= onset {
				age := time - onset
				gain += min(1.0, age/.004) * math.Exp(-age/.09)
			}
		}
		pcm[i] = float32(.2 * gain * math.Sin(2*math.Pi*220*time))
	}
	result, err := Analyze(pcm, rate, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 3 {
		t.Fatalf("got %d notes for three repeated attacks: %+v", len(result.Notes), result.Notes)
	}
	for i, onset := range []float64{.05, .35, .65} {
		if math.Abs(result.Notes[i].Start-onset) > .025 {
			t.Errorf("attack %d onset %.3f, want %.3f", i, result.Notes[i].Start, onset)
		}
	}
}

func TestLegatoSemitoneBoundary(t *testing.T) {
	const rate = 16000
	pcm := make([]float32, rate)
	var phase float64
	for i := range pcm {
		midi := 60.0
		if i >= rate/2 {
			midi = 61
		}
		phase += 2 * math.Pi * 440 * math.Exp2((midi-69)/12) / rate
		pcm[i] = float32(.2 * math.Sin(phase))
	}
	result, err := Analyze(pcm, rate, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 2 {
		t.Fatalf("got %d notes for legato semitone", len(result.Notes))
	}
	if math.Abs(result.Notes[1].Start-.5) > .03 {
		t.Errorf("legato onset %.3f, want .5", result.Notes[1].Start)
	}
}

func TestRejectInvalidAudio(t *testing.T) {
	for _, fixture := range []struct {
		name string
		pcm  []float32
		rate int
	}{
		{"empty", nil, 16000},
		{"low-rate", []float32{.1}, 7999},
		{"high-rate", []float32{.1}, 96001},
		{"nan", []float32{float32(math.NaN())}, 16000},
		{"infinite", []float32{float32(math.Inf(1))}, 16000},
		{"over-range", []float32{1.1}, 16000},
		{"clipped", []float32{1, -1, 1, -1}, 16000},
		{"silence", make([]float32, 1600), 16000},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			_, err := Analyze(fixture.pcm, fixture.rate, DefaultOptions())
			if err == nil {
				t.Fatal("invalid recording accepted")
			}
			if fixture.name == "silence" && !errors.Is(err, ErrNoNotes) {
				t.Errorf("silence error %v, want ErrNoNotes", err)
			}
		})
	}
}

func TestDecodeWAVKeepsSourceAndRawOnset(t *testing.T) {
	pcm := append(make([]float32, 1600), pitchedFixture(16000, 64.12, 0, .3)...)
	wav := recording.EncodeWAV(pcm, 16000)
	result, err := DecodeWAV(wav, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.InputSHA256 != recording.Digest(wav) || result.Source != "" || len(result.Notes) != 1 {
		t.Fatalf("invalid source or notes: %+v", result)
	}
	if math.Abs(result.Notes[0].Start-.1) > .015 {
		t.Errorf("raw onset %.3f, want .1", result.Notes[0].Start)
	}
}

func BenchmarkAnalyzeTenSeconds(b *testing.B) {
	pcm := pitchedFixture(16000, 60.17, .3, 10)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Analyze(pcm, 16000, DefaultOptions()); err != nil {
			b.Fatal(err)
		}
	}
}
