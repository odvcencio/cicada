package project

import (
	"bytes"
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

const chorusScore = `cicada 2
fx wide chorus { voices = 3 rate = 0.6Hz depth = 3ms spread = 1 mix = 0.5 }
track lead acid { insert = wide }
pattern p { 1 . 3 . }
scene main { lead = p }
song { main }
`

func TestChorusProjectImageRoundTrip(t *testing.T) {
	score, ds := notation.Parse([]byte(chorusScore))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	p, err = DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("chorus")) {
		t.Fatalf("chorus lost: %s", source)
	}
	score, ds = notation.Parse(source)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if _, ds = FromScore(score); len(ds) != 0 {
		t.Fatal(ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(image[8:12])&kernelimage.ChorusFlag == 0 {
		t.Fatal("missing modular chorus flag")
	}
	got, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Track[0].InsertChain) != 1 || got.Track[0].InsertChain[0].Chorus == nil || *got.Track[0].InsertChain[0].Chorus != *cfg.Track[0].InsertChain[0].Chorus {
		t.Fatal("chorus parameters changed")
	}
	e, err := engine.New(got)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	bad := append([]byte(nil), image...)
	binary.LittleEndian.PutUint32(bad[8:12], binary.LittleEndian.Uint32(bad[8:12])&^kernelimage.ChorusFlag)
	if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
		t.Fatal("accepted unadvertised chorus")
	}
	bad = append([]byte(nil), image...)
	bad[len(bad)-35] = 1
	if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
		t.Fatal("accepted invalid chorus track reference")
	}
}
