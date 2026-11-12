package render

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// TestStemsTracksAndBits renders a chosen track as 24-bit PCM and checks
// that it is the full set's float32 stem rounded, that only it and the
// master are written, and that verify-stems refuses the partial set.
func TestStemsTracksAndBits(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "testdata", "edition1", "examples", "sfx-bus.cicada"))
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
	base := Options{SampleRate: 48_000, Bars: 1, TailSec: 0.1, Dither: &noDither}
	fullDir := filepath.Join(t.TempDir(), "full")
	if _, err := Stems(score, base, fullDir); err != nil {
		t.Fatal(err)
	}
	var trackFile string
	entries, _ := os.ReadDir(fullDir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".wav") && entry.Name()[0] >= '0' && entry.Name()[0] <= '9' {
			trackFile = entry.Name()
			break
		}
	}
	id := strings.TrimSuffix(trackFile[3:], ".wav")
	chosen := base
	chosen.StemTracks, chosen.Bits = []string{id}, 24
	dir := filepath.Join(t.TempDir(), "chosen")
	if _, err := Stems(score, chosen, dir); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(dir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if want := []string{trackFile, "manifest.json", "master.wav"}; strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("files %v, want %v", names, want)
	}
	for _, name := range []string{trackFile, "master.wav"} {
		float, _ := os.ReadFile(filepath.Join(fullDir, name))
		pcm, _ := os.ReadFile(filepath.Join(dir, name))
		frames := (len(float) - 68) / 8
		if len(pcm) != frames*6+68 || binary.LittleEndian.Uint16(pcm[20:]) != 1 || binary.LittleEndian.Uint16(pcm[34:]) != 24 {
			t.Fatalf("%s: %d bytes, format %d, %d bits", name, len(pcm), binary.LittleEndian.Uint16(pcm[20:]), binary.LittleEndian.Uint16(pcm[34:]))
		}
		for i := 0; i < frames*2; i++ {
			value := math.Float32frombits(binary.LittleEndian.Uint32(float[44+i*4:]))
			got := int32(pcm[44+i*3]) | int32(pcm[45+i*3])<<8 | int32(pcm[46+i*3])<<16
			if got&0x800000 != 0 {
				got |= ^int32(0xffffff)
			}
			want := int32(math.RoundToEven(float64(value) * 8388607))
			want = max(-8388608, min(8388607, want))
			if got != want {
				t.Fatalf("%s sample %d: %d, want %d", name, i, got, want)
			}
		}
		if !bytes.Equal(float[len(float)-24:], pcm[len(pcm)-24:]) {
			t.Fatalf("%s: timing chunk differs", name)
		}
	}
	var manifest stemManifest
	data, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if json.Unmarshal(data, &manifest) != nil || strings.Join(manifest.Selected, ",") != id || manifest.Bits != 24 {
		t.Fatalf("manifest %s", data)
	}
	if _, err := VerifyStems(nil, dir, VerifyStemsOptions{}); err == nil || !strings.Contains(err.Error(), "--tracks") {
		t.Fatalf("verify-stems accepted a partial set: %v", err)
	}
	full, _ := os.ReadFile(filepath.Join(fullDir, "manifest.json"))
	if bytes.Contains(full, []byte("selected")) || bytes.Contains(full, []byte("bits")) {
		t.Fatalf("a full float32 set's manifest changed: %s", full)
	}
	_, err = Stems(score, Options{SampleRate: 48_000, Bars: 1, StemTracks: []string{"nope"}}, filepath.Join(t.TempDir(), "bad"))
	var unknown *UnknownTrackError
	if !errors.As(err, &unknown) || unknown.Name != "nope" || len(unknown.Valid) != len(score.Tracks) {
		t.Fatalf("unknown track: %v", err)
	}
}
