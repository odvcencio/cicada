// Package testpack writes synthetic sampler packs for tests. A pack is a valid,
// pinned manifest with gzip WAV assets of the size a test asks for. It holds no
// real recording: the assets are a short repeating ramp, so a pack that decodes
// to tens of megabytes of PCM takes a few kilobytes on disk.
package testpack

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/voice/sample"
)

// Zone is the span of MIDI keys and velocities that one zone of a pack plays.
// Zones must not overlap. Zero velocities select 1 and 127.
type Zone struct {
	KeyLow, KeyHigh           int
	VelocityLow, VelocityHigh int
}

// Options shape a pack. The zero value is one mono asset of 1,024 frames and
// one zone over MIDI 48 to 72 at every velocity.
type Options struct {
	ID       string // directory under packs/; default "synthetic"
	Assets   int    // number of assets; default 1
	Frames   int    // frames in each asset; default 1,024
	Channels int    // 1 or 2; default 1
	Zones    []Zone // zone i plays asset i modulo Assets
}

// Pack describes a pack that Write made.
type Pack struct {
	Manifest string // path from the project directory, such as packs/synthetic/manifest.json
	SHA256   string // hex pin of the manifest
	PCMBytes int64  // decoded float32 PCM of the assets that a zone plays
}

// Sampler spells a sampler declaration that pins the pack. Its root is c4.
func (p Pack) Sampler(name string, voices int) string {
	return fmt.Sprintf("sampler %s {\n  pack = %q\n  sha256 = %q\n  root = c4\n  voices = %d\n}\n", name, p.Manifest, p.SHA256, voices)
}

type encoded struct {
	gz            []byte
	gzSHA, wavSHA string
	wavBytes      int64
}

var (
	cacheMu sync.Mutex
	cache   = map[[2]int]encoded{}
)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// asset encodes one WAV once per size, so a test that writes several packs of
// one size builds the WAV a single time.
func asset(t testing.TB, frames, channels int) encoded {
	t.Helper()
	cacheMu.Lock()
	defer cacheMu.Unlock()
	key := [2]int{frames, channels}
	if cached, ok := cache[key]; ok {
		return cached
	}
	wav := testwav.Bytes(48000, channels, 16, frames, 1)
	var packed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&packed, gzip.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write(wav); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	cache[key] = encoded{gz: packed.Bytes(), gzSHA: digest(packed.Bytes()), wavSHA: digest(wav), wavBytes: int64(len(wav))}
	return cache[key]
}

// Write writes the pack under dir/packs/<ID>/ and returns where it is.
func Write(t testing.TB, dir string, o Options) Pack {
	t.Helper()
	if o.ID == "" {
		o.ID = "synthetic"
	}
	if o.Assets == 0 {
		o.Assets = 1
	}
	if o.Frames == 0 {
		o.Frames = 1024
	}
	if o.Channels == 0 {
		o.Channels = 1
	}
	if len(o.Zones) == 0 {
		o.Zones = []Zone{{KeyLow: 48, KeyHigh: 72}}
	}
	data := asset(t, o.Frames, o.Channels)
	root := filepath.Join(dir, "packs", o.ID)
	if err := os.MkdirAll(filepath.Join(root, "samples"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := instrumentpack.Manifest{Format: instrumentpack.Format, ID: o.ID, Description: "synthetic test pack", Config: sample.DefaultInstrumentConfig()}
	for i := 0; i < o.Assets; i++ {
		id := fmt.Sprintf("take%d", i)
		path := "samples/" + id + ".wav.gz"
		if err := os.WriteFile(filepath.Join(root, path), data.gz, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest.Assets = append(manifest.Assets, instrumentpack.Asset{
			ID: id, Path: path, SHA256: data.gzSHA, Bytes: int64(len(data.gz)),
			WAVSHA256: data.wavSHA, WAVBytes: data.wavBytes, Frames: o.Frames, Rate: 48000, Channels: o.Channels,
			SourceURL: "https://example.org/" + id + ".wav", SourceSHA256: data.wavSHA,
			License: "CC0-1.0", LicenseURL: "https://creativecommons.org/publicdomain/zero/1.0/",
		})
	}
	for i, z := range o.Zones {
		low, high := z.VelocityLow, z.VelocityHigh
		if low == 0 {
			low = 1
		}
		if high == 0 {
			high = 127
		}
		manifest.Zones = append(manifest.Zones, instrumentpack.Zone{
			Asset: manifest.Assets[i%o.Assets].ID, Root: (z.KeyLow + z.KeyHigh) / 2, KeyLow: z.KeyLow, KeyHigh: z.KeyHigh,
			VelocityLow: low, VelocityHigh: high, Layer: (low + high) / 2, Group: i, Count: 1, Gain: 1,
		})
	}
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), encodedManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	return Pack{
		Manifest: "packs/" + o.ID + "/manifest.json", SHA256: digest(encodedManifest),
		PCMBytes: int64(min(o.Assets, len(o.Zones))) * int64(o.Frames) * int64(o.Channels) * 4,
	}
}
