package render_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestModeledKitNativeOfflineParityAndBlockIndependence(t *testing.T) {
	const source = `cicada 1
tempo 120
seed 99
kit full {
 bd=model.kick sd=model.snare rs=model.rimshot cp=model.cross_stick
 lt=model.tom_low mt=model.tom_mid ht=model.tom_high
 ch=model.hat_closed oh=model.hat_open cb=model.ride_bow cy=model.crash
}
kit details { ch=model.hat_pedal oh=model.hat_half_open cb=model.ride_bell cy=model.splash }
track shells full { level=-18db bd_tune=0.9 sd_position=0.6 cy_pan=-0.2 }
track cymbals details { level=-18db cb_decay=0.8 cy_humanize=0.03 }
pattern beat drums steps=4 {
 bd:X... sd:.x4.. rs:..x. cp:...x
 lt:x... mt:.x.. ht:..x. ch:...x oh:x... cb:.x.. cy:..x.
}
scene main { shells=beat cymbals=beat shells.bd_level=-15db cymbals.cy_pan=0.6 }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil || len(diagnostics) != 0 {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := project.FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("project: %+v", diagnostics)
	}
	for _, rate := range []int{44_100, 48_000} {
		opts := render.Options{SampleRate: rate, Bits: 32, Bars: 1, Block: 128}
		var baseline bytes.Buffer
		report, err := render.WAV(score, opts, &baseline)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range []int{1, 4096} {
			opts.Block = block
			var audio bytes.Buffer
			if _, err := render.WAV(score, opts, &audio); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(baseline.Bytes(), audio.Bytes()) {
				t.Fatalf("%d Hz block %d changed the modeled kit render", rate, block)
			}
		}
		cfg, err := project.CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		native, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("native play command was rejected")
		}
		limiter, err := mix.NewLimiter(rate)
		if err != nil {
			t.Fatal(err)
		}
		latency := limiter.LatencyFrames()
		var left, right [128]float32
		var peakDelta float64
		nonzero := false
		for offset := 0; offset < int(report.Frames)+latency; offset += 128 {
			native.Render(left[:], right[:])
			for j := range left {
				frame := offset + j - latency
				if frame < 0 || frame >= int(report.Frames)-128 {
					continue
				}
				for channel, sample := range [...]float32{left[j], right[j]} {
					at := 44 + (frame*2+channel)*4
					want := math.Float32frombits(binary.LittleEndian.Uint32(baseline.Bytes()[at : at+4]))
					peakDelta = max(peakDelta, math.Abs(float64(sample-want)))
					nonzero = nonzero || sample != 0
				}
			}
			var message cmd.Message
			for native.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatalf("native modeled kit fault: %+v", message)
				}
			}
		}
		if !nonzero || peakDelta > 1e-6 {
			t.Fatalf("%d Hz native/offline delta=%g nonzero=%v", rate, peakDelta, nonzero)
		}
		t.Logf("rate=%d native_offline_peak_delta=%g block_bytes_identical=true", rate, peakDelta)
	}
}

func TestModeledKitMappedHatChokeAndCymbalIndependence(t *testing.T) {
	const source = `cicada 1
tempo 120
seed 37
kit mapped { cp=model.hat_open rs=model.hat_pedal cy=model.ride_bow }
track percussion mapped { level=-12db cp_humanize=0 rs_humanize=0 cy_humanize=0 }
pattern beat drums steps=4 { cp:x... rs:.... cy:.... }
scene main { percussion=beat }
song { main }
`
	renderSource := func(source string) []byte {
		t.Helper()
		score, ds := notation.Parse([]byte(source))
		if score == nil || len(ds) != 0 {
			t.Fatal(ds)
		}
		var audio bytes.Buffer
		if _, err := render.WAV(score, render.Options{SampleRate: 48_000, Bits: 32, Bars: 1}, &audio); err != nil {
			t.Fatal(err)
		}
		return audio.Bytes()
	}
	energy := func(audio []byte) float64 {
		var sum float64
		for frame := 31_200; frame < 45_600; frame++ {
			for channel := 0; channel < 2; channel++ {
				at := 44 + (frame*2+channel)*4
				sample := float64(math.Float32frombits(binary.LittleEndian.Uint32(audio[at : at+4])))
				sum += sample * sample
			}
		}
		return sum
	}
	residual := func(first, second, third []byte) float64 {
		var sum float64
		for frame := 31_200; frame < 45_600; frame++ {
			for channel := 0; channel < 2; channel++ {
				at := 44 + (frame*2+channel)*4
				value := float64(math.Float32frombits(binary.LittleEndian.Uint32(first[at:at+4]))) - float64(math.Float32frombits(binary.LittleEndian.Uint32(second[at:at+4])))
				if third != nil {
					value -= float64(math.Float32frombits(binary.LittleEndian.Uint32(third[at : at+4])))
				}
				sum += value * value
			}
		}
		return sum
	}
	open := energy(renderSource(source))
	closed := renderSource(strings.Replace(source, "rs:....", "rs:.x..", 1))
	pedal := renderSource(strings.Replace(strings.Replace(source, "rs:....", "rs:.x..", 1), "cp:x...", "cp:....", 1))
	remainingOpen := residual(closed, pedal, nil)
	if open <= 0 || remainingOpen >= open*1e-6 {
		t.Fatalf("mapped pedal failed to choke mapped open hat: open=%g predecessor_residual=%g", open, remainingOpen)
	}
	rideSource := strings.Replace(strings.Replace(source, "cp:x...", "cp:....", 1), "cy:....", "cy:x...", 1)
	rideAudio := renderSource(rideSource)
	ride := energy(rideAudio)
	rideWithPedal := renderSource(strings.Replace(rideSource, "rs:....", "rs:.x..", 1))
	rideError := residual(rideWithPedal, pedal, rideAudio)
	if ride <= 0 || rideError >= ride*1e-6 {
		t.Fatalf("pedal altered ringing ride: ride=%g interference_residual=%g", ride, rideError)
	}
	t.Logf("mapped_hat_predecessor_tail_ratio=%g ride_interference_ratio=%g", remainingOpen/open, rideError/ride)
}
