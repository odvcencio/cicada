package render

import (
	"bytes"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"os"
	"path/filepath"
	"testing"
)

func TestArrangementTempoWAVStemsAndNativePCM(t *testing.T) {
	source, err := os.ReadFile("../examples/arrangement-tempo.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	p, more := project.FromScore(score)
	if p == nil || len(ds) > 0 || len(more) > 0 {
		t.Fatal(ds, more)
	}
	for _, rate := range []int{44100, 48000} {
		a, b := renderBoth(t, string(source), rate, 6)
		peak, _ := driftDelta(t, a, b)
		if peak != 0 {
			t.Fatal("scheduled tempo PCM differs", rate, peak)
		}
	}
	clock, err := project.NewTempoMap(48000, int64(p.TempoMilli), project.SongTempoChanges(p))
	if err != nil {
		t.Fatal(err)
	}
	var wav bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48000, From: 1, Bars: 4, TailSec: .5}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.SampleAtTick(5*3840) - clock.SampleAtTick(3840) + 24000; report.Frames != want {
		t.Fatal("tempo excerpt length", report.Frames, want)
	}
	dir := filepath.Join(t.TempDir(), "stems")
	stemReport, err := Stems(score, Options{SampleRate: 48000, From: 1, Bars: 4, TailSec: .5}, dir)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80})
	if err != nil || verified.Frames != stemReport.Frames || stemReport.Frames != report.Frames {
		t.Fatal("tempo stems", err, verified.Frames, stemReport.Frames)
	}
}
