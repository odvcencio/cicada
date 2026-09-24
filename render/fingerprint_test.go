package render

import (
	"math"
	"reflect"
	"testing"
)

func TestFingerprintToneAndDrift(t *testing.T) {
	const rate = 48_000
	left := make([]float32, rate)
	right := make([]float32, rate)
	for sample := range left {
		left[sample] = float32(.5 * math.Sin(2*math.Pi*1000*float64(sample)/rate))
		right[sample] = left[sample]
	}
	reference, err := FingerprintStereo(left, right, rate)
	if err != nil {
		t.Fatal(err)
	}
	if reference.Samples != rate || len(reference.Frames) != 10 {
		t.Fatalf("wrong fingerprint dimensions: %+v", reference)
	}
	peakBand := 0
	for band, energy := range reference.Frames[0] {
		if energy > reference.Frames[0][peakBand] {
			peakBand = band
		}
	}
	if peakBand < 33 || peakBand > 36 {
		t.Fatalf("1 kHz tone peaked in log band %d", peakBand)
	}
	encoded, err := reference.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFingerprint(encoded)
	if err != nil || !reflect.DeepEqual(reference, decoded) {
		t.Fatalf("fingerprint binary round trip: %v", err)
	}
	if _, err := DecodeFingerprint(encoded[:len(encoded)-1]); err == nil {
		t.Fatal("truncated fingerprint was accepted")
	}
	for sample := range left {
		left[sample] = float32(.5 * math.Sin(2*math.Pi*2000*float64(sample)/rate))
		right[sample] = left[sample]
	}
	changed, err := FingerprintStereo(left, right, rate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompareFingerprint(reference, changed); err == nil {
		t.Fatal("changed first samples were accepted")
	}
	changed.FirstSamplesHash = reference.FirstSamplesHash
	diff, err := FingerprintDrift(reference, changed)
	if err != nil || diff.MaxDB <= 2 {
		t.Fatalf("changed tone did not move spectral bands: %+v %v", diff, err)
	}
	if _, err := CompareFingerprint(reference, changed); err == nil {
		t.Fatal("spectral drift was accepted")
	}
}

func TestFingerprintNinetySixKilohertzWindow(t *testing.T) {
	const rate = 96_000
	audio := make([]float32, rate/10)
	for sample := range audio {
		audio[sample] = float32(.5 * math.Sin(2*math.Pi*1000*float64(sample)/rate))
	}
	fingerprint, err := FingerprintStereo(audio, audio, rate)
	if err != nil || fingerprint.WindowSamples != 9600 || len(fingerprint.Frames) != 1 {
		t.Fatalf("96 kHz spectral window: %+v %v", fingerprint, err)
	}
}
