//go:build ignore

// Render identical explicit MIDI event scores through the full and starter maps.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
)

type event struct {
	Seconds        float64 `json:"seconds"`
	Bank           string  `json:"bank"`
	Note, Velocity uint8
}
type articulation struct {
	Name, Bank, Kind string
	Note             uint8
}

func main() {
	root := flag.String("root", "build/kit-final", "demo root containing packs and before-packs")
	flag.Parse()
	data, err := os.ReadFile(filepath.Join(*root, "packs", "catalog.json"))
	must(err)
	var catalog struct{ Articulations []articulation }
	must(json.Unmarshal(data, &catalog))
	var sweeps []event
	at := 0.0
	for _, note := range []uint8{36, 38, 41} {
		for _, vel := range []uint8{8, 16, 32, 48, 64, 80, 96, 112, 127} {
			sweeps = append(sweeps, event{at, "shells", note, vel})
			at += .4
		}
		at += .6
	}
	for i := 0; i < 12; i++ {
		sweeps = append(sweeps, event{at, "shells", 38, 80})
		at += .18
	}
	at += 1
	// A 35 ms flam from two separate hit triggers, then a graduated snare roll.
	sweeps = append(sweeps, event{at, "shells", 34, 35}, event{at + .035, "shells", 38, 110})
	at += 1
	for i := 0; i < 16; i++ {
		sweeps = append(sweeps, event{at, "shells", 38, uint8(35 + i*5)})
		at += .06
	}
	at += 1
	for _, note := range []uint8{46, 23, 42, 44} {
		sweeps = append(sweeps, event{at, "metals", note, 100})
		at += .5
	}
	at += .5
	for _, notes := range [][]uint8{{49, 49, 26}, {55, 55, 28}, {52, 52, 30}, {51, 53, 59, 33}} {
		for _, note := range notes {
			sweeps = append(sweeps, event{at, "metals", note, 100})
			at += .4
		}
		at += 1
	}
	var articulations []event
	for i, a := range catalog.Articulations {
		bank := "shells"
		if a.Bank == "full-kit-metals" {
			bank = "metals"
		}
		articulations = append(articulations, event{float64(i) * 2, bank, a.Note, 100})
	}
	for name, events := range map[string][]event{"velocity-rr-flam-choke": sweeps, "articulations": articulations} {
		sort.SliceStable(events, func(i, j int) bool { return events[i].Seconds < events[j].Seconds })
		data, err := json.MarshalIndent(events, "", "  ")
		must(err)
		must(os.WriteFile(filepath.Join(*root, name+".events.json"), append(data, '\n'), 0644))
		for _, before := range []bool{false, true} {
			folder, suffix := "packs", "after"
			if before {
				folder, suffix = "before-packs", "before"
			}
			banks := map[string]*sample.Instrument{}
			for _, bank := range []string{"shells", "metals"} {
				p, err := instrumentpack.Load(*root, folder+"/full-kit-"+bank+"/manifest.json", "")
				must(err)
				p.Manifest.Config.Voices = 16
				v, err := p.New(48000)
				must(err)
				banks[bank] = v
			}
			frames := int(math.Ceil((events[len(events)-1].Seconds + 32) * 48000))
			path := filepath.Join(*root, name+"-"+suffix+".wav")
			f, err := os.Create(path)
			must(err)
			header := make([]byte, 44)
			copy(header, "RIFF")
			binary.LittleEndian.PutUint32(header[4:], uint32(36+frames*6))
			copy(header[8:], "WAVEfmt ")
			binary.LittleEndian.PutUint32(header[16:], 16)
			binary.LittleEndian.PutUint16(header[20:], 1)
			binary.LittleEndian.PutUint16(header[22:], 2)
			binary.LittleEndian.PutUint32(header[24:], 48000)
			binary.LittleEndian.PutUint32(header[28:], 288000)
			binary.LittleEndian.PutUint16(header[32:], 6)
			binary.LittleEndian.PutUint16(header[34:], 24)
			copy(header[36:], "data")
			binary.LittleEndian.PutUint32(header[40:], uint32(frames*6))
			_, err = f.Write(header)
			must(err)
			buffer := make([]byte, 128*6)
			idx := 0
			peak := 0.0
			for block := 0; block < frames; block += 128 {
				count := min(128, frames-block)
				for n := 0; n < count; n++ {
					current := block + n
					for idx < len(events) && int(math.Round(events[idx].Seconds*48000)) == current {
						e := events[idx]
						_, err = banks[e.Bank].NoteOn(e.Note, e.Velocity)
						must(err)
						idx++
					}
					var l, r float32
					for _, bank := range []string{"shells", "metals"} {
						a, b := banks[bank].NextStereo()
						l += a
						r += b
					}
					for ch, x := range []float32{l, r} {
						value := float64(x) * math.Pow(10, -12.0/20)
						if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) >= 1 {
							panic("nonfinite or clipped audition")
						}
						peak = max(peak, math.Abs(value))
						v := int32(math.Round(value * 8388607))
						off := n*6 + ch*3
						buffer[off], buffer[off+1], buffer[off+2] = byte(v), byte(v>>8), byte(v>>16)
					}
				}
				_, err = f.Write(buffer[:count*6])
				must(err)
			}
			if idx != len(events) {
				panic("unscheduled event")
			}
			must(f.Close())
			fmt.Printf("%s frames=%d events=%d peak=%.6f\n", filepath.Base(path), frames, len(events), peak)
		}
	}
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
