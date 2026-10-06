package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/render"
)

func acceptedMasterScore(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs", "spec", "features.md"))
	if err != nil {
		t.Fatal(err)
	}
	section := strings.SplitN(string(data), "## Mastering targets and the master insert chain", 2)
	if len(section) != 2 {
		t.Fatal("missing mastering design")
	}
	fence := strings.SplitN(section[1], "```cicada\n", 2)
	if len(fence) != 2 {
		t.Fatal("accepted master fence is not runnable")
	}
	return []byte(strings.SplitN(fence[1], "```", 2)[0])
}

func TestAcceptedMasteringCheckRenderAndVerify(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mastering.cicada")
	if err := os.WriteFile(path, acceptedMasterScore(t), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := checkCommand([]string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("check: %v %s", err, stderr.String())
	}
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	opts := render.Options{SampleRate: 48000, Bits: 24, Bars: 16}
	target := renderTargetOptions{ExportName: "streaming", TruePeakMaxDBTP: -1}
	if err := applyExportTarget(inspection.semantic, &opts, &target); err != nil {
		t.Fatal(err)
	}
	if !target.Loudness || target.LoudnessTarget != -14 || target.TruePeakMaxDBTP != -1 || opts.Normalize {
		t.Fatal("saved delivery target not applied")
	}
	wav := filepath.Join(dir, "mastering.wav")
	result, err := renderLoudnessFileReport(inspection.score, wav, opts, target, nil)
	if err != nil {
		t.Fatalf("target render: %v (%+v)", err, result)
	}
	report, err := render.VerifyWAV(wav, render.VerifyOptions{SampleRate: 48000, Bits: 24, Bars: 16, PeakMaxDB: -.3, DCMaxDB: -60, LUFSTarget: -14, LUFSTolerance: .5, CheckLUFS: true, TruePeakMaxDBTP: -1, CheckTruePeak: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("METRIC MASTER bars=16 integrated_lufs=%.4f true_peak_dbtp=%.4f passes=%d", report.IntegratedLUFS, report.TruePeakDBTP, result.Passes)
	var page bytes.Buffer
	if err := writeScorePage(&page, inspection.semantic, string(inspection.source), "mastering.cicada", true, "test"); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Master chain", "glue · comp", "Safety limiter", "streaming", "-14 LUFS", "-1 dBTP"} {
		if !strings.Contains(page.String(), text) {
			t.Fatalf("Studio omitted %q", text)
		}
	}
}
