// Render deterministic research auditions, without sample or IR dependencies.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"m31labs.dev/cicada/experimental/expressive"
	"math"
	"os"
	"path/filepath"
	"time"
)

const sr = 48000

func hz(n int) float64 { return 440 * math.Exp2(float64(n-69)/12) }

type event struct {
	at, dur float64
	note    int
	vel     float64
}
type metric struct {
	Peak, RMS, DC float64
	Seconds       float64
	RenderMS      int64
}

func render(kind string, events []event, duration float64, drive float64) []float64 {
	var v expressive.Voice
	switch kind {
	case "bow":
		v = expressive.NewBow(sr)
	case "brass":
		v = expressive.NewBrass(sr)
	default:
		v = expressive.NewGuitar(sr)
	}
	out := make([]float64, int(duration*sr))
	idx := -1
	off := true
	for i := range out {
		t := float64(i) / sr
		if idx+1 < len(events) && t >= events[idx+1].at {
			idx++
			v.SetExpression(expressive.Expression{PitchHz: hz(events[idx].note), Pressure: .65, Position: .2, Brightness: .6, Drive: drive})
			v.NoteOn(hz(events[idx].note), events[idx].vel)
			off = false
		}
		if idx >= 0 {
			e := events[idx]
			age := t - e.at
			if !off && age >= e.dur {
				v.NoteOff()
				off = true
			}
			if i%64 == 0 {
				bend := 0.
				if kind == "guitar" && idx%3 == 2 {
					bend = 2 * math.Min(1, age/.3)
				}
				pressure := .4 + .35*math.Min(1, age/.25)
				damp := 0.
				if kind == "guitar" && idx%4 == 3 {
					damp = .75
				}
				v.SetExpression(expressive.Expression{PitchHz: hz(e.note) * math.Exp2(bend/12), Pressure: pressure, Position: .17 + .04*math.Sin(t*2), Brightness: .55 + .2*math.Min(1, age/.3), Vibrato: 18 * math.Min(1, math.Max(0, age-.2)), Drive: drive, Damping: damp})
			}
		}
		gain := .35
		if kind == "brass" {
			gain = 2
		}
		out[i] = gain * v.Next() // fixed authored gains; no normalization or limiting
	}
	return out
}
func wav(path string, l, r []float64) error {
	f, e := os.Create(path)
	if e != nil {
		return e
	}
	defer f.Close()
	n := len(l) * 4
	f.WriteString("RIFF")
	binary.Write(f, binary.LittleEndian, uint32(n+36))
	f.WriteString("WAVEfmt ")
	for _, x := range []any{uint32(16), uint16(1), uint16(2), uint32(sr), uint32(sr * 4), uint16(4), uint16(16)} {
		binary.Write(f, binary.LittleEndian, x)
	}
	f.WriteString("data")
	binary.Write(f, binary.LittleEndian, uint32(n))
	for i, x := range l {
		for _, v := range []float64{x, r[i]} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1 {
				return fmt.Errorf("invalid output sample %d: %g", i, v)
			}
			binary.Write(f, binary.LittleEndian, int16(math.Round(v*32767)))
		}
	}
	return nil
}
func stats(x []float64) metric {
	m := metric{Seconds: float64(len(x)) / sr}
	for _, v := range x {
		m.Peak = math.Max(m.Peak, math.Abs(v))
		m.RMS += v * v
		m.DC += v
	}
	m.RMS = math.Sqrt(m.RMS / float64(len(x)))
	m.DC /= float64(len(x))
	return m
}
func main() {
	dir := flag.String("out", "/tmp/cicada-expressive-demos", "output directory")
	flag.Parse()
	os.MkdirAll(*dir, 0755)
	events := []event{{0, 1.2, 57, .75}, {1.55, .35, 60, .6}, {2.15, 1.3, 64, .85}, {3.85, .45, 62, .7}, {4.65, 1.6, 57, .8}}
	metrics := map[string]metric{}
	for _, spec := range []struct {
		name, kind string
		drive      float64
	}{{"bow-dry", "bow", 0}, {"brass-dry", "brass", 0}, {"guitar-clean", "guitar", 0}, {"guitar-drive", "guitar", .7}} {
		start := time.Now()
		x := render(spec.kind, events, 8, spec.drive)
		m := stats(x)
		m.RenderMS = time.Since(start).Milliseconds()
		metrics[spec.name] = m
		if e := wav(filepath.Join(*dir, spec.name+".wav"), x, x); e != nil {
			panic(e)
		}
	}
	l := make([]float64, 12*sr)
	r := make([]float64, len(l))
	for j, k := range []string{"bow", "brass", "guitar"} {
		seq := []event{}
		notes := []int{57, 60, 64, 62, 57, 55, 60, 59}
		for i, n := range notes {
			dur := .75
			at := float64(i)*1.2 + .1*float64(j)
			if j == 0 {
				n += 12
				dur = 1.05
			}
			if j == 2 {
				n -= 12
				dur = .8
			}
			seq = append(seq, event{at, dur, n, .7})
		}
		drive := 0.
		if j == 2 {
			drive = .55
		}
		x := render(k, seq, 12, drive)
		pan := []float64{-.4, .4, 0}[j]
		for i, v := range x {
			l[i] += v * .32 * math.Sqrt((1-pan)/2)
			r[i] += v * .32 * math.Sqrt((1+pan)/2)
		}
	}
	metrics["trio-mix"] = stats(l)
	if e := wav(filepath.Join(*dir, "trio-mix.wav"), l, r); e != nil {
		panic(e)
	}
	data, _ := json.MarshalIndent(metrics, "", "  ")
	os.WriteFile(filepath.Join(*dir, "metrics.json"), data, 0644)
	fmt.Println(string(data))
}
