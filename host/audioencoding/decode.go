// Package audioencoding decodes bounded, pinned audio outside the render path.
package audioencoding

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"

	"github.com/mewkiz/flac"

	"m31labs.dev/cicada/audioasset"
)

const MaxPCMBytes = 256 << 20

// Asset describes the encoded stream and its canonical float32 reconstruction.
// Scale is a power of two applied after FLAC decoding. It allows a PCM16 tier
// to preserve quiet samples without depending on browser gain arithmetic.
type Asset struct {
	Encoding     string  `json:"encoding"`
	DecodedBytes int64   `json:"decoded_bytes,omitempty"`
	SHA256       string  `json:"sha256"`
	Bytes        int64   `json:"bytes"`
	PCMHash      string  `json:"pcm_sha256"`
	Frames       int     `json:"frames"`
	Rate         int     `json:"rate"`
	Channels     int     `json:"channels"`
	Scale        float32 `json:"scale"`
}

func (a Asset) Validate() error {
	if (a.Encoding != "flac" && a.Encoding != "wav-gzip") || (a.Encoding == "wav-gzip" && (a.DecodedBytes < 44 || a.DecodedBytes > MaxPCMBytes || a.Scale != 1)) || !validHash(a.SHA256) || !validHash(a.PCMHash) || a.Bytes < 1 || a.Bytes > MaxPCMBytes || a.Frames < 1 || a.Frames > 8<<20 || a.Rate < 8000 || a.Rate > 192000 || a.Channels < 1 || a.Channels > 2 || int64(a.Frames)*int64(a.Channels)*4 > MaxPCMBytes || !PowerOfTwo(a.Scale) {
		return fmt.Errorf("invalid encoded audio bounds, hashes or scale")
	}
	return nil
}

// PowerOfTwo accepts positive, normal float32 powers of two only.
func PowerOfTwo(x float32) bool {
	b := math.Float32bits(x)
	return b&0x807fffff == 0 && b&0x7f800000 != 0 && b&0x7f800000 != 0x7f800000
}

// DecodeFLAC checks dimensions before allocating resident PCM. Frame checksums,
// exact frame count and a hash of scaled planar float32 samples complete admission.
func DecodeFLAC(a Asset, encoded []byte) ([][]float32, error) {
	if a.Encoding != "flac" {
		return nil, fmt.Errorf("expected FLAC encoding")
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if int64(len(encoded)) != a.Bytes || digest(encoded) != a.SHA256 {
		return nil, fmt.Errorf("encoded audio hash/size mismatch")
	}
	stream, err := flac.New(bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	info := stream.Info
	if info.SampleRate != uint32(a.Rate) || int(info.NChannels) != a.Channels || info.NSamples != uint64(a.Frames) || info.BitsPerSample < 4 || info.BitsPerSample > 32 {
		return nil, fmt.Errorf("FLAC dimensions mismatch")
	}
	pcm := make([][]float32, a.Channels)
	for i := range pcm {
		pcm[i] = make([]float32, a.Frames)
	}
	denominator := float32(math.Ldexp(1, int(info.BitsPerSample)-1))
	offset := 0
	for {
		frame, err := stream.ParseNext()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(frame.Subframes) != a.Channels || len(frame.Subframes[0].Samples) > a.Frames-offset {
			return nil, fmt.Errorf("FLAC frame exceeds declared dimensions")
		}
		n := len(frame.Subframes[0].Samples)
		if int(frame.BitsPerSample) != int(info.BitsPerSample) || frame.SampleRate != info.SampleRate {
			return nil, fmt.Errorf("FLAC frame format changed")
		}
		for ch, sub := range frame.Subframes {
			if len(sub.Samples) != n {
				return nil, fmt.Errorf("FLAC channel length mismatch")
			}
			for i, v := range sub.Samples {
				x := float32(v) / denominator * a.Scale
				if math.IsInf(float64(x), 0) || math.IsNaN(float64(x)) {
					return nil, fmt.Errorf("nonfinite reconstructed PCM")
				}
				pcm[ch][offset+i] = x
			}
		}
		offset += n
	}
	if offset != a.Frames || PCMHash(pcm) != a.PCMHash {
		return nil, fmt.Errorf("decoded PCM hash/size mismatch")
	}
	return pcm, nil
}

// PCMHash pins channel-major IEEE float32 values in little-endian byte order.
func PCMHash(pcm [][]float32) string {
	h := sha256.New()
	var b [4]byte
	for _, ch := range pcm {
		for _, v := range ch {
			binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
			h.Write(b[:])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == hex.EncodeToString(b)
}
func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// Decode admits either tier without a codec dependency in the audio kernel.
func Decode(a Asset, encoded []byte) ([][]float32, error) {
	if a.Encoding == "flac" {
		return DecodeFLAC(a, encoded)
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if int64(len(encoded)) != a.Bytes || digest(encoded) != a.SHA256 {
		return nil, fmt.Errorf("encoded audio hash/size mismatch")
	}
	reader := bytes.NewReader(encoded)
	g, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	g.Multistream(false)
	wav, err := io.ReadAll(io.LimitReader(g, a.DecodedBytes+1))
	g.Close()
	if err != nil {
		return nil, err
	}
	if reader.Len() != 0 {
		return nil, fmt.Errorf("trailing gzip data")
	}
	if int64(len(wav)) != a.DecodedBytes {
		return nil, fmt.Errorf("decoded WAV size mismatch")
	}
	pcm, rate, err := DecodeWAV(wav)
	if err != nil {
		return nil, err
	}
	if rate != a.Rate || len(pcm) != a.Channels || len(pcm[0]) != a.Frames || PCMHash(pcm) != a.PCMHash {
		return nil, fmt.Errorf("decoded PCM hash/dimensions mismatch")
	}
	return pcm, nil
}

// DecodeWAV supplies canonical source PCM to offline encoders. It allocates only
// after the RIFF validator has checked the complete file and declared bounds.
func DecodeWAV(wav []byte) ([][]float32, int, error) {
	if len(wav) > MaxPCMBytes {
		return nil, 0, fmt.Errorf("WAV exceeds limit")
	}
	h, err := audioasset.ReadWAVHeader(bytes.NewReader(wav))
	if err != nil {
		return nil, 0, err
	}
	if h.Frames < 1 || h.Frames > 8<<20 || h.Channels < 1 || h.Channels > 2 || h.RateHz < 8000 || h.RateHz > 192000 || h.Frames*int64(h.Channels)*4 > MaxPCMBytes {
		return nil, 0, fmt.Errorf("WAV dimensions exceed limit")
	}
	pcm := make([][]float32, h.Channels)
	for i := range pcm {
		pcm[i] = make([]float32, int(h.Frames))
	}
	w := h.BitDepth / 8
	o := int(h.DataOffset)
	for frame := 0; frame < int(h.Frames); frame++ {
		for ch := 0; ch < h.Channels; ch++ {
			b := wav[o : o+w]
			o += w
			var x float32
			switch {
			case h.Encoding == "float":
				x = math.Float32frombits(binary.LittleEndian.Uint32(b))
			case w == 2:
				x = float32(int16(binary.LittleEndian.Uint16(b))) / 32768
			case w == 3:
				v := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
				x = float32(v<<8>>8) / 8388608
			case w == 4:
				x = float32(float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648)
			default:
				return nil, 0, fmt.Errorf("unsupported WAV precision")
			}
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, 0, fmt.Errorf("nonfinite source PCM")
			}
			pcm[ch][frame] = x
		}
	}
	return pcm, h.RateHz, nil
}
