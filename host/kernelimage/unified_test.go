package kernelimage_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestFrozenMainVersion13BytesAndRender(t *testing.T) {
	for _, tc := range []struct{ name, render string }{
		{"legacy-first-acid-v13", "737dbeb7a584cab6ccfcbfcd312db6936aa1c83d48eb2b2c090253240d067439"},
		{"legacy-graph-v13", "1d57d124b55ed3f8bc7f0728e2987d45b1b7e9a7a510a6bcf46121cdb004aab9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata", tc.name+".cicada"))
			if err != nil {
				t.Fatal(err)
			}
			score, ds := notation.Parse(source)
			p, ds := project.FromScore(score)
			if p == nil {
				t.Fatal(ds)
			}
			cfg, err := project.CompileEngine(p, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", tc.name+".cicimg"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := kernelimage.Encode(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("released v13 bytes changed: got %d bytes, want %d", len(got), len(want))
			}
			decoded, err := kernelimage.Decode(want, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			for _, config := range []engine.Config{cfg, decoded} {
				e, err := engine.New(config)
				if err != nil {
					t.Fatal(err)
				}
				if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
					t.Fatal("play rejected")
				}
				h := sha256.New()
				var l, r [128]float32
				var frame [8]byte
				for n := 0; n < 192000; n += 128 {
					e.Render(l[:], r[:])
					for i := range l {
						binary.LittleEndian.PutUint32(frame[:4], math.Float32bits(l[i]))
						binary.LittleEndian.PutUint32(frame[4:], math.Float32bits(r[i]))
						h.Write(frame[:])
					}
					assertNoFault(t, e)
				}
				if fmt.Sprintf("%x", h.Sum(nil)) != tc.render {
					t.Fatalf("released v13 render changed: %x", h.Sum(nil))
				}
			}
		})
	}
}

func TestBothDevelopment14DialectsRejectBeforeMutation(t *testing.T) {
	for _, name := range []string{"development-chords-v14", "development-schedule-v14"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", name+".cicimg"))
			if err != nil {
				t.Fatal(err)
			}
			if binary.LittleEndian.Uint16(data[4:6]) != 14 {
				t.Fatal("fixture does not contain original v14 bytes")
			}
			cfg := engine.Config{Tracks: 7, Seed: 123}
			before := cfg
			err = kernelimage.DecodeInto(data, 48000, 128, &cfg)
			if err == nil || !strings.Contains(err.Error(), "ambiguous development image version 14") {
				t.Fatalf("missing ambiguity diagnostic: %v", err)
			}
			if !reflect.DeepEqual(cfg, before) {
				t.Fatal("rejected header mutated destination")
			}
		})
	}
}

func assertNoFault(t *testing.T, e *engine.Engine) {
	t.Helper()
	var m cmd.Message
	for e.Poll(&m) {
		if m.Kind == cmd.Fault {
			t.Fatalf("kernel fault: %+v", m)
		}
	}
}

func mixedUnifiedConfig(t *testing.T) engine.Config {
	cfg := chordConfig(t)
	cfg.MasterGainDB = -3
	cfg.MasterBiasL, cfg.MasterBiasR = 0.0001, -0.0001
	cfg.Tracks = 3
	cfg.MaxVoices = 8
	cfg.Patterns = append(cfg.Patterns, engine.PatternBank{}, engine.PatternBank{})
	cfg.Track[1] = engine.TrackConfig{Kind: engine.VoiceAudio, GainSet: true}
	cfg.Track[2] = engine.TrackConfig{Kind: engine.VoiceSample, GainSet: true, Sample: &engine.SamplerConfig{Asset: 0, RootKey: 60, Voices: 2, Loop: true}}
	cfg.Patterns[2].Slots[0] = cfg.Patterns[0].Slots[0]
	cfg.Patterns[2].Slots[0].Chords = [64]seq.ChordStep{}
	pcm := make([]float32, 512)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2*math.Pi*float64(i)/32) * 0.05)
	}
	cfg.Assets = []engine.AudioAsset{{SampleRate: 48000, Left: pcm}}
	cfg.Clips = []engine.ClipConfig{{Asset: 0, EndFrame: 512}}
	cfg.Song = nil
	cfg.Scenes = nil
	cfg.Schedule = []engine.ScheduleEvent{
		{Kind: engine.SchedulePattern, Track: 0, Tick: 0, EndTick: 960, ID: 1},
		{Kind: engine.SchedulePattern, Track: 2, Tick: 0, EndTick: 960, ID: 2},
		{Kind: engine.ScheduleClip, Track: 1, Tick: 0, EndTick: 960, ID: 3},
		{Kind: engine.SchedulePatternEnd, Track: 0, Tick: 960, EndTick: 960, ID: 1},
		{Kind: engine.SchedulePatternEnd, Track: 2, Tick: 960, EndTick: 960, ID: 2},
		{Kind: engine.ScheduleClipEnd, Track: 1, Tick: 960, EndTick: 960, ID: 3},
	}
	return cfg
}

func TestUnifiedMixedRoundTripSeekStopAndReentry(t *testing.T) {
	cfg := mixedUnifiedConfig(t)
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != kernelimage.UnifiedImageVersion {
		t.Fatal("wrong unified version")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := kernelimage.Encode(decoded)
	if err != nil || !bytes.Equal(data, reencoded) {
		t.Fatal("v15 layout changed on roundtrip", err)
	}
	direct, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := engine.New(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var dl, dr, wl, wr [128]float32
	var taps [128]engine.TapFrame
	var heard [3]bool
	drain := func(e *engine.Engine) []cmd.Message {
		var messages []cmd.Message
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("kernel fault: %+v", m)
			}
			messages = append(messages, m)
		}
		return messages
	}
	for block := 0; block < 1000; block++ {
		var c cmd.Command
		send := true
		switch block {
		case 0, 410, 700:
			c = cmd.Command{Op: cmd.OpPlay, Track: 255}
		case 50, 200, 600:
			c = cmd.Command{Op: cmd.OpSeek, Track: 255, Arg1: 240}
		case 400, 690:
			c = cmd.Command{Op: cmd.OpStop, Track: 255}
		default:
			send = false
		}
		if send && (!direct.Push(c) || !wire.Push(c)) {
			t.Fatal("command rejected")
		}
		direct.RenderWithTaps(dl[:], dr[:], taps[:])
		wire.Render(wl[:], wr[:])
		for _, frame := range taps {
			for track := range heard {
				heard[track] = heard[track] || frame.Tracks[track].Left != 0 || frame.Tracks[track].Right != 0
			}
		}
		if a, b := drain(direct), drain(wire); !reflect.DeepEqual(a, b) {
			t.Fatalf("direct/image events differ at block %d: %v / %v", block, a, b)
		}
		if dl != wl || dr != wr {
			t.Fatalf("direct/image audio mismatch block %d", block)
		}
	}
	if heard != [3]bool{true, true, true} {
		t.Fatalf("mixed graph/clip/sampler tracks not all audible: %v", heard)
	}
}

func TestUnifiedTruncationAndFooterLimit(t *testing.T) {
	cfg := mixedUnifiedConfig(t)
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(data); n++ {
		if _, err := kernelimage.Decode(data[:n], 48000, 128); err == nil {
			t.Fatalf("accepted prefix length %d", n)
		}
	}
	if _, err := kernelimage.Decode(append(append([]byte(nil), data...), 0), 48000, 128); err == nil {
		t.Fatal("accepted trailing byte")
	}
	cfg.Assets[0].Left = make([]float32, kernelimage.MaxImageBytes/4)
	if _, err := kernelimage.Encode(cfg); err == nil {
		t.Fatal("accepted oversized resident image")
	}
}

func TestUnifiedInvalidRecordsRejectAtEncode(t *testing.T) {
	cases := map[string]func(*engine.Config){
		"polyphony on sampler": func(c *engine.Config) { c.Track[2].Polyphony = 4 },
		"invalid chord count":  func(c *engine.Config) { c.Patterns[0].Slots[0].Chords[0].Count = 1 },
		"duplicate chord pitch": func(c *engine.Config) {
			c.Patterns[0].Slots[0].Chords[0].Notes[1] = c.Patterns[0].Slots[0].Chords[0].Notes[0]
		},
		"sampler chord ownership": func(c *engine.Config) { c.Patterns[2].Slots[0].Chords = c.Patterns[0].Slots[0].Chords },
		"missing sampler":         func(c *engine.Config) { c.Track[2].Sample = nil },
		"sampler asset":           func(c *engine.Config) { c.Track[2].Sample.Asset = 1 },
		"sampler pitch":           func(c *engine.Config) { c.Track[2].Sample.RootKey = 128 },
		"sampler voices":          func(c *engine.Config) { c.Track[2].Sample.Voices = 33 },
		"asset rate":              func(c *engine.Config) { c.Assets[0].SampleRate = 1 },
		"empty PCM":               func(c *engine.Config) { c.Assets[0].Left = nil },
		"stereo dimensions":       func(c *engine.Config) { c.Assets[0].Right = make([]float32, 1) },
		"NaN PCM":                 func(c *engine.Config) { c.Assets[0].Left[0] = float32(math.NaN()) },
		"Inf PCM":                 func(c *engine.Config) { c.Assets[0].Left[0] = float32(math.Inf(1)) },
		"master bias":             func(c *engine.Config) { c.MasterBiasL = float32(math.NaN()) },
		"master gain":             func(c *engine.Config) { c.MasterGainDB = math.Inf(1) },
		"clip asset":              func(c *engine.Config) { c.Clips[0].Asset = 1 },
		"clip offset":             func(c *engine.Config) { c.Clips[0].StartFrame = -1 },
		"clip end":                func(c *engine.Config) { c.Clips[0].EndFrame = 513 },
		"clip fade":               func(c *engine.Config) { c.Clips[0].FadeOutFrames = 513 },
		"clip gain":               func(c *engine.Config) { c.Clips[0].GainDB = math.NaN() },
		"schedule ticks":          func(c *engine.Config) { c.Schedule[0].EndTick = 1 << 53 },
		"schedule kind":           func(c *engine.Config) { c.Schedule[0].Kind = 255 },
		"schedule track":          func(c *engine.Config) { c.Schedule[0].Track = 3 },
		"schedule pattern":        func(c *engine.Config) { c.Schedule[0].Index = 16 },
		"schedule clip":           func(c *engine.Config) { c.Schedule[2].Index = 1 },
		"ambiguous authority":     func(c *engine.Config) { c.Song = []engine.SongEntry{{}} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := mixedUnifiedConfig(t)
			mutate(&c)
			if data, err := kernelimage.Encode(c); err == nil || data != nil {
				t.Fatal("invalid unified config encoded")
			}
		})
	}
}

func TestUnifiedFeatureShapesRoundTrip(t *testing.T) {
	mono := firstAcidConfig(t)
	scheduleOnly := firstAcidConfig(t)
	scheduleOnly.Schedule = engine.LowerSong(scheduleOnly.Song)
	scheduleOnly.Song = nil
	clips := engine.Config{SampleRate: 48000, MaxBlock: 128, Tracks: 1, MaxVoices: 1, BPMMilli: 120000, Patterns: []engine.PatternBank{{}}}
	clips.Track[0].Kind = engine.VoiceAudio
	clips.Assets = []engine.AudioAsset{{SampleRate: 48000, Left: []float32{0.1, 0.05, -0.1, -0.05}}}
	clips.Clips = []engine.ClipConfig{{EndFrame: 4}}
	clips.Schedule = []engine.ScheduleEvent{{Kind: engine.ScheduleClip, Tick: 0, EndTick: 960, ID: 1}, {Kind: engine.ScheduleClipEnd, Tick: 960, EndTick: 960, ID: 1}}
	sampler := clips
	sampler.MaxVoices = 2
	sampler.Clips = nil
	sampler.Schedule = nil
	sampler.Track[0] = engine.TrackConfig{Kind: engine.VoiceSample, Sample: &engine.SamplerConfig{RootKey: 60, Voices: 2, Loop: true}}
	sampler.Patterns = []engine.PatternBank{{}}
	p := &sampler.Patterns[0].Slots[0]
	p.Len = 1
	p.GatePercent = 55
	p.Steps[0], _ = seq.PackStep(seq.Step{Note: 60, Gate: true, Velocity: 100, Ratchet: 1, Probability: 100})
	sampler.Scenes = []engine.Scene{{Track: [16]engine.SceneBinding{{Mode: engine.SceneSlot}}}}
	sampler.Song = []engine.SongEntry{{Bars: 1}}
	empty := chordConfig(t)
	empty.Patterns = []engine.PatternBank{{}}
	empty.Scenes = nil
	empty.Song = nil
	for _, tc := range []struct {
		name    string
		cfg     engine.Config
		version uint16
	}{
		{"mono", mono, 13}, {"chord-only", chordConfig(t), 15}, {"schedule-only", scheduleOnly, 15}, {"clip-only", clips, 15}, {"sampler-only", sampler, 15}, {"empty-optional-sections", empty, 15}, {"combined", mixedUnifiedConfig(t), 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := kernelimage.Encode(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if binary.LittleEndian.Uint16(data[4:6]) != tc.version {
				t.Fatal("unexpected feature version")
			}
			decoded, err := kernelimage.Decode(data, 48000, 128)
			if err != nil {
				t.Fatal(err)
			}
			again, err := kernelimage.Encode(decoded)
			if err != nil || !bytes.Equal(data, again) {
				t.Fatal("wire roundtrip changed", err)
			}
			direct, err := engine.New(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := engine.New(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(audioFrames(t, direct), audioFrames(t, wire)) {
				t.Fatal("native image roundtrip changed playback")
			}
		})
	}
}
