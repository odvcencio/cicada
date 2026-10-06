package irasset

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"os"
	"testing"

	"m31labs.dev/cicada/audioasset"
)

func testWAV() ([]byte, Entry) {
	data := make([]byte, 44+8)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 2)
	binary.LittleEndian.PutUint32(data[24:], 48000)
	binary.LittleEndian.PutUint32(data[28:], 48000*4)
	binary.LittleEndian.PutUint16(data[32:], 4)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], 8)
	binary.LittleEndian.PutUint16(data[44:], 16384)
	binary.LittleEndian.PutUint16(data[46:], 49152)
	binary.LittleEndian.PutUint16(data[48:], 8192)
	binary.LittleEndian.PutUint16(data[50:], 0)
	hash, _ := audioasset.SHA256(bytes.NewReader(data))
	entry := Entry{ID: "test", Name: "Test response", Category: "room", URL: "https://example.test/room.wav",
		SourceURL: "https://example.test", License: "MIT", LicenseURL: "https://example.test/LICENSE",
		Attribution: "Test fixture", SHA256: hash, Bytes: int64(len(data)), RateHz: 48000, Channels: 2, BitDepth: 16, Frames: 2}
	return data, entry
}

func TestDecodeVerifiedPCMAndRejectMismatch(t *testing.T) {
	data, entry := testWAV()
	impulse, err := Decode(bytes.NewReader(data), entry, 48000)
	if err != nil {
		t.Fatal(err)
	}
	if impulse.RateHz != 48000 || len(impulse.Left) != 2 || impulse.Left[0] != .5 || impulse.Right[0] != -.5 || impulse.Left[1] != .25 {
		t.Fatalf("decoded impulse %+v", impulse)
	}
	if _, err := impulse.Convolver(128); err != nil {
		t.Fatal(err)
	}
	data[44] ^= 1
	if _, err := Decode(bytes.NewReader(data), entry, 48000); err == nil {
		t.Fatal("corrupt PCM accepted")
	}
	data[44] ^= 1
	entry.Frames++
	if _, err := Decode(bytes.NewReader(data), entry, 48000); err == nil {
		t.Fatal("wrong dimensions accepted")
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchBoundsAndChecksum(t *testing.T) {
	data, entry := testWAV()
	client := &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: -1}, nil
	})}
	if fetched, err := Fetch(context.Background(), client, entry); err != nil || !bytes.Equal(fetched, data) {
		t.Fatalf("verified fetch: %v", err)
	}
	data = append(data, 1)
	if _, err := Fetch(context.Background(), client, entry); err == nil {
		t.Fatal("oversized transfer accepted")
	}
	data = data[:len(data)-1]
	data[44] ^= 1
	if _, err := Fetch(context.Background(), client, entry); err == nil {
		t.Fatal("corrupt transfer accepted")
	}
}

func TestPackManifest(t *testing.T) {
	f, err := os.Open("../../assets/ir/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ReadManifest(f)
	if err != nil || len(m.Assets) != 4 {
		t.Fatalf("pack manifest: %v", err)
	}
	for _, entry := range m.Assets {
		entry.License = "personal-use-only"
		if err := entry.Validate(); err == nil {
			t.Fatal("unsupported license accepted")
		}
	}
}

func TestResamplePassbandAndAliasRejection(t *testing.T) {
	const sourceRate = 96000
	const targetRate = 48000
	const frames = 9600
	for _, frequency := range []float64{1000, 30000} {
		input := make([]float32, frames)
		for i := range input {
			input[i] = float32(math.Sin(2 * math.Pi * frequency * float64(i) / sourceRate))
		}
		output := resample(input, sourceRate, targetRate, frames/2)
		var energy float64
		for _, x := range output[200 : len(output)-200] {
			energy += float64(x) * float64(x)
		}
		rms := math.Sqrt(energy / float64(len(output)-400))
		if frequency == 1000 && math.Abs(rms-math.Sqrt(2)) > .01 {
			t.Fatalf("passband RMS %g; expected sqrt(2) after coefficient scaling", rms)
		}
		if frequency == 30000 && rms > 1e-4 {
			t.Fatalf("alias RMS %g, exceeds -80 dBFS", rms)
		}
		t.Logf("resample %.0f Hz RMS %.2f dBFS", frequency, 20*math.Log10(rms))
	}
}

func TestConditionDCLinkedEnergyAndOwnership(t *testing.T) {
	input := Impulse{Left: make([]float32, 48000), Right: make([]float32, 48000), RateHz: 48000}
	for n := range input.Left {
		input.Left[n], input.Right[n] = .1, -.05
	}
	output, err := input.Condition(20, 1)
	if err != nil {
		t.Fatal(err)
	}
	var sum, energy float64
	for n, x := range output.Left {
		sum += float64(x)
		energy += float64(x) * float64(x)
		if output.Right[n] != -.5*x {
			t.Fatal("linked conditioning changed the stereo ratio")
		}
	}
	if math.Abs(sum) > .02 || energy > 1.000001 || input.Left[0] != .1 {
		t.Fatalf("conditioned DC gain %g energy %g or changed source", sum, energy)
	}
	plain, err := input.Condition(0, 0)
	if err != nil || len(plain.Left) != len(input.Left) || plain.Left[0] != input.Left[0] {
		t.Fatal("disabled conditioning changed PCM")
	}
}
