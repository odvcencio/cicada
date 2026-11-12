package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// writeCheckProject writes a score and the WAV files it names, each a silent
// 16-bit file, and returns the score's path. assets maps a file name to its
// {rate, channels, frames}; a placeholder such as {{a.wav}} in the score
// becomes that file's SHA-256.
func writeCheckProject(t *testing.T, assets map[string][3]int, score string) string {
	t.Helper()
	dir := t.TempDir()
	for name, spec := range assets {
		wav := testwav.Bytes(spec[0], spec[1], 16, spec[2], 1)
		if err := os.WriteFile(filepath.Join(dir, name), wav, 0o600); err != nil {
			t.Fatal(err)
		}
		score = strings.ReplaceAll(score, "{{"+name+"}}", fmt.Sprintf("%x", sha256.Sum256(wav)))
	}
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(score), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// checkAndRender checks the score as cicada check --json does and renders it
// whole at rate, and returns the check's report and the render's error.
func checkAndRender(t *testing.T, path string, rate int) (checkScoreReport, error) {
	t.Helper()
	report := checkScoreJSON(path, checkOptions{profile: profileDesktop})
	inspection, err := inspectScore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = render.WAV(inspection.score, render.Options{SampleRate: rate, Bits: 24, AssetRoot: inspection.root(path), AssetDir: inspection.root(path)}, io.Discard)
	return report, err
}

// The asset rates that render refuses: a sampler's asset above 192 kHz and a
// clip's at 384 kHz refuse at every rate, and an 8 kHz clip at 96 kHz only.
func TestCheckRefusesAnAssetTheSampleVoiceCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name, score       string
		rate              int
		severity, expects string
	}{
		{"sampler at 384 kHz", "asset hi \"a.wav\" { sha256 = \"{{a.wav}}\" format = wav frames = 9600 rate = 384000Hz channels = 1 }\nsampler s { asset = hi root = c4 mode = oneshot voices = 2 }\ntrack t s {}\npattern p { c4 . }\nscene m { t = p }\nsong { m }\n", 384_000, "error", "8000 to 192000 Hz"},
		{"clip at 384 kHz", "asset hi \"a.wav\" { sha256 = \"{{a.wav}}\" format = wav frames = 9600 rate = 384000Hz channels = 1 }\nclip c hi { start = 0frames end = 9600frames }\ntrack v audio {}\nscene m { v = c }\nsong { m }\n", 384_000, "error", "12000 to 192000 Hz"},
		{"clip at 8 kHz", "asset lo \"a.wav\" { sha256 = \"{{a.wav}}\" format = wav frames = 8000 rate = 8000Hz channels = 1 }\nclip c lo { start = 0frames end = 8000frames }\ntrack v audio {}\nscene m { v = c }\nsong { m }\n", 8_000, "warning", "12000 to 192000 Hz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := 9600
			if tc.rate == 8_000 {
				frames = 8000
			}
			path := writeCheckProject(t, map[string][3]int{"a.wav": {tc.rate, 1, frames}}, "cicada 2\ntempo 120\n"+tc.score)
			report, renderErr := checkAndRender(t, path, 48_000)
			if len(report.Diagnostics) != 1 {
				t.Fatalf("check: %+v", report.Diagnostics)
			}
			d := report.Diagnostics[0]
			if d.Code != "CICADA-ASSET-FORMAT" || d.Severity != tc.severity || d.Expected != tc.expects || d.Found != fmt.Sprintf("%d Hz", tc.rate) || d.Hint == "" || d.Start == nil || d.Start.Line != 4 {
				t.Fatalf("diagnostic %+v", d)
			}
			// An error is what render at 48 kHz refuses; a warning names a rate
			// where it refuses, and 48 kHz renders.
			if (renderErr != nil) != (tc.severity == "error") || report.Valid != (tc.severity == "warning") {
				t.Fatalf("render at 48 kHz: %v; check valid %v", renderErr, report.Valid)
			}
			if tc.severity == "warning" {
				if _, err := checkAndRender(t, path, 96_000); err == nil {
					t.Fatal("render at 96 kHz played a clip check warned it cannot")
				}
				if !strings.Contains(d.Message, "at 96 kHz output") {
					t.Fatalf("the warning does not name 96 kHz: %s", d.Message)
				}
			}
		})
	}
}

// Sampler assets share 64 MiB of resident PCM in render; check counts them
// from their declarations, as render does before it decodes them. Two stereo
// assets of 4.2 million frames need 67.2 MB of float PCM.
func TestCheckRefusesSamplerAssetsOverTheResidentBudget(t *testing.T) {
	const frames = 4_200_000
	path := writeCheckProject(t, map[string][3]int{"a.wav": {48_000, 2, frames}, "b.wav": {48_000, 2, frames + 1}}, fmt.Sprintf(`cicada 2
tempo 120
asset a "a.wav" { sha256 = "{{a.wav}}" format = wav frames = %d rate = 48000Hz channels = 2 }
asset b "b.wav" { sha256 = "{{b.wav}}" format = wav frames = %d rate = 48000Hz channels = 2 }
sampler sa { asset = a root = c4 mode = oneshot voices = 1 }
sampler sb { asset = b root = c4 mode = oneshot voices = 1 }
track ta sa {}
track tb sb {}
pattern p { c4 . }
scene m { ta = p tb = p }
song { m }
`, frames, frames+1))
	report, renderErr := checkAndRender(t, path, 48_000)
	if renderErr == nil || !strings.Contains(renderErr.Error(), "64 MiB resident PCM budget") {
		t.Fatalf("render: %v", renderErr)
	}
	if report.Valid || len(report.Diagnostics) != 1 {
		t.Fatalf("check: %+v", report.Diagnostics)
	}
	d := report.Diagnostics[0]
	if d.Code != "CICADA-LIMIT" || !strings.Contains(d.Message, "64 MiB resident PCM budget") || d.Expected != "at most 64.0 MiB of resident PCM" || d.Found != "64.1 MiB" {
		t.Fatalf("diagnostic %+v", d)
	}
}

// engineDiagnostic files the engine compile's refusals under their codes.
func TestEngineDiagnosticCodes(t *testing.T) {
	budget := engineDiagnostic(fmt.Errorf("compile: %w", &schedule.ResidentBudgetError{Name: "asset a", Bytes: 40 << 20, Used: 30 << 20}))
	if budget.Code != "CICADA-LIMIT" || budget.Found != "70.0 MiB" || budget.Position != (notation.Position{Line: 1, Column: 1}) {
		t.Fatalf("budget: %+v", budget)
	}
	if other := engineDiagnostic(fmt.Errorf("track t: something else")); other.Code != "CICADA-PARAM" || other.Expected != "" {
		t.Fatalf("other: %+v", other)
	}
}

// A song longer than 256 bars, on the direct mixer, and a song with more than
// 256 tempo changes stop a render of the whole song; check says so at the song
// or the arrangement, and a range of it still renders.
func TestCheckReportsTheLimitsOfAWholeSongRender(t *testing.T) {
	direct := writeCheckProject(t, nil, "cicada 2\ntempo 300\ntrack b acid {}\npattern r { a1 . }\nscene m { b = r }\nsong { m*300 }\n")
	report, renderErr := checkAndRender(t, direct, 48_000)
	if renderErr == nil || len(report.Diagnostics) != 1 {
		t.Fatalf("render %v; check %+v", renderErr, report.Diagnostics)
	}
	if d := report.Diagnostics[0]; d.Code != "CICADA-LIMIT" || d.Start == nil || d.Start.Line != 6 || d.Expected != "at most 256 bars" || d.Found != "300 bars" || !strings.Contains(d.Hint, "--from") {
		t.Fatalf("diagnostic %+v", d)
	}
	inspection, err := inspectScore(direct)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := render.WAV(inspection.score, render.Options{SampleRate: 48_000, Bits: 24, From: 254, Bars: 2}, io.Discard); err != nil {
		t.Fatalf("a range of the song: %v", err)
	}

	// The scheduled engine renders 300 bars; 300 tempo steps stop it.
	var steps strings.Builder
	for bar := 1; bar <= 300; bar++ {
		fmt.Fprintf(&steps, "  tempo @%d.1.1 %d\n", bar, 121-bar%2)
	}
	arranged := writeCheckProject(t, nil, "cicada 2\ntempo 120\ntrack b acid {}\npattern r { a1 . }\narrange {\n"+steps.String()+"  place one b r { at = @1.1.1 length = 300bars }\n}\n")
	report, renderErr = checkAndRender(t, arranged, 48_000)
	if renderErr == nil || len(report.Diagnostics) != 1 {
		t.Fatalf("render %v; check %+v", renderErr, report.Diagnostics)
	}
	if d := report.Diagnostics[0]; d.Code != "CICADA-LIMIT" || d.Start == nil || d.Start.Line != 5 || d.Expected != "at most 256 tempo changes" || d.Found != "299 tempo changes" {
		t.Fatalf("diagnostic %+v", d)
	}
}
