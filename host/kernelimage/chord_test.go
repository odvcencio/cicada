package kernelimage_test

import (
	"encoding/binary"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"reflect"
	"testing"
)

const chordScore = `tempo 120
key d minor
instrument piano { voice poly { out=sine(pitch)*env(gate,300ms)*0.1 } }
track keys piano {}
pattern chords notes { [d4 f4 a4] - . [c4 e4 g4] }
scene one { keys=chords }
song { one }
`

func chordConfig(t *testing.T) engine.Config {
	t.Helper()
	score, ds := notation.Parse([]byte(chordScore))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatalf("source %+v", ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func audioFrames(t *testing.T, e *engine.Engine) []float32 {
	t.Helper()
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		t.Fatal("play rejected")
	}
	out := make([]float32, 24000)
	var l, r [128]float32
	for at := 0; at < len(out); at += 128 {
		n := min(128, len(out)-at)
		e.Render(l[:n], r[:n])
		copy(out[at:at+n], l[:n])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("kernel fault %+v", m)
			}
		}
	}
	return out
}

func TestChordImageAndRealCommandUploadPlayback(t *testing.T) {
	cfg := chordConfig(t)
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(image[4:6]) != 14 {
		t.Fatal("poly image not version14")
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatalf("v14 changed config: %v", err)
	}
	reference, err := engine.New(decoded)
	if err != nil {
		t.Fatal(err)
	}
	want := audioFrames(t, reference)
	target := cfg
	target.Patterns = []engine.PatternBank{{}}
	pattern := cfg.Patterns[0].Slots[0]
	commands, err := kernelimage.PatternCommands(pattern, &target, 0, 0, kernelimage.CapabilityChords)
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := engine.New(target)
	if err != nil {
		t.Fatal(err)
	}
	bytes := make([]byte, 0, len(commands)*24)
	for _, c := range commands {
		wire, err := cmd.EncodeCommand(c, 1)
		if err != nil {
			t.Fatal(err)
		}
		bytes = append(bytes, wire[:]...)
	}
	decodedCommands := make([]cmd.Command, len(commands))
	n, err := cmd.DecodeCommands(bytes, 1, decodedCommands)
	if err != nil || !uploaded.PushBatch(decodedCommands[:n]) {
		t.Fatalf("wire upload rejected %v", err)
	}
	if got := audioFrames(t, uploaded); !reflect.DeepEqual(want, got) {
		t.Fatal("uploaded chord audio differs from complete image path")
	}
	rejected, err := kernelimage.PatternCommands(pattern, &target, 0, 0, 0)
	if err == nil || rejected != nil {
		t.Fatal("old capability silently accepted chord")
	}
	before := target
	for _, field := range []string{"gate", "seed"} {
		bad := pattern
		if field == "gate" {
			bad.GatePercent = 60
		} else {
			bad.Seed++
		}
		commands, err := kernelimage.PatternCommands(bad, &target, 0, 0, 1)
		if err == nil || commands != nil || !reflect.DeepEqual(before, target) {
			t.Fatalf("unsupported %s metadata was not atomically rejected", field)
		}
	}
	custom := cfg
	custom.Patterns[0].Slots[0].GatePercent = 60
	custom.Patterns[0].Slots[0].Seed = 7
	image, err = kernelimage.Encode(custom)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || dc.Patterns[0].Slots[0].GatePercent != 60 || dc.Patterns[0].Slots[0].Seed != 7 {
		t.Fatal("complete image dropped gate/seed")
	}
	image[4] = 15
	if _, err := kernelimage.Decode(image, 48000, 128); err == nil {
		t.Fatal("unsupported image version accepted")
	}
	mono := firstAcidConfig(t)
	mi, _ := kernelimage.Encode(mono)
	if binary.LittleEndian.Uint16(mi[4:6]) != 13 {
		t.Fatal("mono image version changed")
	}
	_ = seq.NoteOn
}

func TestChordCommandUploadCanShrinkLoadedSlot(t *testing.T) {
	cfg := chordConfig(t)
	loaded := cfg.Patterns[0].Slots[0]
	short := loaded
	short.Len = 1
	for i := 1; i < 64; i++ {
		short.Chords[i] = seq.ChordStep{}
	}
	commands, err := kernelimage.PatternCommands(short, &cfg, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.PushBatch(commands) {
		t.Fatal("shrink upload rejected")
	}
	if got := audioFrames(t, e); len(got) == 0 {
		t.Fatal("shrunken chord did not render")
	}
}

func TestChordCommandUploadClearsOldPayloadBeforeNewSlides(t *testing.T) {
	cfg := chordConfig(t)
	loaded := cfg.Patterns[0].Slots[0]
	loaded.Steps[1] = loaded.Steps[0]
	loaded.Chords[1] = loaded.Chords[0]
	cfg.Patterns[0].Slots[0] = loaded
	replacement := loaded
	replacement.Chords = [64]seq.ChordStep{}
	replacement.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Slide: true, Ratchet: 1, Probability: 100})
	replacement.Steps[1], _ = seq.PackStep(seq.Step{Note: 64, Gate: true, Ratchet: 1, Probability: 100})
	commands, err := kernelimage.PatternCommands(replacement, &cfg, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.PushBatch(commands) {
		t.Fatal("replace batch rejected")
	}
	audioFrames(t, e)
}

func TestImageCannotDropInvalidUnusedChordPayload(t *testing.T) {
	cfg := chordConfig(t)
	cfg.Patterns[0].Slots[2].Chords[0] = seq.ChordStep{Notes: [4]uint8{60}}
	if _, err := engine.New(cfg); err == nil {
		t.Fatal("direct invalid cfg accepted")
	}
	if data, err := kernelimage.Encode(cfg); err == nil || data != nil {
		t.Fatal("image silently dropped unused invalid chord payload")
	}
}
