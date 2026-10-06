package ddsp

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

func TestPinnedModelAndHeldoutQuality(t *testing.T) {
	// Prove the fixed model cannot overflow its signed int32 accumulators.
	bound := func(weights []int16, bias int16) {
		abs := func(x int16) int64 {
			n := int64(x)
			if n < 0 {
				return -n
			}
			return n
		}
		total := abs(bias) << 15
		for _, x := range weights {
			total += abs(x) * 32767
		}
		if total > math.MaxInt32 {
			t.Fatalf("model accumulator bound=%d", total)
		}
	}
	for i := range inputWeights {
		bound(inputWeights[i][:], hiddenBias[i])
	}
	for i := range outputWeights {
		bound(outputWeights[i][:], outputBias[i])
	}
	var packed []byte
	put := func(x int16) { packed = binary.LittleEndian.AppendUint16(packed, uint16(x)) }
	for j := 0; j < 2; j++ {
		for i := range inputWeights {
			put(inputWeights[i][j])
		}
	}
	for _, x := range hiddenBias {
		put(x)
	}
	for j := 0; j < 8; j++ {
		for i := range outputWeights {
			put(outputWeights[i][j])
		}
	}
	for _, x := range outputBias {
		put(x)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(packed)); got != ModelSHA256 || len(packed) != 210 {
		t.Fatalf("model pin: hash=%s bytes=%d", got, len(packed))
	}
	f, err := os.Open("testdata/heldout.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var squared, maxError float64
	for _, row := range rows[1:] {
		f0, _ := strconv.ParseUint(row[0], 10, 32)
		l, _ := strconv.ParseUint(row[1], 10, 16)
		got := Predict(uint32(f0), uint16(l))
		for i, sample := range got {
			quantized, err := strconv.ParseInt(row[i+11], 10, 32)
			if err != nil || sample != int32(quantized) {
				t.Fatalf("Python/Go integer inference: got=%d want=%d err=%v", sample, quantized, err)
			}
			want, err := strconv.ParseFloat(row[i+2], 64)
			if err != nil {
				t.Fatal(err)
			}
			d := math.Abs(float64(sample)/32768 - want)
			squared += d * d
			maxError = math.Max(maxError, d)
		}
	}
	mse := squared / float64((len(rows)-1)*9)
	if len(rows) != 144 || mse > 0.00001 || maxError > 0.03 {
		t.Fatalf("heldout: n=%d mse=%g max=%g", len(rows)-1, mse, maxError)
	}
	t.Logf("CC0 model: bytes=%d heldout=%d mse=%g max_error=%g", len(packed), len(rows)-1, mse, maxError)
}

func TestResetSilenceAndAllocationFree(t *testing.T) {
	for _, sr := range []int{44100, 48000, 96000} {
		s, err := New(sr)
		if err != nil {
			t.Fatal(err)
		}
		var want [512]int16
		for i := range want {
			want[i] = s.Next(220000, 26000)
		}
		s.Reset()
		for i, sample := range want {
			if got := s.Next(220000, 26000); got != sample {
				t.Fatalf("reset frame=%d: %d != %d", i, got, sample)
			}
		}
		if got := s.Next(220000, 0); got != 0 {
			t.Fatal("zero loudness")
		}
		if got := s.Next(0, 30000); got != 0 {
			t.Fatal("zero f0")
		}
		if got := s.NextFloat(float32(math.NaN()), 1); got != 0 {
			t.Fatal("NaN pitch")
		}
		if got := s.NextFloat(440, float32(math.NaN())); got != 0 {
			t.Fatal("NaN loudness")
		}
		if alloc := testing.AllocsPerRun(100, func() {
			for range 128 {
				s.NextFloat(440, 0.8)
			}
		}); alloc != 0 {
			t.Fatalf("allocations=%g", alloc)
		}
	}
	if _, err := New(12345); err == nil {
		t.Fatal("sample rate accepted")
	}
	var zero Synth
	if zero.Next(440000, 32767) != 0 {
		t.Fatal("zero-value synth")
	}
}

func TestFrequencyAndNyquistMask(t *testing.T) {
	// Integer oscillator frequency stays within one period at the end of a second.
	s, _ := New(48000)
	var energy float64
	for i := 0; i < 48000; i++ {
		x := float64(s.Next(440000, 26000)) / 32768
		energy += x * x
	}
	if energy/48000 < .005 || energy/48000 > .3 {
		t.Fatalf("rms power=%g", energy/48000)
	}
	if uint32(0)-s.phase > 48000 {
		t.Fatalf("phase drift=%d", uint32(0)-s.phase)
	}
	// Above 0.49*sr only the first partial is admitted; high input cannot wrap.
	s.Reset()
	for i := 0; i < 512; i++ {
		s.Next(^uint32(0), 65535)
		if s.increment != uint32((uint64(48000*490)<<32)/(48000*1000)) {
			t.Fatal("frequency clamp")
		}
	}
}

func BenchmarkNext32Voices(b *testing.B) {
	var voices [32]Synth
	for i := range voices {
		voices[i], _ = New(48000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for frame := 0; frame < 128; frame++ {
			for j := range voices {
				voices[j].Next(110000+uint32(j)*17000, 26000)
			}
		}
	}
}
