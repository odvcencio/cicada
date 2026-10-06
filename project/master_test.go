package project

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

const masterMusic = `cicada 2
track bass acid {}
pattern pulse acid { 1 . 3 . 5 . 3 . }
scene main { bass = pulse }
song { main*16 }
`

const masterEffects = `fx tone eq { type = highpass frequency = 25Hz q = 0.707 }
fx glue comp { threshold = -18dB ratio = 2 makeup = 1dB }
fx punch transient { attack = 2dB sustain = -1dB }
fx stereo width { amount = 1.08 bass_mono = 100Hz }
fx ceiling limiter { ceiling = -1dBTP }
master { insert = tone -> glue -> punch -> stereo -> ceiling }
export streaming { loudness = -14LUFS true_peak = -1dBTP normalize = off }
`

func masterProject(t *testing.T, source string) *Project {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatalf("parse: %+v", ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatalf("compile: %+v", ds)
	}
	return p
}

func TestMasterChainRoundTripsAndOwnsNativeDSP(t *testing.T) {
	p := masterProject(t, masterMusic+masterEffects)
	source, err := ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if !SemanticEqual(p, masterProject(t, string(source))) {
		t.Fatal("source lost chain or targets")
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil || !SemanticEqual(p, decoded) {
		t.Fatalf("JSON round trip: %v", err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		cfg, err := CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.CompMusic != nil || cfg.MasterProcessor == nil || cfg.MasterProcessor.LatencyFrames() == 0 {
			t.Fatal("master compressor leaked onto music bus or latency missing")
		}
		if _, err := kernelimage.Encode(cfg); err == nil {
			t.Fatal("image silently omitted host master DSP")
		}
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		l, r := make([]float32, 128), make([]float32, 128)
		allocs := testing.AllocsPerRun(100, func() {
			e.Render(l, r)
			var message cmd.Message
			for e.Poll(&message) {
				if message.Kind == cmd.Fault {
					panic("master render fault")
				}
			}
		})
		if allocs != 0 {
			t.Fatalf("rate %d: %g render allocations", rate, allocs)
		}
		cfg.MasterProcessor.Reset()
		for i := 0; i < 1024; i++ {
			l, r := cfg.MasterProcessor.Process(0, 0)
			if l != 0 || r != 0 {
				t.Fatal("reset retained master history")
			}
		}
	}
}

func TestMasterChainRejectsInvalidControlsAndRoutes(t *testing.T) {
	cases := []struct{ source, code string }{
		{"master { insert = missing }", "CICADA-REFERENCE"},
		{"fx glue comp {} master { insert = glue -> glue }", "CICADA-DUPLICATE"},
		{"fx tone eq { frequency = 400ms } master { insert = tone }", "CICADA-PARAM"},
		{"fx tone eq { q = 0 } master { insert = tone }", "CICADA-PARAM"},
		{"fx stereo width { amount = 3 } master { insert = stereo }", "CICADA-PARAM"},
		{"fx stereo width { amount = 1dB } master { insert = stereo }", "CICADA-PARAM"},
		{"fx punch transient { attack = 20dB } master { insert = punch }", "CICADA-PARAM"},
		{"fx ceiling limiter { ceiling = -20dBTP } master { insert = ceiling }", "CICADA-PARAM"},
		{"fx room convolution { asset = absent } master { insert = room }", "CICADA-REFERENCE"},
		{"fx room convolution { asset = absent partition = 100frames } master { insert = room }", "CICADA-PARAM"},
		{"fx glue comp { sidechain = sfx } master { insert = glue }", "CICADA-UNSUPPORTED"},
		{"fx glue comp {} bus music { insert = glue } master { insert = glue }", "CICADA-UNSUPPORTED"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			score, ds := notation.Parse([]byte(masterMusic + tc.source))
			_, more := FromScore(score)
			ds = append(ds, more...)
			for _, d := range ds {
				if d.Code == tc.code && d.Position.Line > 0 {
					return
				}
			}
			t.Fatalf("want %s: %+v", tc.code, ds)
		})
	}
	source := masterMusic
	var names []string
	for i := 0; i < 17; i++ {
		name := fmt.Sprintf("eq%d", i)
		names = append(names, name)
		source += "fx " + name + " eq {}\n"
	}
	score, ds := notation.Parse([]byte(source + "master { insert = " + strings.Join(names, " -> ") + " }"))
	_, more := FromScore(score)
	ds = append(ds, more...)
	for _, d := range ds {
		if d.Code == "CICADA-LIMIT" {
			return
		}
	}
	t.Fatalf("chain length: %+v", ds)
}

func TestMasterConvolutionPreparesVerifiedProjectAsset(t *testing.T) {
	dir := t.TempDir()
	wav := make([]byte, 44+32*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 48000)
	binary.LittleEndian.PutUint32(wav[28:], 96000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 64)
	binary.LittleEndian.PutUint16(wav[44:], 16384)
	if err := os.WriteFile(filepath.Join(dir, "impulse.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	source := masterMusic + fmt.Sprintf(`asset room-ir "impulse.wav" { sha256 = "%x" format = wav frames = 32 rate = 48000Hz channels = 1 }
fx room convolution { asset = room-ir mix = 0.2 partition = 128frames }
master { insert = room }
`, sha256.Sum256(wav))
	path := filepath.Join(dir, "score.cicada")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	score, ds, err := LoadScore(path, nil)
	if err != nil || len(ds) != 0 {
		t.Fatalf("load: %v %+v", err, ds)
	}
	p, ds := FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	if p.NeedsSampleEngine() {
		t.Fatal("impulse-only score requires sampler")
	}
	cfg, err := CompileEngine(p, 44100, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MasterProcessor.LatencyFrames() != 128 {
		t.Fatal("wet/dry partition alignment missing")
	}
	allocs := testing.AllocsPerRun(100, func() { cfg.MasterProcessor.Process(.1, -.2) })
	if allocs != 0 {
		t.Fatalf("convolution render allocations: %g", allocs)
	}
	wav[44]++
	if err := os.WriteFile(filepath.Join(dir, "impulse.wav"), wav, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileEngine(p, 48000, 128); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("changed impulse accepted: %v", err)
	}
}
