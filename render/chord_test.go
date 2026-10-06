package render

import (
	"bytes"
	"m31labs.dev/cicada/notation"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const polyphonicScore = `tempo 120
key d minor
seed 7
instrument piano { voice poly { out=sine(pitch)*env(gate,300ms)*0.1 } }
track keys piano {}
pattern a notes { [d4 f4 a4]^?70 - . [c4 e4 g4] }
pattern b notes { [e4 g4 b4] - . . }
scene verse { keys=a }
scene chorus { keys=b }
scene stop { keys=off }
song { verse chorus stop }
`

func TestChordWAVKernelScenesStemsAndBlockParity(t *testing.T) {
	score, ds := notation.Parse([]byte(polyphonicScore))
	if score == nil {
		t.Fatalf("parse %+v", ds)
	}
	var reference []byte
	for _, block := range []int{17, 128, 4096} {
		var output bytes.Buffer
		report, err := WAV(score, Options{SampleRate: 48000, Bits: 32, Block: block}, &output)
		if err != nil {
			t.Fatal(err)
		}
		if report.Peak < 0.001 {
			t.Fatal("chords rendered silence")
		}
		if reference == nil {
			reference = append([]byte(nil), output.Bytes()...)
		} else if !bytes.Equal(reference, output.Bytes()) {
			t.Fatalf("offline block parity changed at %d", block)
		}
		assertSceneEngineMatchesWAV(t, score, output.Bytes(), 48000, 0, int(report.Frames))
	}
	dir := filepath.Join(t.TempDir(), "stems")
	if _, err := Stems(score, Options{SampleRate: 48000, Bits: 32}, dir); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.wav"))
	if err != nil {
		t.Fatal(err)
	}
	trackCount := 0
	for _, file := range files {
		if strings.Contains(filepath.Base(file), "keys") {
			trackCount++
			data, err := os.ReadFile(file)
			if err != nil || len(data) < 44 {
				t.Fatal("track stem missing")
			}
		}
	}
	if trackCount != 1 {
		t.Fatalf("chord created %d track stems", trackCount)
	}
}

func TestScalarSlideIntoNextSceneChordStartsNewCohort(t *testing.T) {
	source := `tempo 120 key d minor instrument piano { voice poly { out=sine(pitch)*env(gate,300ms)*0.1 } } track keys piano {} pattern a notes { d4~ } pattern b notes { [e4 g4 b4] } scene verse { keys=a } scene chorus { keys=b } song { verse chorus }`
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("source %+v", ds)
	}
	var output bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48000, Bits: 32}, &output)
	if err != nil {
		t.Fatal(err)
	}
	assertSceneEngineMatchesWAV(t, score, output.Bytes(), 48000, 0, int(report.Frames))
}

func TestPolyChanceUsesAssignedSlotsAcrossScenes(t *testing.T) {
	source := strings.ReplaceAll(polyphonicScore, "[e4 g4 b4] - . .", "[e4 g4 b4]?50 - . .")
	score, ds := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("source %+v", ds)
	}
	var output bytes.Buffer
	report, err := WAV(score, Options{SampleRate: 48000, Bits: 32}, &output)
	if err != nil {
		t.Fatal(err)
	}
	assertSceneEngineMatchesWAV(t, score, output.Bytes(), 48000, 0, int(report.Frames))
}
