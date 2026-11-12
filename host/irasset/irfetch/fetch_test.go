package irfetch

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"testing"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/host/irasset"
)

// testWAV is irasset's test fixture: a two-frame stereo 16-bit WAV and its
// catalog entry.
func testWAV() ([]byte, irasset.Entry) {
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
	entry := irasset.Entry{ID: "test", Name: "Test response", Category: "room", URL: "https://example.test/room.wav",
		SourceURL: "https://example.test", License: "MIT", LicenseURL: "https://example.test/LICENSE",
		Attribution: "Test fixture", SHA256: hash, Bytes: int64(len(data)), RateHz: 48000, Channels: 2, BitDepth: 16, Frames: 2}
	return data, entry
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
