package project

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
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

func TestChorusModulationTargetsUniqueInsert(t *testing.T) {
	source := chorusScore + "\nlfo motion { rate = 1/4 }\nmod motion -> lead.chorus_mix depth = 0.4\n"
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 1 {
		t.Fatal("chorus route missing")
	}
	render := func(block int) []float32 {
		t.Helper()
		player, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer player.Close()
		player.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		left, right := make([]float32, block), make([]float32, block)
		out := make([]float32, 8192)
		for i := 0; i < len(out); i += block {
			player.Render(left, right)
			copy(out[i:], left)
		}
		return out
	}
	a, b := render(64), render(128)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("host buffer changed frame %d", i)
		}
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ambiguous := strings.Replace(source, "track lead acid { insert = wide }", "fx wide2 chorus {}\ntrack lead acid { insert = wide wide2 }", 1)
	score, ds = notation.Parse([]byte(ambiguous))
	if score != nil && len(ds) == 0 {
		_, ds = FromScore(score)
	}
	if len(ds) == 0 {
		t.Fatal("accepted ambiguous chorus route")
	}
}
