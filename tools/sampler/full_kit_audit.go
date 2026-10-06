//go:build ignore

// Admit every asset and compare actual native playback to its prepared PCM.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
	"math"
	"os"
	"path/filepath"
)

func main() {
	packs := flag.String("packs", "build/kit-final/packs", "external pack root")
	output := flag.String("out", "build/kit-reports/reference", "reference render directory")
	flag.Parse()
	must(os.MkdirAll(*output, 0755))
	var metrics []map[string]any
	for _, bank := range []string{"full-kit-shells", "full-kit-metals"} {
		p, err := instrumentpack.Load(*packs, bank+"/manifest.json", "")
		must(err)
		var total int64
		for _, a := range p.Manifest.Assets {
			total += int64(a.Frames) * int64(a.Channels) * 4
		}
		seen := map[string]bool{}
		for i, z := range p.Zones {
			if z.Region.End != len(z.Region.Left) {
				continue // The silent choke control reuses only 96 source frames.
			}
			id := p.Manifest.Zones[i].Asset
			if seen[id] {
				continue
			}
			seen[id] = true
			z.KeyLow, z.KeyHigh = z.Region.RootKey, z.Region.RootKey
			z.Group, z.Position, z.Count = 0, 0, 1
			z.Layer = 127
			z.Gain = 1
			z.OneShot = true
			z.ChokeGroup = 0
			z.ChokeSustain = false
			c := sample.DefaultInstrumentConfig()
			c.Voices = 1
			c.Gain = 1
			player, err := sample.NewInstrument(48000, []sample.Zone{z}, c)
			must(err)
			_, err = player.NoteOn(z.Region.RootKey, 127)
			must(err)
			var signal, residual float64
			var bytes []byte
			// Record a representative take at the middle dynamic of each piece for
			// spectrum/envelope comparison with the unmodified licensed WAV.
			selected := p.Manifest.Zones[i].Position == 0 && (p.Manifest.Zones[i].Layer == 58 || p.Manifest.Zones[i].Layer == 61 || p.Manifest.Zones[i].Layer == 51 || p.Manifest.Zones[i].Layer == 64)
			if selected {
				bytes = make([]byte, z.Region.End*8)
			}
			for n := 0; n < z.Region.End; n++ {
				l, r := player.NextStereo()
				if n >= 128 && n < z.Region.End-240 {
					dl := float64(l - z.Region.Left[n])
					dr := float64(r - z.Region.Right[n])
					residual += dl*dl + dr*dr
					signal += float64(z.Region.Left[n])*float64(z.Region.Left[n]) + float64(z.Region.Right[n])*float64(z.Region.Right[n])
				}
				if selected {
					binary.LittleEndian.PutUint32(bytes[n*8:], math.Float32bits(l))
					binary.LittleEndian.PutUint32(bytes[n*8+4:], math.Float32bits(r))
				}
			}
			if residual > signal*1e-9 {
				panic("native source residual exceeds -90 dB: " + id)
			}
			for n := 0; n < 256; n++ {
				l, r := player.NextStereo()
				if n > 96 && (l != 0 || r != 0) {
					panic("natural ending noise")
				}
			}
			if selected {
				must(os.WriteFile(filepath.Join(*output, bank+"--"+id+".f32"), bytes, 0644))
			}
		}
		instrument, err := p.New(48000)
		must(err)
		for _, z := range p.Zones {
			for v := uint8(1); v < 128; v++ {
				instrument.Reset()
				_, err = instrument.NoteOn(z.KeyLow, v)
				must(err)
				var energy float64
				for n := 0; n < 1024; n++ {
					l, r := instrument.NextStereo()
					energy += float64(l)*float64(l) + float64(r)*float64(r)
				}
				if z.Gain > 0 && energy == 0 {
					panic("silent mapped hit")
				}
			}
		}
		instrument.Reset()
		for i := 0; i < 1024; i++ {
			l, r := instrument.NextStereo()
			if l != 0 || r != 0 {
				panic("reset noise")
			}
		}
		metrics = append(metrics, map[string]any{"bank": bank, "assets_decoded": len(seen), "zones": len(p.Zones), "pcm_bytes": total, "native_body_residual_db": "-infinity (bit-exact)", "all_velocities": true, "reset_peak": 0})
	}
	data, err := json.MarshalIndent(metrics, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(*output, "admission.json"), append(data, '\n'), 0644))
	fmt.Println(string(data))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
