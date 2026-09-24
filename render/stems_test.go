package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
