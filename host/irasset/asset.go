// Package irasset fetches and prepares licensed impulse responses outside the
// audio callback. It verifies exact WAV bytes before allocating decoded PCM.
package irasset

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/kernel/fx/convolution"
)

const MaxBytes = 16 << 20

// Entry pins original audio, dimensions and redistribution terms. The WAV
// checksum covers headers and ancillary chunks, before any resampling.
type Entry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	URL         string `json:"url"`
	SourceURL   string `json:"source_url"`
	License     string `json:"license"`
	LicenseURL  string `json:"license_url"`
	Attribution string `json:"attribution"`
	SHA256      string `json:"sha256"`
	Bytes       int64  `json:"bytes"`
	RateHz      int    `json:"rate_hz"`
	Channels    int    `json:"channels"`
	BitDepth    int    `json:"bit_depth"`
	Frames      int64  `json:"frames"`
}

type Manifest struct {
	Version int     `json:"version"`
	Assets  []Entry `json:"assets"`
}

func httpsURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == ""
}

func (e Entry) Validate() error {
	if e.ID == "" || len(e.ID) > 64 {
		return errors.New("invalid impulse asset ID")
	}
	for _, c := range e.ID {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return errors.New("invalid impulse asset ID")
		}
	}
	checksum, err := hex.DecodeString(e.SHA256)
	if err != nil || len(checksum) != 32 || e.SHA256 != strings.ToLower(e.SHA256) ||
		!httpsURL(e.URL) || !httpsURL(e.SourceURL) || !httpsURL(e.LicenseURL) ||
		(e.License != "MIT" && e.License != "CC0-1.0" && e.License != "CC-BY-4.0") || e.Attribution == "" ||
		e.Name == "" || e.Category == "" || e.Bytes < 44 || e.Bytes > MaxBytes ||
		e.RateHz < 8000 || e.RateHz > 192000 || e.Channels < 1 || e.Channels > 2 ||
		(e.BitDepth != 16 && e.BitDepth != 24 && e.BitDepth != 32) || e.Frames < 1 ||
		e.Frames > convolution.MaxFrames || e.Frames > int64(e.RateHz)*12 {
		return errors.New("invalid impulse asset metadata, checksum, bounds, or redistribution license")
	}
	return nil
}

func ReadManifest(r io.Reader) (Manifest, error) {
	var m Manifest
	d := json.NewDecoder(io.LimitReader(r, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	if m.Version != 1 || len(m.Assets) == 0 || len(m.Assets) > 128 {
		return Manifest{}, errors.New("unsupported or empty impulse manifest")
	}
	ids := make(map[string]bool, len(m.Assets))
	for _, e := range m.Assets {
		if err := e.Validate(); err != nil {
			return Manifest{}, fmt.Errorf("impulse %s: %w", e.ID, err)
		}
		if ids[e.ID] {
			return Manifest{}, errors.New("duplicate impulse asset ID")
		}
		ids[e.ID] = true
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("trailing impulse manifest content")
	}
	return m, nil
}

// Fetch bounds the transfer and verifies its checksum before returning any
// audio. The caller controls cancellation and HTTP timeouts through context
// and client. Call it on a worker or host thread, never an audio owner.
func Fetch(ctx context.Context, client *http.Client, e Entry) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("impulse fetch requires an HTTP client")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("impulse fetch returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > e.Bytes {
		return nil, errors.New("impulse transfer exceeds declared byte count")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, e.Bytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != e.Bytes {
		return nil, errors.New("impulse transfer differs from declared byte count")
	}
	checksum, err := audioasset.SHA256(bytes.NewReader(data))
	if err != nil || checksum != e.SHA256 {
		return nil, errors.New("impulse transfer checksum mismatch")
	}
	return data, nil
}

// Impulse contains owned, verified PCM at the requested sample rate. It is
// prepared storage, independent of the source reader and network response.
type Impulse struct {
	Left, Right []float32
	RateHz      int
}

func (i Impulse) Convolver(partitionFrames int) (*convolution.Reverb, error) {
	return convolution.New(i.RateHz, i.Left, i.Right, partitionFrames)
}

// Condition returns owned PCM with an optional first-order highpass and a
// linked stereo energy ceiling. energyLimit is a maximum channel L2 norm;
// zero disables attenuation. It never boosts a quiet response. This is host
// preparation, not a realtime parameter or a guarantee against clipping.
// A highpass adds eight time constants of decay so it does not discard its
// own filter tail. Decode preserves the original response unless this method
// is explicitly selected.
func (i Impulse) Condition(highpassHz, energyLimit float64) (Impulse, error) {
	if i.RateHz < 8000 || i.RateHz > 192000 || len(i.Left) == 0 ||
		(len(i.Right) != 0 && len(i.Right) != len(i.Left)) ||
		math.IsNaN(highpassHz) || math.IsInf(highpassHz, 0) || highpassHz < 0 || highpassHz > 1000 ||
		math.IsNaN(energyLimit) || math.IsInf(energyLimit, 0) || energyLimit < 0 {
		return Impulse{}, errors.New("invalid impulse conditioning controls or PCM")
	}
	extra := 0
	if highpassHz > 0 {
		extra = int(math.Ceil(8 * float64(i.RateHz) / (2 * math.Pi * highpassHz)))
	}
	frames := len(i.Left) + extra
	if frames > convolution.MaxFrames || frames > i.RateHz*12 {
		return Impulse{}, errors.New("conditioned impulse exceeds resident frame bound")
	}
	result := Impulse{Left: make([]float32, frames), RateHz: i.RateHz}
	if len(i.Right) != 0 {
		result.Right = make([]float32, frames)
	}
	var maximumEnergy float64
	for c, source := range [][]float32{i.Left, i.Right} {
		if len(source) == 0 {
			continue
		}
		output := result.Left
		if c == 1 {
			output = result.Right
		}
		alpha := math.Exp(-2 * math.Pi * highpassHz / float64(i.RateHz))
		var previousInput, previousOutput, energy float64
		for n := range output {
			var x float64
			if n < len(source) {
				x = float64(source[n])
				if math.IsNaN(x) || math.IsInf(x, 0) {
					return Impulse{}, errors.New("impulse conditioning contains non-finite PCM")
				}
			}
			y := x
			if highpassHz > 0 {
				y = alpha * (previousOutput + x - previousInput)
			}
			if math.Abs(y) > math.MaxFloat32 {
				return Impulse{}, errors.New("impulse conditioning exceeds float32 coefficient range")
			}
			previousInput, previousOutput = x, y
			output[n] = float32(y)
			energy += y * y
		}
		maximumEnergy = math.Max(maximumEnergy, energy)
	}
	if energyLimit > 0 && maximumEnergy > energyLimit*energyLimit {
		gain := float32(energyLimit / math.Sqrt(maximumEnergy))
		for _, channel := range [][]float32{result.Left, result.Right} {
			for n := range channel {
				channel[n] *= gain
			}
		}
	}
	return result, nil
}

// Decode verifies an exact source WAV, decodes finite PCM, and optionally
// applies a Blackman-windowed sinc resampler. Resampling scales coefficients
// by sourceRate/targetRate to preserve the impulse's transfer-function gain.
// The radius expands when downsampling to maintain a fixed output bandwidth.
func Decode(r io.ReadSeeker, e Entry, targetRate int) (Impulse, error) {
	if err := e.Validate(); err != nil {
		return Impulse{}, err
	}
	if targetRate < 8000 || targetRate > 192000 {
		return Impulse{}, errors.New("invalid impulse target sample rate")
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil || size != e.Bytes {
		return Impulse{}, errors.New("impulse file differs from declared byte count")
	}
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return Impulse{}, err
	}
	checksum, err := audioasset.SHA256(r)
	if err != nil || checksum != e.SHA256 {
		return Impulse{}, errors.New("impulse file checksum mismatch")
	}
	h, err := audioasset.ReadWAVHeader(r)
	if err != nil {
		return Impulse{}, err
	}
	if h.Frames != e.Frames || h.RateHz != e.RateHz || h.Channels != e.Channels || h.BitDepth != e.BitDepth {
		return Impulse{}, errors.New("impulse WAV dimensions differ from the manifest")
	}
	outputFrames := (h.Frames*int64(targetRate) + int64(h.RateHz) - 1) / int64(h.RateHz)
	if outputFrames > convolution.MaxFrames || outputFrames > int64(targetRate)*12 {
		return Impulse{}, errors.New("resampled impulse exceeds resident frame bound")
	}
	result := Impulse{Left: make([]float32, h.Frames), RateHz: targetRate}
	if h.Channels == 2 {
		result.Right = make([]float32, h.Frames)
	}
	if _, err = r.Seek(h.DataOffset, io.SeekStart); err != nil {
		return Impulse{}, err
	}
	reader := bufio.NewReaderSize(r, 64<<10)
	bytesPerSample := h.BitDepth / 8
	var frame [8]byte
	for i := range result.Left {
		if _, err = io.ReadFull(reader, frame[:h.Channels*bytesPerSample]); err != nil {
			return Impulse{}, err
		}
		for channel := 0; channel < h.Channels; channel++ {
			data := frame[channel*bytesPerSample:]
			var value float32
			switch {
			case h.Encoding == "float":
				value = math.Float32frombits(binary.LittleEndian.Uint32(data))
			case h.BitDepth == 16:
				value = float32(int16(binary.LittleEndian.Uint16(data))) / 32768
			case h.BitDepth == 24:
				x := int32(data[0]) | int32(data[1])<<8 | int32(data[2])<<16
				value = float32(x<<8>>8) / 8388608
			default:
				value = float32(float64(int32(binary.LittleEndian.Uint32(data))) / 2147483648)
			}
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return Impulse{}, errors.New("impulse WAV contains non-finite PCM")
			}
			if channel == 0 {
				result.Left[i] = value
			} else {
				result.Right[i] = value
			}
		}
	}
	if h.RateHz != targetRate {
		result.Left = resample(result.Left, h.RateHz, targetRate, int(outputFrames))
		if result.Right != nil {
			result.Right = resample(result.Right, h.RateHz, targetRate, int(outputFrames))
		}
	}
	return result, nil
}

func resample(input []float32, sourceRate, targetRate, frames int) []float32 {
	result := make([]float32, frames)
	ratio := float64(sourceRate) / float64(targetRate)
	band := math.Min(1, 1/ratio)
	cutoff := .45 * band
	radius := int(math.Ceil(48 / band))
	for i := range result {
		position := float64(i) * ratio
		center := int(position)
		var sum, weights float64
		for j := center - radius; j <= center+radius; j++ {
			x := float64(j) - position
			if math.Abs(x) > float64(radius) {
				continue
			}
			weight := 2 * cutoff
			if math.Abs(x) > 1e-12 {
				weight = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
			}
			weight *= .42 + .5*math.Cos(math.Pi*x/float64(radius)) + .08*math.Cos(2*math.Pi*x/float64(radius))
			// Zero padding preserves causal edge behaviour; all filter taps
			// contribute to the normalization, including those outside data.
			weights += weight
			if j >= 0 && j < len(input) {
				sum += float64(input[j]) * weight
			}
		}
		result[i] = float32(sum * ratio / weights)
	}
	return result
}
