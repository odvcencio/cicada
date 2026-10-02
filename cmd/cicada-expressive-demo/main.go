// Render deterministic research auditions, without sample or IR dependencies.
package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := writeWAV(f, l, r); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// writeWAV owns f, including its close result. Both partial writes and a failed
// final flush/close must reach the caller before a render can report success.
func writeWAV(f io.WriteCloser, l, r []float64) (err error) {
	defer func() { err = errors.Join(err, f.Close()) }()
	if len(l) != len(r) {
		return fmt.Errorf("WAV channel lengths differ: %d and %d", len(l), len(r))
	}
	n := len(l) * 4
	var header [44]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(n+36))
	copy(header[8:16], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 2)
	binary.LittleEndian.PutUint32(header[24:28], sr)
	binary.LittleEndian.PutUint32(header[28:32], sr*4)
	binary.LittleEndian.PutUint16(header[32:34], 4)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(n))
	if err := writeBytes(f, header[:]); err != nil {
		return err
	}
	var sample [2]byte
	for i, x := range l {
		for _, v := range [2]float64{x, r[i]} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1 {
				return fmt.Errorf("invalid output sample %d: %g", i, v)
			}
			binary.LittleEndian.PutUint16(sample[:], uint16(int16(math.Round(v*32767))))
			if err := writeBytes(f, sample[:]); err != nil {
				return fmt.Errorf("sample %d: %w", i, err)
			}
		}
	}
	return nil
}
func writeBytes(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
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
	if err := run(*dir, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(dir string, output io.Writer) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
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
		if e := wav(filepath.Join(dir, spec.name+".wav"), x, x); e != nil {
			return e
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
	if e := wav(filepath.Join(dir, "trio-mix.wav"), l, r); e != nil {
		return e
	}
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "metrics.json"), data, 0644); err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(data))
	return err
}
