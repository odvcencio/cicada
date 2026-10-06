package graph_test

import (
	"os"
	"strconv"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/notation"
)

func authoredPMPrograms(tb testing.TB) []graph.Program {
	tb.Helper()
	source, err := os.ReadFile("../../examples/fm-bell.cicada")
	if err != nil {
		tb.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil || len(ds) != 0 {
		tb.Fatalf("parse: %+v", ds)
	}
	var programs []graph.Program
	for _, definition := range score.Instruments {
		p, ds := instrument.Compile(definition)
		if p == nil || len(ds) != 0 {
			tb.Fatalf("compile: %+v", ds)
		}
		lowered, err := instrument.Lower(p, nil)
		if err != nil {
			tb.Fatal(err)
		}
		programs = append(programs, lowered)
	}
	return programs
}

func BenchmarkAuthoredPMVoice(b *testing.B) {
	programs := authoredPMPrograms(b)
	for i, name := range []string{"bell", "bass"} {
		for _, rate := range []int{44_100, 48_000} {
			b.Run(name+"/"+strconv.Itoa(rate), func(b *testing.B) {
				v, err := graph.NewVoice(programs[i], rate)
				if err != nil {
					b.Fatal(err)
				}
				v.NoteOn(69, 127, false)
				b.ReportAllocs()
				b.ResetTimer()
				for j := 0; j < b.N; j++ {
					if j%4096 == 0 {
						v.NoteOn(69, 127, false)
					}
					sampleSink = v.Next()
				}
			})
		}
	}
}
