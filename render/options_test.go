package render

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestWAVFormatsBlockIndependenceAndNormalization(t *testing.T) {
	score, diagnostics := notation.Parse([]byte(testScore))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	for _, bits := range []int{16, 24, 32} {
		t.Run(strconv.Itoa(bits), func(t *testing.T) {
			options := Options{SampleRate: 48_000, Bits: bits, Bars: 1, TailSec: .00001}
			var large, small bytes.Buffer
			largeReport, err := WAV(score, options, &large)
			if err != nil {
				t.Fatal(err)
			}
			options.Block = 64
			if _, err := WAV(score, options, &small); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(large.Bytes(), small.Bytes()) {
				t.Fatal("WAV changed with render block size")
			}
			if largeReport.TailFrames != 1 {
				t.Fatalf("fractional tail did not round up: %+v", largeReport)
			}
			path := filepath.Join(t.TempDir(), "format.wav")
			if err := os.WriteFile(path, large.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			verified, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: bits, Bars: 1, TailSec: .00001, PeakMaxDB: 0, DCMaxDB: 0})
			if err != nil || verified.Frames != largeReport.Frames {
				t.Fatalf("verify %d-bit WAV: %+v %v", bits, verified, err)
			}
			if bits == 32 {
				corrupt := bytes.Clone(large.Bytes())
				binary.LittleEndian.PutUint32(corrupt[44:48], math.Float32bits(float32(math.NaN())))
				if err := os.WriteFile(path, corrupt, 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 32, Bars: 1, TailSec: .00001, PeakMaxDB: 0, DCMaxDB: 0}); err == nil {
					t.Fatal("nonfinite float WAV sample accepted")
				}
			} else {
				corrupt := bytes.Clone(large.Bytes())
				for index := 0; index < bits/8; index++ {
					corrupt[44+index] = 0xff
				}
				corrupt[44+bits/8-1] = 0x7f
				if err := os.WriteFile(path, corrupt, 0644); err != nil {
					t.Fatal(err)
				}
				if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: bits, Bars: 1, TailSec: .00001, PeakMaxDB: 0, DCMaxDB: 0}); err == nil || !strings.Contains(err.Error(), "clipped") {
					t.Fatalf("full-scale integer sample accepted: %v", err)
				}
			}
		})
	}
	options := Options{SampleRate: 48_000, Bits: 32, Bars: 1, Normalize: true}
	var normalized bytes.Buffer
	report, err := WAV(score, options, &normalized)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(amplitudeDB(float64(report.OutputPeak))+1) > .001 {
		t.Fatalf("normalized peak differs from -1 dBFS: %+v", report)
	}
	path := filepath.Join(t.TempDir(), "normalized.wav")
	if err := os.WriteFile(path, normalized.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyWAV(path, VerifyOptions{SampleRate: 48_000, Bits: 32, Bars: 1, PeakMaxDB: -.99, DCMaxDB: 0}); err != nil {
		t.Fatal(err)
	}
}
