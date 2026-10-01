// Package audioasset verifies immutable audio files without decoding samples.
package audioasset

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
)

type WAVHeader struct {
	RateHz, Channels, BitDepth int
	Frames                     int64
	DataOffset                 int64  // first PCM byte, validated against RIFF size
	Encoding                   string // pcm or float
}

// SHA256 hashes exact file bytes, including headers and ancillary chunks.
func SHA256(r io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, r); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// ReadWAVHeader scans bounded RIFF chunks and validates their dimensions. It
// never reads or allocates the audio payload. RF64 and compressed WAV are later work.
func ReadWAVHeader(r io.ReadSeeker) (WAVHeader, error) {
	var h WAVHeader
	invalid := func(reason string) (WAVHeader, error) {
		return h, fmt.Errorf("expected valid PCM 16/24/32 or float32 mono/stereo RIFF WAV; actual %s", reason)
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return h, err
	}
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return h, err
	}
	var riff [12]byte
	if _, err = io.ReadFull(r, riff[:]); err != nil {
		return invalid("truncated RIFF header")
	}
	if string(riff[:4]) != "RIFF" || string(riff[8:]) != "WAVE" {
		return invalid("non-WAV header")
	}
	limit := int64(binary.LittleEndian.Uint32(riff[4:8])) + 8
	if limit != size || limit < 12 {
		return invalid(fmt.Sprintf("RIFF size %d, file size %d", limit, size))
	}
	fmtSeen, dataSeen := false, false
	var dataBytes int64
	var alignment int
	for offset := int64(12); offset < limit; {
		if limit-offset < 8 {
			return invalid("truncated chunk header")
		}
		if _, err = r.Seek(offset, io.SeekStart); err != nil {
			return h, err
		}
		var chunk [8]byte
		if _, err = io.ReadFull(r, chunk[:]); err != nil {
			return h, err
		}
		length := int64(binary.LittleEndian.Uint32(chunk[4:]))
		start := offset + 8
		if length > limit-start {
			return invalid("chunk exceeds RIFF size")
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if fmtSeen || length < 16 {
				return invalid("duplicate or short fmt chunk")
			}
			fmtSeen = true
			var format [16]byte
			if _, err = io.ReadFull(r, format[:]); err != nil {
				return h, err
			}
			code := binary.LittleEndian.Uint16(format[:2])
			h.Channels = int(binary.LittleEndian.Uint16(format[2:4]))
			h.RateHz = int(binary.LittleEndian.Uint32(format[4:8]))
			alignment = int(binary.LittleEndian.Uint16(format[12:14]))
			h.BitDepth = int(binary.LittleEndian.Uint16(format[14:]))
			if code == 1 && (h.BitDepth == 16 || h.BitDepth == 24 || h.BitDepth == 32) {
				h.Encoding = "pcm"
			} else if code == 3 && h.BitDepth == 32 {
				h.Encoding = "float"
			} else {
				return invalid(fmt.Sprintf("encoding %d, %d bits", code, h.BitDepth))
			}
			if h.Channels < 1 || h.Channels > 2 || h.RateHz < 8000 || h.RateHz > 384000 || alignment != h.Channels*h.BitDepth/8 || int64(binary.LittleEndian.Uint32(format[8:12])) != int64(h.RateHz)*int64(alignment) {
				return invalid("inconsistent rate, channels, or block alignment")
			}
		case "data":
			if dataSeen {
				return invalid("duplicate data chunk")
			}
			dataSeen = true
			dataBytes = length
			h.DataOffset = start
		}
		offset = start + length + (length & 1)
		if offset > limit {
			return invalid("missing chunk padding")
		}
	}
	if !fmtSeen || !dataSeen || dataBytes == 0 || alignment == 0 || dataBytes%int64(alignment) != 0 {
		return invalid("missing fmt/data or incomplete source frame")
	}
	h.Frames = dataBytes / int64(alignment)
	return h, nil
}
