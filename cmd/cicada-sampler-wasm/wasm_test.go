//go:build wasm_integration

package main_test

import (
	"context"
	"encoding/binary"
	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/kernel/voice/sample"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSamplerWASMABIParity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "build", "cicada-sampler.wasm"))
	if err != nil {
		t.Fatal("run make build-sampler-wasm first:", err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	m, err := runtime.Instantiate(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		result, err := m.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(name, err)
		}
		if len(result) == 0 {
			return 0
		}
		return result[0]
	}
	call("_initialize")
	if int32(call("sampler_init", 48000, 1)) != 0 {
		t.Fatal("init")
	}
	pcm := make([]float32, 513)
	id := call("sampler_pcm_alloc", uint64(len(pcm)), 1, 48000)
	ptr := uint32(call("sampler_pcm_ptr", id, 0))
	for i := range pcm {
		pcm[i] = float32((i*17)%101-50) / 100
		m.Memory().WriteFloat32Le(ptr+uint32(i*4), pcm[i])
	}
	write := func(ptr uint32, values []float64) {
		for i, x := range values {
			m.Memory().WriteFloat64Le(ptr+uint32(i*8), x)
		}
	}
	write(uint32(call("sampler_config_ptr")), []float64{3, 3, 8, .7, 20, 4, 8, .5, 12, 400, 5000, .2, 0, 0, 0, 0, 4242, 0})
	// Sustain-group membership must survive ABI packing: repeated strikes in
	// block 16 overlap, while key-up and pedal still control this looped fixture.
	write(uint32(call("sampler_zone_ptr", 0)), []float64{float64(id), 60, 48, 72, 1, 127, 64, 0, 0, 1, 0, 1, 0, 0, 513, 1, 31, 501, 101, 517})
	if int32(call("sampler_prepare")) != 0 {
		t.Fatal("prepare")
	}
	c := sample.DefaultInstrumentConfig()
	c.Voices = 3
	c.Amp = sample.Envelope{Attack: 3, Decay: 8, Sustain: .7, Release: 20}
	c.Filter = sample.Envelope{Attack: 4, Decay: 8, Sustain: .5, Release: 12}
	c.Cutoff = 400
	c.FilterDepth = 5000
	c.Gain = .2
	c.Humanize.Seed = 4242
	z := sample.Zone{ChokeGroup: 5, ChokeSustain: true, Region: sample.Region{Left: pcm, SampleRate: 48000, RootKey: 60, End: 513, Loop: true, LoopStart: 31, LoopEnd: 501, Crossfade: 101}, KeyLow: 48, KeyHigh: 72, VelocityLow: 1, VelocityHigh: 127, Layer: 64, Count: 1, Gain: 1}
	native, err := sample.NewInstrument(48000, []sample.Zone{z}, c)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := native.NoteOn(60, 65)
	wid := call("sampler_note_on", 60, 65)
	out := uint32(call("sampler_output_ptr"))
	memory := m.Memory().Size()
	var l, r [128]float32
	for block := 0; block < 32; block++ {
		if block == 4 {
			native.Legato(h, 62, 2)
			if int32(call("sampler_legato", wid, 62, math.Float64bits(2))) != 0 {
				t.Fatal("legato")
			}
		}
		if block == 8 {
			native.Sustain(true)
			native.NoteOff(h)
			call("sampler_sustain", 1)
			call("sampler_note_off", wid)
		}
		if block == 12 {
			native.Sustain(false)
			call("sampler_sustain", 0)
		}
		if block == 16 {
			for i := 0; i < 5; i++ {
				native.NoteOn(uint8(60+i), uint8(64+i))
				call("sampler_note_on", uint64(60+i), uint64(64+i))
			}
		}
		native.Render(l[:], r[:])
		call("sampler_render", 128)
		for ch, want := range [][]float32{l[:], r[:]} {
			bytes, _ := m.Memory().Read(out+uint32(ch*4096*4), 512)
			for i, x := range want {
				got := binary.LittleEndian.Uint32(bytes[i*4:])
				if got != math.Float32bits(x) {
					t.Fatalf("block %d channel %d frame %d native %g WASM %g", block, ch, i, x, math.Float32frombits(got))
				}
			}
		}
	}
	if m.Memory().Size() != memory {
		t.Fatal("callback memory growth")
	}
	t.Log("production sampler ABI: 8192 samples bit-identical; no callback memory growth")
}
