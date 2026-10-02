package project

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/notation"
)

func assetFixture(t *testing.T) (string, []byte, *notation.Score) {
	t.Helper()
	dir := t.TempDir()
	data := testwav.Bytes(48000, 1, 16, 4800, 1)
	if err := os.WriteFile(filepath.Join(dir, "vocal.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte(fmt.Sprintf(`asset vocal "vocal.wav" { sha256 = "%x" format = wav frames = 4800 rate = 48000Hz channels = 1 }
clip vocal-a vocal { start = 10ms end = 100ms gain = -2dB fade_in = 1ms }
sampler hit { asset = vocal root = c3 mode = oneshot voices = 8 }
track chops hit {}
track vox audio {}
pattern hits { c3 . c3 . }
scene verse { chops = hits vox = vocal-a }
song { verse*4 }
`, sha256.Sum256(data)))
	score, ds := notation.ParseEdition(source, 2)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	return dir, source, score
}
func requireAssetDiagnostic(t *testing.T, ds []notation.Diagnostic, code string) {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && d.Severity == "error" && d.Position.Line > 0 && d.Position.Column > 0 && strings.Contains(d.Message, "expected") && strings.Contains(d.Message, "actual") {
			return
		}
	}
	t.Fatalf("missing %s: %+v", code, ds)
}
func TestAssetHashMismatchDiagnostic(t *testing.T) {
	dir, _, score := assetFixture(t)
	score.Assets[0].SHA256 = strings.Repeat("0", 64)
	requireAssetDiagnostic(t, VerifyAssets(score, dir), "CICADA-ASSET-HASH")
}
func TestAssetMissingFileDiagnostic(t *testing.T) {
	dir, _, score := assetFixture(t)
	score.Assets[0].Path = "missing.wav"
	requireAssetDiagnostic(t, VerifyAssets(score, dir), "CICADA-ASSET-MISSING")
}
func TestAssetFormatDiagnostic(t *testing.T) {
	dir, source, score := assetFixture(t)
	for _, change := range [][2]string{{"format = wav", "format = flac"}, {"rate = 48000Hz", "rate = 48000"}, {"channels = 1", "channels = 3"}, {"frames = 4800", "frames = 0"}} {
		_, ds := notation.ParseEdition(bytes.Replace(source, []byte(change[0]), []byte(change[1]), 1), 2)
		requireAssetDiagnostic(t, ds, "CICADA-ASSET-FORMAT")
	}
	score.Assets[0].RateHz = 44100
	requireAssetDiagnostic(t, VerifyAssets(score, dir), "CICADA-ASSET-FORMAT")
	score.Assets[0].RateHz = 48000
	if err := os.WriteFile(filepath.Join(dir, "vocal.wav"), []byte("not a WAV"), 0600); err != nil {
		t.Fatal(err)
	}
	requireAssetDiagnostic(t, VerifyAssets(score, dir), "CICADA-ASSET-FORMAT")
}
func TestAssetPathsAndHeaders(t *testing.T) {
	dir, _, score := assetFixture(t)
	external := filepath.Join(t.TempDir(), "outside.wav")
	if err := os.WriteFile(external, testwav.Bytes(48000, 1, 16, 4800, 1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "escape.wav")); err == nil {
		score.Assets[0].Path = "escape.wav"
		requireAssetDiagnostic(t, VerifyAssets(score, dir), "CICADA-ASSET-PATH")
	}
	for _, path := range []string{"../outside.wav", "/outside.wav", `C:\outside.wav`} {
		score.Assets[0].Path = path
		requireAssetDiagnostic(t, VerifyAssets(score, dir), "CICADA-ASSET-PATH")
	}
	for _, encoding := range []int{1, 3} {
		for _, bits := range []int{16, 24, 32} {
			if encoding == 3 && bits != 32 {
				continue
			}
			for _, channels := range []int{1, 2} {
				data := testwav.Bytes(48000, channels, bits, 23, encoding)
				h, err := audioasset.ReadWAVHeader(bytes.NewReader(data))
				if err != nil || h.BitDepth != bits || h.Frames != 23 || h.Channels != channels {
					t.Fatalf("%+v %v", h, err)
				}
			}
		}
	}
}
func TestAudioProjectRoundTrip(t *testing.T) {
	dir, _, score := assetFixture(t)
	if ds := VerifyAssets(score, dir); hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatalf("lower: %+v", ds)
	}
	if len(p.Assets) != 1 || len(p.Clips) != 1 || len(p.Samplers) != 1 || p.Scenes[0].Bindings["vox"] != "vocal-a" {
		t.Fatalf("missing engine data: %+v", p)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !SemanticEqual(p, decoded) {
		t.Fatal("JSON lost audio data")
	}
	source, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	s, ds := notation.ParseEdition(source, 2)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	recompiled, ds := FromScore(s)
	if recompiled == nil || !SemanticEqual(p, recompiled) {
		t.Fatalf("source lost audio data: %+v", ds)
	}
	if _, err := CompileEngine(p, 48000, 128); err == nil || !strings.Contains(err.Error(), "CICADA-UNSUPPORTED") {
		t.Fatalf("legacy engine silently accepted audio: %v", err)
	}
}
func TestAudioOnlyProject(t *testing.T) {
	_, source, _ := assetFixture(t)
	text := string(source)
	text = strings.ReplaceAll(text, "track chops hit {}\n", "")
	text = strings.ReplaceAll(text, "pattern hits { c3 . c3 . }\n", "")
	text = strings.ReplaceAll(text, "chops = hits ", "")
	score, ds := notation.ParseEdition([]byte(text), 2)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatalf("audio-only: %+v", ds)
	}
}
func TestScoresWithoutAssetsCompileUnchanged(t *testing.T) {
	data, err := os.ReadFile("testdata/scores-without-assets.json")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string][2]string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	// This baseline freezes the pre-audio corpus; new examples have their own tests.
	for _, key := range sortedKeys(want) {
		path := filepath.Join("..", filepath.FromSlash(key))
		t.Run(path, func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			e, m, err := edition.ScoreEdition(path)
			if err != nil {
				t.Fatal(err)
			}
			var score *notation.Score
			var ds []notation.Diagnostic
			if m != "" {
				score, ds = notation.ParseEdition(source, e)
			} else {
				score, ds = notation.Parse(source)
			}
			if hasErrors(ds) {
				t.Fatal(ds)
			}
			p, ds := FromScore(score)
			if p == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			config, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			actual := [2]string{fmt.Sprintf("%x", sha256.Sum256(data)), fmt.Sprintf("%x", sha256.Sum256(config))}
			if expected := want[key]; expected != actual {
				t.Fatalf("JSON/engine configuration changed: expected %v actual %v", expected, actual)
			}
		})
	}
}
func BenchmarkAssets64Clips256(b *testing.B) {
	dir := b.TempDir()
	data := testwav.Bytes(48000, 1, 16, 4800, 1)
	if err := os.WriteFile(filepath.Join(dir, "vocal.wav"), data, 0600); err != nil {
		b.Fatal(err)
	}
	var text strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&text, "asset a%d \"vocal.wav\" {sha256 = \"%x\" format = wav frames = 4800 rate = 48000Hz channels = 1}\n", i, sha256.Sum256(data))
	}
	for i := 0; i < 256; i++ {
		fmt.Fprintf(&text, "clip c%d a%d {start = 1ms end = 100ms}\n", i, i%64)
	}
	text.WriteString("track vox audio {}\nscene main {vox=c0}\nsong {main}\n")
	source := []byte(text.String())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, ds := notation.ParseEdition(source, 2)
		if hasErrors(ds) {
			b.Fatal(ds)
		}
		if ds := VerifyAssets(s, dir); hasErrors(ds) {
			b.Fatal(ds)
		}
		if p, ds := FromScore(s); p == nil || hasErrors(ds) {
			b.Fatal(ds)
		}
	}
}
func BenchmarkAssetHash10MB(b *testing.B) {
	path := filepath.Join(b.TempDir(), "ten-megabytes")
	if err := os.WriteFile(path, make([]byte, 10_000_000), 0600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(10_000_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}
		_, err = audioasset.SHA256(f)
		f.Close()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestEditionOneAudioNameCompatibility(t *testing.T) {
	for _, fixture := range []struct{ path, name string }{{"glassbass.cicada", "glassbass"}, {"authored-kit.cicada", "steel"}} {
		t.Run(fixture.path, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("..", "examples", fixture.path))
			if err != nil {
				t.Fatal(err)
			}
			original, ds := notation.ParseEdition(source, 1)
			if hasErrors(ds) {
				t.Fatal(ds)
			}
			before, ds := FromScore(original)
			if before == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			want, err := CompileEngine(before, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			renamed := bytes.ReplaceAll(source, []byte(fixture.name), []byte("audio"))
			score, ds := notation.ParseEdition(renamed, 1)
			if hasErrors(ds) {
				t.Fatal(ds)
			}
			p, ds := FromScore(score)
			if p == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			if p.HasAudio() || p.Format != before.Format {
				t.Fatal("edition 1 name changed audio semantics or project format")
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			p, err = DecodeJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			got, err := CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatal("renaming changed engine configuration")
			}
			_, ds = notation.ParseEdition(renamed, 2)
			if !hasErrors(ds) {
				t.Fatal("edition 2 allowed reserved audio name")
			}
		})
	}
}
