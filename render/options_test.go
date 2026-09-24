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

	"m31labs.dev/cicada/kernel/seq"
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

func TestWAVBarRangeMatchesFullRender(t *testing.T) {
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
	firstBar := clock.SampleAtTick(seq.TicksPerBar)
	start := clock.SampleAtTick(2 * seq.TicksPerBar)
	end := clock.SampleAtTick(3 * seq.TicksPerBar)
	if end-start == firstBar {
		t.Fatal("test tempo did not produce a distinct third-bar frame count")
	}
	for _, bits := range []int{16, 24, 32} {
		t.Run(strconv.Itoa(bits), func(t *testing.T) {
			var full, part bytes.Buffer
			if _, err := WAV(score, Options{SampleRate: 48_000, Bits: bits}, &full); err != nil {
				t.Fatal(err)
			}
			report, err := WAV(score, Options{SampleRate: 48_000, Bits: bits, From: 2, Bars: 1}, &part)
			if err != nil {
				t.Fatal(err)
			}
			var remaining bytes.Buffer
			remainingReport, err := WAV(score, Options{SampleRate: 48_000, Bits: bits, From: 2}, &remaining)
			if err != nil || remainingReport.Bars != 1 || !bytes.Equal(remaining.Bytes(), part.Bytes()) {
				t.Fatalf("bars=0 did not render the remaining song: %+v %v", remainingReport, err)
			}
			if report.From != 2 || report.Bars != 1 || report.Frames != end-start {
				t.Fatalf("range duration: %+v, want %d frames", report, end-start)
			}
			frameBytes := bits / 4
			want := full.Bytes()[44+int(start)*frameBytes : 44+int(end)*frameBytes]
			got := part.Bytes()[44 : 44+int(report.Frames)*frameBytes]
			if !bytes.Equal(want, got) {
				t.Fatal("bar-range WAV differs from the matching full-render frames")
			}
			path := filepath.Join(t.TempDir(), "range.wav")
			if err := os.WriteFile(path, part.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			verify := VerifyOptions{SampleRate: 48_000, Bits: bits, From: 2, Bars: 1, PeakMaxDB: 0, DCMaxDB: 0}
			if _, err := VerifyWAV(path, verify); err != nil {
				t.Fatal(err)
			}
			verify.From = 0
			if _, err := VerifyWAV(path, verify); err == nil {
				t.Fatal("incorrect start bar passed duration verification")
			}
		})
	}
	for _, invalid := range []Options{{From: -1}, {From: 3}, {From: 2, Bars: 2}} {
		var output bytes.Buffer
		if _, err := WAV(score, invalid, &output); err == nil || output.Len() != 0 {
			t.Fatalf("invalid bar range wrote output: %+v, %v", invalid, err)
		}
	}
}

func TestWAVBarRangePreservesEffectStateAndAlignment(t *testing.T) {
	for _, example := range []string{"fx-bus.cicada", filepath.Join("fx", "drive-insert.cicada")} {
		t.Run(example, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "examples", example))
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
			var full, part bytes.Buffer
			// Both renders stop at bar three, so post-song release and drive drain agree.
			if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 32, Bars: 3}, &full); err != nil {
				t.Fatal(err)
			}
			report, err := WAV(score, Options{SampleRate: 48_000, Bits: 32, From: 1, Bars: 2}, &part)
			if err != nil {
				t.Fatal(err)
			}
			if report.Frames != end-start {
				t.Fatalf("%s has %d frames, want %d", example, report.Frames, end-start)
			}
			want := full.Bytes()[44+int(start)*8 : 44+int(end)*8]
			got := part.Bytes()[44 : 44+int(report.Frames)*8]
			if !bytes.Equal(want, got) {
				for index := range want {
					if want[index] != got[index] {
						t.Fatalf("%s differs at output frame %d byte %d: %x versus %x", example, index/8, index%8, want[index:index+1], got[index:index+1])
					}
				}
			}
		})
	}
}
