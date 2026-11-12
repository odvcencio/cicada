package render

import (
	"bytes"
	"reflect"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// When SameAudio says two renders write the same audio, they must; and gains
// far enough apart to change a factor must not count as the same.
func TestSameAudioMatchesRenders(t *testing.T) {
	for _, test := range []struct {
		name  string
		score *notation.Score
	}{
		{"direct, no master chain", cacheScoreWithMaster(t, "master { level = 3dB }")},
		{"direct, no master block", cacheScoreWithMaster(t, "")},
		{"direct, chain ahead of the gain", cacheScoreWithMaster(t, "fx tone eq { type = highpass frequency = 25Hz }\nmaster { level = -2dB insert = tone }")},
		{"direct, limiter chain", cacheScoreVariant(t)},
		{"scheduled, no master chain", scheduledCacheScore(t, "master { level = 3dB }")},
		{"scheduled, limiter chain", scheduledCacheScore(t, masterCacheChain)},
		{"scheduled, chain without limiter", scheduledCacheScore(t, "fx tone eq { type = highpass frequency = 25Hz }\nmaster { level = -2dB insert = tone }")},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := Options{SampleRate: 48000, Bars: 1, TailSec: .05}
			at := func(gain float64) Options {
				opts := base
				opts.MasterGainDB = gain
				return opts
			}
			const gain = 11.40714904491081
			a := at(gain)
			want, _ := renderCacheBytes(t, test.score, a)
			same, differ := 0, 0
			// Steps from far below one float32 step of the factor (about
			// 5e-7 dB) to far above it.
			for _, step := range []float64{0, 8.4e-8, 2e-7, 4e-7, 1e-6, 1e-3, .5} {
				b := at(gain + step)
				got, _ := renderCacheBytes(t, test.score, b)
				switch {
				case SameAudio(test.score, a, b):
					same++
					if !bytes.Equal(got, want) {
						t.Fatalf("step %g dB: SameAudio is true but the renders differ", step)
					}
				default:
					differ++
				}
			}
			if same < 2 || differ < 2 {
				t.Fatalf("SameAudio was true for %d steps and false for %d; the steps should show both", same, differ)
			}
		})
	}
}

func TestSameAudioComparesEveryOption(t *testing.T) {
	score := cacheScoreVariant(t)
	off := false
	base := Options{SampleRate: 48000, Bars: 1, TailSec: .05}
	if !SameAudio(score, base, base) {
		t.Fatal("equal options must render the same audio")
	}
	// One change per field. The export gains and MasterCache are compared by
	// their effect or not at all, so they are not here.
	changes := map[string]func(*Options){
		"AssetRoot":       func(o *Options) { o.AssetRoot = "elsewhere" },
		"AssetDir":        func(o *Options) { o.AssetDir = "elsewhere" },
		"SamplerBaseline": func(o *Options) { o.SamplerBaseline = true },
		"SampleRate":      func(o *Options) { o.SampleRate = 44100 },
		"Bits":            func(o *Options) { o.Bits = 16 },
		"Bars":            func(o *Options) { o.Bars = 2 },
		"From":            func(o *Options) { o.From = 1 },
		"TailSec":         func(o *Options) { o.TailSec = .06 },
		"Dither":          func(o *Options) { o.Dither = &off },
		"Normalize":       func(o *Options) { o.Normalize = true },
		"Block":           func(o *Options) { o.Block = 512 },
		"MasterBiasL":     func(o *Options) { o.MasterBiasL = 1e-6 },
		"MasterBiasR":     func(o *Options) { o.MasterBiasR = -1e-6 },
		"MaxResidentPCM":  func(o *Options) { o.MaxResidentPCM = 1 << 20 },
		"NoteTap":         func(o *Options) { o.NoteTap = func(int, seq.Event) {} },
	}
	ignored := map[string]bool{"MasterGainDB": true, "MasterCache": true}
	fields := reflect.TypeOf(Options{})
	for i := range fields.NumField() {
		name := fields.Field(i).Name
		change, ok := changes[name]
		if ignored[name] {
			continue
		}
		if !ok {
			t.Fatalf("Options.%s is new: decide whether it changes the audio and compare it in sameNonGainOptions", name)
		}
		b := base
		change(&b)
		if SameAudio(score, base, b) || SameAudio(score, b, base) {
			t.Fatalf("a change of Options.%s still counts as the same audio", name)
		}
	}
	cache := newTestMasterCache(t)
	withCache := base
	withCache.MasterCache = cache
	if !SameAudio(score, base, withCache) {
		t.Fatal("a cache never changes the audio")
	}
	outOfRange := base
	outOfRange.MasterGainDB = 30
	if SameAudio(score, outOfRange, outOfRange) {
		t.Fatal("a gain that the render refuses cannot count as the same audio")
	}
}
