// cicada-audio-encode writes immutable audio assets before playback.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/host/audioencoding"
)

type descriptor struct {
	Format       string              `json:"format"`
	Tier         string              `json:"tier"`
	SourceSHA256 string              `json:"source_sha256"`
	Asset        audioencoding.Asset `json:"asset"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	f := flag.NewFlagSet("cicada-audio-encode", flag.ContinueOnError)
	in := f.String("in", "", "source PCM/float32 WAV")
	dest := f.String("out", "", "new output filename stem (codec extension is added)")
	tier := f.String("tier", "lossless", "lossless or hq16")
	encoder := f.String("ffmpeg", "ffmpeg", "offline FLAC encoder executable")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *in == "" || *dest == "" || (*tier != "lossless" && *tier != "hq16") {
		return fmt.Errorf("usage: cicada-audio-encode -in source.wav -out asset -tier lossless|hq16")
	}
	src, err := readBounded(*in)
	if err != nil {
		return err
	}
	pcm, rate, err := audioencoding.DecodeWAV(src)
	if err != nil {
		return err
	}
	a := audioencoding.Asset{Encoding: "flac", Rate: rate, Channels: len(pcm), Frames: len(pcm[0]), Scale: 1, PCMHash: audioencoding.PCMHash(pcm)}
	encoded := []byte(nil)
	dir, err := os.MkdirTemp("", "cicada-audio-encode-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	wave := src
	sampleFormat := "s32"
	if *tier == "hq16" {
		var peak float32
		for _, c := range pcm {
			for _, x := range c {
				if v := float32(math.Abs(float64(x))); v > peak {
					peak = v
				}
			}
		}
		if peak > 0 {
			a.Scale = float32(math.Exp2(math.Ceil(math.Log2(float64(peak)))))
		}
		if !audioencoding.PowerOfTwo(a.Scale) {
			return fmt.Errorf("source amplitude cannot be scaled")
		}
		wave, pcm = pcm16WAV(pcm, rate, a.Scale)
		a.PCMHash = audioencoding.PCMHash(pcm)
		sampleFormat = "s16"
	}
	source := filepath.Join(dir, "source.wav")
	target := filepath.Join(dir, "encoded.flac")
	if err := os.WriteFile(source, wave, 0600); err != nil {
		return err
	}
	cmd := exec.Command(*encoder, "-v", "error", "-nostdin", "-threads", "1", "-y", "-i", source, "-map_metadata", "-1", "-c:a", "flac", "-compression_level", "8", "-sample_fmt", sampleFormat, target)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if *tier == "hq16" {
			return fmt.Errorf("FLAC encoding failed: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
	} else {
		encoded, err = readBounded(target)
		if err != nil {
			return err
		}
		a.Bytes = int64(len(encoded))
		a.SHA256 = digest(encoded)
		if _, err = audioencoding.Decode(a, encoded); err != nil {
			if *tier == "hq16" {
				return fmt.Errorf("encoded PCM verification failed: %w", err)
			}
			encoded = nil
		}
	}
	// An exact float32 reconstruction can need more precision than integer FLAC.
	// Missing encoders also retain a useful exact tier rather than breaking export.
	if len(encoded) == 0 {
		var b bytes.Buffer
		g, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
		if _, err := g.Write(src); err != nil {
			return err
		}
		if err := g.Close(); err != nil {
			return err
		}
		encoded = b.Bytes()
		a.Encoding = "wav-gzip"
		a.DecodedBytes = int64(len(src))
		a.Bytes = int64(len(encoded))
		a.SHA256 = digest(encoded)
		if _, err := audioencoding.Decode(a, encoded); err != nil {
			return err
		}
	}
	outputPath := *dest + ".flac"
	if a.Encoding == "wav-gzip" {
		outputPath = *dest + ".wav.gz"
	}
	d := descriptor{Format: "cicada.audio-encoding/1", Tier: *tier, SourceSHA256: digest(src), Asset: a}
	metadata, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	metadata = append(metadata, '\n')
	// Exclusive creation preserves existing user assets and descriptors.
	if err := writeNew(outputPath, encoded); err != nil {
		return err
	}
	if err := writeNew(outputPath+".json", metadata); err != nil {
		os.Remove(outputPath)
		return err
	}
	_, err = fmt.Fprintf(out, "%s: %s, %d bytes, PCM sha256 %s\n", filepath.Base(outputPath), a.Encoding, a.Bytes, a.PCMHash)
	return err
}
func readBounded(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, audioencoding.MaxPCMBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > audioencoding.MaxPCMBytes {
		return nil, fmt.Errorf("audio exceeds limit")
	}
	return b, nil
}
func writeNew(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	closeErr := f.Close()
	if e != nil {
		os.Remove(path)
		return e
	}
	if closeErr != nil {
		os.Remove(path)
		return closeErr
	}
	return nil
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func pcm16WAV(pcm [][]float32, rate int, scale float32) ([]byte, [][]float32) {
	ch := len(pcm)
	frames := len(pcm[0])
	b := make([]byte, 44+frames*ch*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], uint16(ch))
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*ch*2))
	binary.LittleEndian.PutUint16(b[32:], uint16(ch*2))
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(len(b)-44))
	out := make([][]float32, ch)
	for i := range out {
		out[i] = make([]float32, frames)
	}
	o := 44
	for i := 0; i < frames; i++ {
		for c := 0; c < ch; c++ {
			v := math.Round(float64(pcm[c][i]) / float64(scale) * 32768)
			v = math.Max(-32768, math.Min(32767, v))
			q := int16(v)
			binary.LittleEndian.PutUint16(b[o:], uint16(q))
			o += 2
			out[c][i] = float32(q) / 32768 * scale
		}
	}
	return b, out
}
