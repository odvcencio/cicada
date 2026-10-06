// Render one recorded layer at its original pitch for the licensed-reference
// comparison. Whole-score musical A/B renders use cicada render separately.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
)

func main() {
	packs := flag.String("packs", "build/pro-packs", "pack root")
	output := flag.String("out", "build/source-reference", "reference render directory")
	flag.Parse()
	if err := os.MkdirAll(*output, 0755); err != nil {
		panic(err)
	}
	for _, name := range []string{"grand", "nylon", "bass", "kit", "violin", "trumpet"} {
		p, err := instrumentpack.Load(*packs, name+"/manifest.json", "")
		if err != nil {
			panic(err)
		}
		chosen := -1
		distance := 1 << 30
		for i, z := range p.Zones {
			if z.Release || z.Position != 0 {
				continue
			}
			target := 60
			if name == "bass" {
				target = 43
			}
			if name == "kit" {
				target = 38
			}
			d := abs(int(z.Region.RootKey)-target)*128 + abs(int(z.Layer)-88)
			if d < distance {
				chosen, distance = i, d
			}
		}
		z := p.Zones[chosen]
		z.KeyLow, z.KeyHigh = z.Region.RootKey, z.Region.RootKey
		z.Group, z.Position, z.Count = 0, 0, 1
		z.Gain = 1
		z.OneShot = false
		config := sample.DefaultInstrumentConfig()
		config.Voices = 1
		config.Gain = 1
		config.Amp.Release = 120
		player, err := sample.NewInstrument(48000, []sample.Zone{z}, config)
		if err != nil {
			panic(err)
		}
		player.NoteOn(z.Region.RootKey, z.Layer)
		frames := min(z.Region.End, 48000*4)
		if z.Region.Loop {
			frames = min(frames, z.Region.LoopEnd-z.Region.Crossfade)
		}
		f, err := os.Create(filepath.Join(*output, name+".f32"))
		if err != nil {
			panic(err)
		}
		var bytes [8]byte
		for i := 0; i < frames; i++ {
			l, r := player.NextStereo()
			binary.LittleEndian.PutUint32(bytes[:], math.Float32bits(l))
			binary.LittleEndian.PutUint32(bytes[4:], math.Float32bits(r))
			if _, err = f.Write(bytes[:]); err != nil {
				panic(err)
			}
		}
		f.Close()
		fmt.Printf("%s asset=%s frames=%d velocity=%d root=%d\n", name, p.Manifest.Zones[chosen].Asset, frames, z.Layer, z.Region.RootKey)
	}
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
