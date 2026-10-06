//go:build wasm_integration

package recording

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"m31labs.dev/cicada/host/instrumentpack"
)

func TestRecordedPackNativeWASMParity(t *testing.T) {
	hits, err := Analyze([]Audio{fixture(t)}, Options{Root: 60, Layers: 3})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Build("recorded", hits, 3)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if err := p.Write(dir); err != nil {
		t.Fatal(err)
	}
	prepared, err := instrumentpack.Load(dir, "manifest.json", p.Pin)
	if err != nil {
		t.Fatal(err)
	}
	native, err := prepared.New(48000)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile("../../build/cicada-sampler.wasm")
	if err != nil {
		t.Fatal("run make build-sampler-wasm first:", err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	m, err := runtime.Instantiate(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args ...uint64) uint64 {
		t.Helper()
		values, err := m.ExportedFunction(name).Call(ctx, args...)
		if err != nil {
			t.Fatal(name, err)
		}
		if len(values) > 0 {
			return values[0]
		}
		return 0
	}
	write := func(pointer uint32, values []float64) {
		for i, x := range values {
			if !m.Memory().WriteFloat64Le(pointer+uint32(i*8), x) {
				t.Fatal("WASM upload bounds")
			}
		}
	}
	call("_initialize")
	if int32(call("sampler_init", 48000, uint64(len(p.Manifest.Zones)))) != 0 {
		t.Fatal("init")
	}
	c := p.Manifest.Config
	a, f, h := c.Amp, c.Filter, c.Humanize
	write(uint32(call("sampler_config_ptr")), []float64{float64(c.Voices), a.Attack, a.Decay, a.Sustain, a.Release, f.Attack, f.Decay, f.Sustain, f.Release, c.Cutoff, c.FilterDepth, c.Gain, c.TuneCents, h.DelayMS, h.Velocity, h.Cents, float64(uint32(h.Seed)), float64(uint32(h.Seed >> 32))})
	assets := map[string]uint64{}
	for i, asset := range p.Manifest.Assets {
		id := call("sampler_pcm_alloc", uint64(asset.Frames), 1, uint64(asset.Rate))
		if int32(id) < 0 {
			t.Fatal("PCM allocation")
		}
		assets[asset.ID] = id
		pointer := uint32(call("sampler_pcm_ptr", id, 0))
		for frame, x := range hits[i].PCM {
			if !m.Memory().WriteFloat32Le(pointer+uint32(frame*4), x) {
				t.Fatal("PCM upload bounds")
			}
		}
	}
	for i, z := range p.Manifest.Zones {
		flags := z.ChokeGroup
		if z.OneShot {
			flags += 256
		}
		write(uint32(call("sampler_zone_ptr", uint64(i))), []float64{float64(assets[z.Asset]), float64(z.Root), float64(z.KeyLow), float64(z.KeyHigh), float64(z.VelocityLow), float64(z.VelocityHigh), float64(z.Layer), float64(z.Group), float64(z.Position), float64(z.Count), 0, z.Gain, z.TuneCents, float64(z.Start), float64(z.End), 0, 0, 0, 0, float64(flags)})
	}
	if int32(call("sampler_prepare")) != 0 {
		t.Fatal("prepare")
	}
	memory := m.Memory().Size()
	out := uint32(call("sampler_output_ptr"))
	var left, right [128]float32
	for block := 0; block < 256; block++ {
		if block%16 == 0 {
			note, velocity := uint8(60+12*(block/64%2)), uint8([]int{25, 64, 120}[block/16%3])
			if _, err := native.NoteOn(note, velocity); err != nil {
				t.Fatal(err)
			}
			if call("sampler_note_on", uint64(note), uint64(velocity)) == 0 {
				t.Fatal("note")
			}
		}
		native.Render(left[:], right[:])
		call("sampler_render", 128)
		for channel, want := range [][]float32{left[:], right[:]} {
			for i, x := range want {
				bits, ok := m.Memory().ReadUint32Le(out + uint32(channel*4096*4+i*4))
				if !ok || bits != math.Float32bits(x) {
					t.Fatalf("block %d channel %d frame %d: native %g WASM %g", block, channel, i, x, math.Float32frombits(bits))
				}
			}
		}
	}
	if m.Memory().Size() != memory {
		t.Fatal("recorded sampler render grew WASM memory")
	}
	t.Log("15 recorded zones, three dynamics, round robins and octave transposition: 65,536 channel samples bit-identical; no render memory growth")
}
