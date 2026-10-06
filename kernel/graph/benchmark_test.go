package graph_test

import (
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

var graphBenchmarkOutput float32

// These are graph interpreter workloads, rather than the experimental physical
// models. Each reported operation renders one sample of one configured voice.
func BenchmarkGraphStyles(b *testing.B) {
	for _, style := range []struct{ name, body string }{
		{"pluck", "let shape = env(gate, 330ms); out = lowpass(saw(pitch), 2400Hz) * shape;"},
		{"brass", "let shape = env(gate, 900ms); let body = mix(saw(pitch), saw(pitch * 1.005), 0.4); out = tanh(diode(body, 3200Hz, 0.55) * 2) * shape;"},
		{"bowed", "let shape = env(gate, 2400ms); let body = mix(saw(pitch), saw(pitch * 0.998), 0.5); let bow = tanh(body * 3); out = mix(ladder(bow, 2200Hz, 0.65), lowpass(bow, 6000Hz), 0.3) * shape;"},
	} {
		b.Run(style.name+"/48000", func(b *testing.B) {
			score, diagnostics := notation.ParseEdition([]byte("instrument voice { voice mono { "+style.body+" } } track t voice {} pattern p { 1 } scene s { t = p } song { s }"), 2)
			for _, d := range diagnostics {
				if d.Severity == "error" {
					b.Fatal(d)
				}
			}
			compiled, diagnostics := instrument.Compile(score.Instruments[0])
			for _, d := range diagnostics {
				if d.Severity == "error" {
					b.Fatal(d)
				}
			}
			p, err := instrument.Lower(compiled, nil)
			if err != nil {
				b.Fatal(err)
			}
			v, err := graph.NewVoice(p, 48000)
			if err != nil {
				b.Fatal(err)
			}
			v.NoteOn(57, 100, false)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i&4095 == 0 {
					v.NoteOn(57, 100, false)
				}
				graphBenchmarkOutput = v.Next()
			}
		})
	}
}
