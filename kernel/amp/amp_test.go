package amp

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"sort"
	"testing"
	"time"
)

func TestPinnedWeights(t *testing.T) {
	var data []byte
	checkAccumulator := func(row []int16) {
		t.Helper()
		var absoluteSum int32
		for _, x := range row {
			y := int32(x)
			if y < 0 {
				y = -y
			}
			absoluteSum += y
		}
		if absoluteSum >= 32767 {
			t.Fatalf("Q15 accumulator bound exceeded: absolute-weight sum %d", absoluteSum)
		}
	}
	appendWeight := func(x int16) { data = binary.LittleEndian.AppendUint16(data, uint16(x)) }
	for _, row := range convolutionWeights {
		checkAccumulator(row[:])
		for _, x := range row {
			appendWeight(x)
		}
	}
	checkAccumulator(outputWeights[:])
	for _, x := range outputWeights {
		appendWeight(x)
	}
	for _, x := range tanhTable {
		appendWeight(x)
	}
	var model struct {
		SHA256  string  `json:"sha256_le_i16_weights_output_tanh"`
		License string  `json:"license"`
		SNR     float64 `json:"held_out_quantized_snr_db"`
	}
	metadata, err := os.ReadFile("model.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(metadata, &model); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != model.SHA256 || model.License != "CC0-1.0" || model.SNR < 40 {
		t.Fatalf("pinned weights/license/quality metadata mismatch: %+v", model)
	}
}

func TestDeterministicResetMemoryAndBounds(t *testing.T) {
	var first, second Model
	var input [2048]int32
	var state uint32 = 0x12345678
	for i := range input {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		input[i] = int32(state>>16) - 32768
		if got, want := first.Process(input[i], int32(i%9)*4096), second.Process(input[i], int32(i%9)*4096); got != want || got < -32768 || got > 32767 {
			t.Fatalf("frame %d: %d != %d or outside Q15", i, got, want)
		}
	}
	first.Reset()
	second.Reset()
	for _, x := range input {
		if first.Process(x, 4096) != second.Process(x, 4096) {
			t.Fatal("reset changed deterministic output")
		}
	}
	first.Reset()
	if first.Process(0, 4096) != 0 {
		t.Fatal("zero input has bias")
	}
	first.Process(32767, 4096)
	var tail bool
	for i := 0; i < Taps; i++ {
		tail = first.Process(0, 4096) != 0 || tail
	}
	if !tail || first.Process(0, 4096) != 0 {
		t.Fatal("missing causal memory or history did not drain")
	}
	for _, x := range []int32{math.MinInt32, -32768, 0, 32767, math.MaxInt32} {
		for _, drive := range []int32{math.MinInt32, 0, 4096, 32768, math.MaxInt32} {
			y := first.Process(x, drive)
			if y < -32768 || y > 32767 {
				t.Fatalf("unbounded output %d", y)
			}
		}
	}
	for _, x := range []float32{float32(math.NaN()), float32(math.Inf(-1)), float32(math.Inf(1)), -1e30, 1e30} {
		y := first.ProcessFloat(x, x)
		if math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) || y < -1 || y >= 1 {
			t.Fatalf("invalid float bridge %g", y)
		}
	}
}

func TestAmpAllocationFree(t *testing.T) {
	var model Model
	if n := testing.AllocsPerRun(100, func() {
		for i := 0; i < 128; i++ {
			model.ProcessFloat(float32(i-64)/64, 3)
		}
	}); n != 0 {
		t.Fatalf("render allocated %g objects", n)
	}
}

func TestLearnedCausalAmpQuality(t *testing.T) {
	var model Model
	var input [Taps]float64
	var errorPower, referencePower float64
	for i := 0; i < 24000; i++ {
		x := .6*math.Sin(2*math.Pi*177*float64(i)/48000) + .25*math.Sin(2*math.Pi*1771*float64(i)/48000)
		for j := Taps - 1; j > 0; j-- {
			input[j] = input[j-1]
		}
		input[0] = x
		stage := func(j int) float64 { return math.Tanh(2.3*input[j] + .35*input[j+1] - .15*input[j+2]) }
		want := .72 * (.65*stage(0) + .25*stage(1) + .10*stage(3))
		got := float64(model.ProcessFloat(float32(x), 1))
		errorPower += (got - want) * (got - want)
		referencePower += want * want
	}
	snr := 10 * math.Log10(referencePower/errorPower)
	if snr < 40 {
		t.Fatalf("learned reference SNR %.2f dB < 40 dB", snr)
	}
	t.Logf("held-out multitone reference SNR %.2f dB", snr)
}

func TestAmpP99BlockBudget(t *testing.T) {
	const blocks = 4096
	var models [8]Model
	var input [128]int32
	for i := range input {
		input[i] = int32(24000 * math.Sin(2*math.Pi*float64(i)/128))
	}
	process := func() {
		for frame, x := range input {
			for voice := range models {
				models[voice].Process(x+int32(frame), 4096+int32(voice)*1024)
			}
		}
	}
	for i := 0; i < 256; i++ {
		process()
	}
	timings := make([]time.Duration, blocks)
	for i := range timings {
		start := time.Now()
		process()
		timings[i] = time.Since(start)
	}
	sort.Slice(timings, func(i, j int) bool { return timings[i] < timings[j] })
	p99 := timings[(blocks*99)/100]
	t.Logf("8 causal amps, 128 frames at 48 kHz: p50=%s p99=%s; budget=670us; weight bytes=72; LUT bytes=514; state bytes=32/amp", timings[blocks/2], p99)
	if p99 > 670*time.Microsecond {
		t.Fatalf("p99 %s exceeds unchanged 670us block budget", p99)
	}
}

func BenchmarkAmpBlock(b *testing.B) {
	var model Model
	b.ReportAllocs()
	for block := 0; block < b.N; block++ {
		for frame := 0; frame < 128; frame++ {
			model.Process(int32(frame*257)-16384, 8192)
		}
	}
}
