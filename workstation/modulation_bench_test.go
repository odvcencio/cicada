package main

import (
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/gosx"
	"os"
	"testing"
)

func BenchmarkModulationDockLargeScore(b *testing.B) {
	data, err := os.ReadFile("../examples/songs/anywhere-you-are.cicada")
	if err != nil {
		b.Fatal(err)
	}
	score, ds := notation.Parse(data)
	if score == nil {
		b.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil {
		b.Fatal(ds)
	}
	if len(p.Modulators) != 0 {
		b.Fatal("fixture now includes modulation")
	}
	s := new(studioApp)
	v := workspace{Project: p, Revision: "r", SourceRevision: "s"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = gosx.RenderHTML(s.modulationDock(v, "csrf"))
	}
}
