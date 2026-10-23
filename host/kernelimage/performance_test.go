package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestPerformanceImageKeepsEveryGateAndNativePCM(t *testing.T) {
	score, ds := notation.Parse([]byte("track keys piano {}\npattern phrase performance length=3840ticks { note 60 at=17ticks length=703ticks velocity=82; note 64 at=19ticks length=1201ticks velocity=105; }\nscene main { keys=phrase }\nsong { main*2 }\n"))
	pjt, ds := project.FromScore(score)
	if pjt == nil {
		t.Fatal(ds)
	}
	cfg, err := project.CompileEngine(pjt, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Patterns[0].Slots[0].Performance
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 16 {
		t.Fatal("performed gates used a lossy legacy image")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("performance changed in image roundtrip")
	}
	reencoded, err := kernelimage.Encode(decoded)
	if err != nil || !bytes.Equal(data, reencoded) {
		t.Fatal("performance image is not deterministic", err)
	}
	native, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	portable, err := engine.New(decoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []*engine.Engine{native, portable} {
		e.Push(cmd.Command{Op: cmd.OpSelectPattern, Track: 0})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	}
	var a, b, c, d [128]float32
	var m cmd.Message
	for frames := 0; frames < 192000; frames += 128 {
		native.Render(a[:], b[:])
		portable.Render(c[:], d[:])
		if a != c || b != d {
			t.Fatalf("decoded PCM differs at %d", frames)
		}
		for _, e := range []*engine.Engine{native, portable} {
			for e.Poll(&m) {
				if m.Kind == cmd.Fault {
					t.Fatal(m)
				}
			}
		}
	}
	if _, err := kernelimage.PatternCommands(cfg.Patterns[0].Slots[0], &cfg, 0, 0, ^uint32(0)); err == nil {
		t.Fatal("performed data admitted through lossy grid opcodes")
	}
	// The additive table is last: validate every truncation and hostile target,
	// count, length, pitch and gate before exposing a Config to a host.
	table := len(data) - (2 + 12 + 18*len(p.Notes))
	for end := table; end < len(data); end++ {
		if _, err := kernelimage.Decode(data[:end], 48000, 128); err == nil {
			t.Fatalf("truncation %d accepted", end)
		}
	}
	for _, mutation := range []struct {
		offset int
		value  byte
	}{{0, 255}, {2, 1}, {3, 16}, {11, 255}, {12, 255}, {21, 255}, {30, 255}, {31, 0}} {
		bad := bytes.Clone(data)
		bad[table+mutation.offset] = mutation.value
		if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
			t.Fatalf("invalid table field %+v accepted", mutation)
		}
	}
}

func TestPortablePerformanceRefusesUnsustainableStatusRate(t *testing.T) {
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, Patterns: []engine.PatternBank{{}}}
	cfg.Track[0].Kind = engine.VoiceAcid
	p := &seq.Performance{LengthTicks: 3840}
	for i := 0; i < 512; i++ {
		p.Notes = append(p.Notes, seq.PerformedNote{Tick: int64(i/128) * 4, EndTick: int64(i/128)*4 + 1, Note: uint8(i % 128), Velocity: 90})
	}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 55, Performance: p}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal("an ordinary full-bar performance was refused", err)
	}
	table := len(image) - (2 + 12 + 18*512)
	binary.LittleEndian.PutUint64(image[table+4:], 30)
	if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
		t.Fatal("a sustained portable status overflow was admitted")
	}
	p.LengthTicks = 30
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("an unsustainable portable performance was exported")
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal("portable limits leaked into native playback", err)
	}
}
