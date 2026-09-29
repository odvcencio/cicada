package acid

import "testing"

func TestOscillatorBankImageRoundTrip(t *testing.T) {
	want := bankForSampleRate(48_000)
	image, err := ExportOscillatorBankImage(48_000)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeOscillatorBankImage(image)
	if err != nil {
		t.Fatal(err)
	}
	for bin := range want.tables {
		if *want.tables[bin] != *got.tables[bin] {
			t.Fatalf("oscillator table %d changed in the image", bin)
		}
		for prior := 0; prior < bin; prior++ {
			if (want.tables[prior] == want.tables[bin]) != (got.tables[prior] == got.tables[bin]) {
				t.Fatalf("oscillator table sharing changed between bins %d and %d", prior, bin)
			}
		}
	}
}

func TestOscillatorBankImageRejectsMalformedData(t *testing.T) {
	for _, image := range [][]byte{nil, {0, 0, 0, 0}, {1, 0, 0, 0}} {
		if _, err := decodeOscillatorBankImage(image); err == nil {
			t.Fatalf("accepted malformed oscillator image %v", image)
		}
	}
	image, err := ExportOscillatorBankImage(48_000)
	if err != nil {
		t.Fatal(err)
	}
	image[oscillatorBankImageHeaderSize] = 0xff
	image[oscillatorBankImageHeaderSize+1] = 0xff
	if _, err := decodeOscillatorBankImage(image); err == nil {
		t.Fatal("accepted an out-of-range table reference")
	}
}
