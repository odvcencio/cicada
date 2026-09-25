package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func TestStemsSinglePassBusEquationsAndMasterAlignment(t *testing.T) {
	for _, example := range []string{"sfx-bus.cicada", filepath.Join("fx", "drive-insert.cicada")} {
		t.Run(example, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "examples", example))
			if err != nil {
				t.Fatal(err)
			}
			score, diagnostics := notation.Parse(source)
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == "error" {
					t.Fatalf("%+v", diagnostic)
				}
			}
			noDither := false
			options := Options{SampleRate: 48_000, Bars: 1, TailSec: 0.1, Dither: &noDither}
			dir := filepath.Join(t.TempDir(), "stems")
			stemReport, err := Stems(score, options, dir)
			if err != nil {
				t.Fatal(err)
			}
			verified, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80})
			if err != nil {
				t.Fatal(err)
			}
			standalone, err := VerifyStems(nil, dir, VerifyStemsOptions{ResidualMaxDB: -80})
			if err != nil || standalone != verified {
				t.Fatalf("directory-only verification differs: %+v, %v", standalone, err)
			}
			if verified.Frames != stemReport.Frames || verified.Files != len(score.Tracks)+5 {
				t.Fatalf("stems report mismatch: %+v %+v", stemReport, verified)
			}
			master, err := os.ReadFile(filepath.Join(dir, "master.wav"))
			if err != nil {
				t.Fatal(err)
			}
			var pcm bytes.Buffer
			wavReport, err := WAV(score, options, &pcm)
			if err != nil {
				t.Fatal(err)
			}
			if stemReport.Frames != wavReport.Frames || len(master) != int(stemReport.Frames)*8+68 || pcm.Len() != int(stemReport.Frames)*6+68 {
				t.Fatalf("render lengths differ: %+v %+v", stemReport, wavReport)
			}
			for i := int64(0); i < stemReport.Frames*2; i++ {
				floatAt := 44 + int(i)*4
				value := math.Float32frombits(binary.LittleEndian.Uint32(master[floatAt : floatAt+4]))
				pcmAt := 44 + int(i)*3
				got := int32(pcm.Bytes()[pcmAt]) | int32(pcm.Bytes()[pcmAt+1])<<8 | int32(pcm.Bytes()[pcmAt+2])<<16
				if got&0x800000 != 0 {
					got |= ^int32(0xffffff)
				}
				want := int32(math.Round(float64(value) * 8388607))
				if got != want {
					t.Fatalf("master frame %d channel %d differs: got %d want %d", i/2, i%2, got, want)
				}
			}
		})
	}
}

func TestVerifyStemsDetectsBrokenBusSum(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "sfx-bus.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("%+v", diagnostic)
		}
	}
	dir := filepath.Join(t.TempDir(), "stems")
	if _, err := Stems(score, Options{SampleRate: 48_000, Bars: 1}, dir); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "music.wav"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	var changed [4]byte
	binary.LittleEndian.PutUint32(changed[:], math.Float32bits(.5))
	_, err = file.WriteAt(changed[:], 44)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("corrupt stem: %v %v", err, closeErr)
	}
	if _, err := VerifyStems(score, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err == nil || !strings.Contains(err.Error(), "residual") {
		t.Fatalf("broken sum accepted: %v", err)
	}
}

func TestStemsBarRangeMatchesFullRender(t *testing.T) {
	source := strings.Replace(testScore, "tempo 120", "tempo 137", 1)
	source = strings.Replace(source, "song { main }", "song { main*3 }", 1)
	score, diagnostics := notation.Parse([]byte(source))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	clock, err := seq.NewClock(48_000, score.TempoMilli)
	if err != nil {
		t.Fatal(err)
	}
	start := clock.SampleAtTick(2 * seq.TicksPerBar)
	end := clock.SampleAtTick(3 * seq.TicksPerBar)
	root := t.TempDir()
	fullDir, partDir := filepath.Join(root, "full"), filepath.Join(root, "part")
	if _, err := Stems(score, Options{SampleRate: 48_000, Bits: 32}, fullDir); err != nil {
		t.Fatal(err)
	}
	report, err := Stems(score, Options{SampleRate: 48_000, Bits: 32, From: 2, Bars: 1}, partDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.From != 2 || report.Frames != end-start {
		t.Fatalf("stem range: %+v", report)
	}
	verified, err := VerifyStems(score, partDir, VerifyStemsOptions{ResidualMaxDB: -80})
	if err != nil || verified.From != 2 || verified.Frames != report.Frames {
		t.Fatalf("verify ranged stems: %+v %v", verified, err)
	}
	if _, err := VerifyStems(nil, partDir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatalf("standalone ranged stem verification: %v", err)
	}
	for _, name := range []string{"01-lead.wav", "return-a.wav", "return-b.wav", "music.wav", "sfx.wav", "master.wav"} {
		full, err := os.ReadFile(filepath.Join(fullDir, name))
		if err != nil {
			t.Fatal(err)
		}
		part, err := os.ReadFile(filepath.Join(partDir, name))
		if err != nil {
			t.Fatal(err)
		}
		want := full[44+int(start)*8 : 44+int(end)*8]
		got := part[44 : 44+int(report.Frames)*8]
		if !bytes.Equal(want, got) {
			t.Fatalf("%s differs from full render", name)
		}
	}
}

func TestStemsBarRangeWithDriveAlignment(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "fx", "drive-insert.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	clock, err := seq.NewClock(48_000, score.TempoMilli)
	if err != nil {
		t.Fatal(err)
	}
	start := clock.SampleAtTick(seq.TicksPerBar)
	end := clock.SampleAtTick(3 * seq.TicksPerBar)
	root := t.TempDir()
	fullDir, partDir := filepath.Join(root, "full"), filepath.Join(root, "part")
	if _, err := Stems(score, Options{SampleRate: 48_000, Bits: 32, Bars: 3}, fullDir); err != nil {
		t.Fatal(err)
	}
	report, err := Stems(score, Options{SampleRate: 48_000, Bits: 32, From: 1, Bars: 2}, partDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Frames != end-start {
		t.Fatalf("stem range frame count: %+v", report)
	}
	if _, err := VerifyStems(nil, partDir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(partDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".wav" {
			continue
		}
		full, err := os.ReadFile(filepath.Join(fullDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		part, err := os.ReadFile(filepath.Join(partDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(full[44+int(start)*8:44+int(end)*8], part[44:44+int(report.Frames)*8]) {
			t.Fatalf("%s differs from prefix render after drive alignment", entry.Name())
		}
	}
}
