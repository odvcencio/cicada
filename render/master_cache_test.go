package render

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// masterCacheScore has two scenes with different tempos, so the full render
// splits its blocks at bar boundaries while a replay uses whole blocks. Its
// drive inserts delay the tracks, and its sends and master chain end in a
// limiter. The chain's effects are a separate line so tests can swap them.
const masterCacheScore = `tempo 120
key a minor
seed 4242
kit pulse { bd = builtin.bd }
fx drive drive { shape = soft gain = 9dB tone = 9kHz mix = 0.7 }
fx delay delay {}
fx reverb reverb {}
MASTER_FX
track acid acid { level = -12dB insert = drive send delay = 0.3 send reverb = 0.35 }
track drums pulse { level = -12dB insert = drive send delay = 0.3 }
pattern riff { 1 5 1 3 }
pattern beat drums { bd: X.x. }
scene first { acid = riff drums = beat }
scene second { acid = riff drums = beat tempo = 100 }
song { first second }
`

// masterCacheChain is the master chain of masterCacheScore: five effects that
// end in a limiter.
const masterCacheChain = `fx tone eq { type = highpass frequency = 25Hz }
fx glue comp { threshold = -18dB }
fx punch transient { attack = 2dB }
fx stereo width { amount = 1.1 }
fx ceiling limiter { ceiling = -1dBTP }
master { level = 3dB insert = tone -> glue -> punch -> stereo -> ceiling }`

func cacheScoreWithMaster(t *testing.T, master string, replacements ...string) *notation.Score {
	t.Helper()
	source := strings.Replace(masterCacheScore, "MASTER_FX", master, 1)
	for i := 0; i+1 < len(replacements); i += 2 {
		if !strings.Contains(source, replacements[i]) {
			t.Fatalf("score has no %q", replacements[i])
		}
		source = strings.Replace(source, replacements[i], replacements[i+1], 1)
	}
	return parseCacheScore(t, source, 2)
}

func parseCacheScore(t *testing.T, source string, edition int) *notation.Score {
	t.Helper()
	score, diagnostics := notation.ParseEdition([]byte(source), edition)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	return score
}

func cacheScoreVariant(t *testing.T, replacements ...string) *notation.Score {
	t.Helper()
	return cacheScoreWithMaster(t, masterCacheChain, replacements...)
}

func renderCacheBytes(t *testing.T, score *notation.Score, opts Options) ([]byte, Report) {
	t.Helper()
	var out bytes.Buffer
	report, err := WAV(score, opts, &out)
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), report
}

func newTestMasterCache(t *testing.T) *MasterCache {
	t.Helper()
	cache, err := NewMasterCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	return cache
}

// Every render that shares a cache must match a render without one, byte for
// byte and field for field, whatever changes after the master input.
func TestMasterCacheReplayMatchesFullRender(t *testing.T) {
	off := false
	for _, test := range []struct {
		name  string
		score *notation.Score
		base  Options
	}{
		{"chain with limiter", cacheScoreVariant(t), Options{SampleRate: 48000, TailSec: .2}},
		{"chain without limiter", cacheScoreWithMaster(t, "fx tone eq { type = highpass frequency = 25Hz }\nfx glue comp { threshold = -18dB }\nmaster { insert = tone -> glue }"), Options{SampleRate: 48000, Bars: 1, TailSec: .1}},
		{"authored master level only", cacheScoreWithMaster(t, "fx tone eq { type = highpass frequency = 25Hz }\nmaster { level = -2dB insert = tone }"), Options{SampleRate: 44100, Bars: 1, TailSec: .1}},
		{"excerpt", cacheScoreVariant(t), Options{SampleRate: 48000, From: 1, Bars: 1, TailSec: .3}},
		{"small blocks", cacheScoreVariant(t), Options{SampleRate: 48000, Bars: 1, Block: 48, TailSec: .05}},
		{"odd blocks", cacheScoreVariant(t), Options{SampleRate: 48000, Bars: 1, Block: 1000, TailSec: .1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := newTestMasterCache(t)
			replays := 0
			for i, change := range []func(*Options){
				func(o *Options) {},
				func(o *Options) { o.MasterInputGainDB = 4.5 },
				func(o *Options) { o.MasterInputGainDB = -7; o.MasterBiasL, o.MasterBiasR = .002, -.001 },
				func(o *Options) { o.MasterGainDB = 1.25; o.MasterBiasL = -.003; o.Bits = 16 },
				func(o *Options) { o.MasterInputGainDB = 2; o.Normalize = true; o.Bits = 32; o.Dither = &off },
			} {
				opts := test.base
				change(&opts)
				want, wantReport := renderCacheBytes(t, test.score, opts)
				opts.MasterCache = cache
				got, gotReport := renderCacheBytes(t, test.score, opts)
				if !bytes.Equal(got, want) {
					t.Fatalf("render %d: cached output differs from a full render (replays so far: %d)", i, cache.Replays())
				}
				if !reflect.DeepEqual(gotReport, wantReport) {
					t.Fatalf("render %d: cached report differs:\n got %+v\nwant %+v", i, gotReport, wantReport)
				}
				if i > 0 && cache.Replays() <= replays {
					t.Fatalf("render %d did not replay from the cache", i)
				}
				replays = cache.Replays()
			}
		})
	}
}

// A render with other inputs replaces the stored signal rather than reading it.
func TestMasterCacheReplacesCaptureForOtherInputs(t *testing.T) {
	cache := newTestMasterCache(t)
	first, second := cacheScoreVariant(t), cacheScoreVariant(t, "pattern riff { 1 5 1 3 }", "pattern riff { 5 3 1 5 }")
	check := func(name string, score *notation.Score, opts Options, wantReplays int) {
		t.Helper()
		want, wantReport := renderCacheBytes(t, score, opts)
		opts.MasterCache = cache
		got, gotReport := renderCacheBytes(t, score, opts)
		if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
			t.Fatalf("%s: output differs from a full render", name)
		}
		if cache.Replays() != wantReplays {
			t.Fatalf("%s: %d replays, want %d", name, cache.Replays(), wantReplays)
		}
	}
	base := Options{SampleRate: 48000, Bars: 1, TailSec: .1}
	check("first score, capture", first, base, 0)
	check("first score, replay", first, base, 1)
	check("second score replaces the capture", second, base, 1)
	check("second score, replay", second, base, 2)
	check("first score replaces it again", first, base, 2)
	check("other range replaces it", first, Options{SampleRate: 48000, From: 1, Bars: 1, TailSec: .1}, 2)
	check("other range, replay", first, Options{SampleRate: 48000, From: 1, Bars: 1, TailSec: .1}, 3)
	check("other sample rate replaces it", first, Options{SampleRate: 44100, From: 1, Bars: 1, TailSec: .1}, 3)
	check("other tail replaces it", first, Options{SampleRate: 44100, From: 1, Bars: 1, TailSec: .2}, 3)
	check("other block size replaces it", first, Options{SampleRate: 44100, From: 1, Bars: 1, TailSec: .2, Block: 512}, 3)
	check("other block size, replay", first, Options{SampleRate: 44100, From: 1, Bars: 1, TailSec: .2, Block: 512}, 4)
	// A render treats an empty AssetRoot as AssetDir and the reverse, so these
	// steps start from a capture that sets both and then change one field of the
	// key at a time. The score has no files to load, so the output stays the
	// same; the replay count shows whether the cache served the render.
	assets := Options{SampleRate: 44100, From: 1, Bars: 1, TailSec: .2, AssetRoot: t.TempDir(), AssetDir: t.TempDir()}
	check("asset root and directory replace it", first, assets, 4)
	check("asset root and directory, replay", first, assets, 5)
	assets.AssetDir = t.TempDir()
	check("other asset directory replaces it", first, assets, 5)
	check("other asset directory, replay", first, assets, 6)
	assets.AssetRoot = t.TempDir()
	check("other asset root replaces it", first, assets, 6)
	check("other asset root, replay", first, assets, 7)
	assets.SamplerBaseline = true
	check("other sampler baseline replaces it", first, assets, 7)
	check("other sampler baseline, replay", first, assets, 8)
}

// Renders the cache cannot serve must run in full and leave it untouched.
func TestMasterCacheIgnoredWhereItCannotApply(t *testing.T) {
	base := Options{SampleRate: 48000, Bars: 1, TailSec: .05}
	for _, test := range []struct {
		name  string
		score *notation.Score
		opts  func(*Options)
	}{
		{"no master chain", cacheScoreWithMaster(t, "master { level = 3dB }"), func(*Options) {}},
		{"note tap", cacheScoreVariant(t), func(o *Options) { o.NoteTap = func(int, seq.Event) {} }},
		{"scheduled engine", cacheScoreVariant(t, "fx delay delay {}", "fx delay delay {}\nfx cleanup eq { type = highpass frequency = 30Hz q = 0.707 }", "track acid acid { level = -12dB insert = drive", "track acid acid { level = -12dB insert = cleanup"), func(*Options) {}},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := newTestMasterCache(t)
			opts := base
			test.opts(&opts)
			if test.name == "scheduled engine" && !UsesScheduledEngine(test.score, opts) {
				t.Fatal("the variant does not use the scheduled engine")
			}
			want, wantReport := renderCacheBytes(t, test.score, opts)
			opts.MasterCache = cache
			for range 2 {
				got, gotReport := renderCacheBytes(t, test.score, opts)
				if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
					t.Fatal("render with an ignored cache differs from a render without one")
				}
			}
			if cache.Replays() != 0 || cache.ready {
				t.Fatalf("cache served a render it cannot serve: replays=%d ready=%v", cache.Replays(), cache.ready)
			}
		})
	}
	t.Run("stems", func(t *testing.T) {
		cache := newTestMasterCache(t)
		opts := base
		opts.MasterCache = cache
		var out bytes.Buffer
		if _, err := renderWithOptions(cacheScoreVariant(t), opts, &out, filepath.Join(t.TempDir(), "stems")); err != nil {
			t.Fatal(err)
		}
		if cache.ready || cache.Replays() != 0 {
			t.Fatal("a stem render used the cache")
		}
	})
}

// A render that fails part way must not leave a partial capture behind.
func TestMasterCacheFailedCaptureIsRetried(t *testing.T) {
	cache := newTestMasterCache(t)
	score := cacheScoreVariant(t)
	opts := Options{SampleRate: 48000, TailSec: .1}
	want, wantReport := renderCacheBytes(t, score, opts)
	opts.MasterCache = cache
	failing := &failAfter{limit: len(want) / 2}
	if _, err := WAV(score, opts, failing); !errors.Is(err, errCacheTestWrite) {
		t.Fatalf("render with a failing writer: %v", err)
	}
	if cache.ready {
		t.Fatal("a failed capture is marked ready")
	}
	for i, wantReplays := range []int{0, 1} {
		got, gotReport := renderCacheBytes(t, score, opts)
		if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
			t.Fatalf("render %d after the failure differs from a full render", i)
		}
		if cache.Replays() != wantReplays {
			t.Fatalf("render %d: %d replays, want %d", i, cache.Replays(), wantReplays)
		}
	}
}

// A capture that fails after an earlier capture succeeded must not leave the
// cache claiming it holds the new signal. The first capture's flag has to go
// when the second capture begins, not when it ends.
func TestMasterCacheFailedRecaptureIsNotReplayed(t *testing.T) {
	cache := newTestMasterCache(t)
	first, second := cacheScoreVariant(t), cacheScoreVariant(t, "pattern riff { 1 5 1 3 }", "pattern riff { 5 3 1 5 }")
	opts := Options{SampleRate: 48000, TailSec: .1}
	want, wantReport := renderCacheBytes(t, second, opts)
	opts.MasterCache = cache
	renderCacheBytes(t, first, opts)
	if !cache.ready {
		t.Fatal("the first capture is not ready")
	}
	if _, err := WAV(second, opts, &failAfter{limit: len(want) / 2}); !errors.Is(err, errCacheTestWrite) {
		t.Fatalf("render with a failing writer: %v", err)
	}
	if cache.ready {
		t.Fatal("a failed second capture left the cache ready")
	}
	replays := cache.Replays()
	got, gotReport := renderCacheBytes(t, second, opts)
	if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
		t.Fatal("render after the failed capture differs from a full render")
	}
	if cache.Replays() != replays {
		t.Fatalf("render after the failed capture replayed %d times from half a capture", cache.Replays()-replays)
	}
	if got, gotReport = renderCacheBytes(t, second, opts); !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) || cache.Replays() != replays+1 {
		t.Fatalf("the repeated render did not replay the new capture: %d replays, want %d", cache.Replays(), replays+1)
	}
}

var errCacheTestWrite = errors.New("test writer failed")

type failAfter struct {
	limit, written int
}

func (w *failAfter) Write(p []byte) (int, error) {
	if w.written+len(p) > w.limit {
		return 0, errCacheTestWrite
	}
	w.written += len(p)
	return len(p), nil
}

// A cache whose file is unusable before the render starts (here it is closed)
// must not fail the render. It stays off for the rest of its life.
func TestMasterCacheUnusableFileDisablesTheCache(t *testing.T) {
	cache := newTestMasterCache(t)
	score := cacheScoreVariant(t)
	opts := Options{SampleRate: 48000, TailSec: .1}
	want, wantReport := renderCacheBytes(t, score, opts)
	opts.MasterCache = cache
	if err := cache.file.Close(); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		got, gotReport := renderCacheBytes(t, score, opts)
		if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
			t.Fatalf("render %d with a broken cache differs from a full render", i)
		}
	}
	if !cache.disabled || cache.ready || cache.Replays() != 0 {
		t.Fatalf("broken cache state: disabled=%v ready=%v replays=%d", cache.disabled, cache.ready, cache.Replays())
	}
}

var errNoSpace = errors.New("no space left on device")

// fullDiskFile is a cache file on a disk with limited room. A write that does
// not fit stores the part that does and then fails, as a full disk does.
type fullDiskFile struct {
	*os.File
	room int64 // bytes the file may still take
}

func (f *fullDiskFile) Write(p []byte) (int, error) {
	n, err := f.File.Write(p[:min(int64(len(p)), f.room)])
	f.room -= int64(n)
	if err == nil && n < len(p) {
		err = errNoSpace
	}
	return n, err
}

// A write that fails part way must not leave the partial capture on the disk:
// the render still has its own output to write there. The render finishes with
// the bytes a render without a cache gives, and the cache stays off.
func TestMasterCacheFullDiskGivesItsSpaceBack(t *testing.T) {
	cache := newTestMasterCache(t)
	score := cacheScoreVariant(t)
	opts := Options{SampleRate: 48000, TailSec: .1}
	want, wantReport := renderCacheBytes(t, score, opts)
	opts.MasterCache = cache
	// Room for three 4096-frame blocks and a part of the fourth.
	disk := &fullDiskFile{File: cache.file.(*os.File), room: 3*4096*8 + 100}
	cache.file = disk
	for i := range 2 {
		got, gotReport := renderCacheBytes(t, score, opts)
		if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
			t.Fatalf("render %d on a full disk differs from a full render", i)
		}
		if i == 0 && disk.room != 0 {
			t.Fatalf("the disk did not fill: %d bytes of room left", disk.room)
		}
	}
	if !cache.disabled || cache.ready || cache.Replays() != 0 {
		t.Fatalf("cache state after the disk filled: disabled=%v ready=%v replays=%d", cache.disabled, cache.ready, cache.Replays())
	}
	info, err := os.Stat(disk.Name())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("the cache that failed still holds %d bytes of disk space", info.Size())
	}
}

// A stored signal that cannot be read back fails the render that replays it,
// because that render has already written part of its output. The cache turns
// itself off and gives its space back, and the next render runs in full.
func TestMasterCacheReadFailureTurnsTheCacheOff(t *testing.T) {
	cache := newTestMasterCache(t)
	score := cacheScoreVariant(t)
	opts := Options{SampleRate: 48000, TailSec: .1}
	want, wantReport := renderCacheBytes(t, score, opts)
	opts.MasterCache = cache
	renderCacheBytes(t, score, opts)
	if !cache.ready {
		t.Fatal("the capture is not ready")
	}
	// Cut the stored signal short, as a failing disk or another process could.
	if err := cache.file.Truncate(cache.frames * 8 / 3); err != nil {
		t.Fatal(err)
	}
	var partial bytes.Buffer
	if _, err := WAV(score, opts, &partial); err == nil || !strings.Contains(err.Error(), "read master cache") {
		t.Fatalf("replay of a cut cache: %v", err)
	}
	if partial.Len() == 0 || partial.Len() >= len(want) {
		t.Fatalf("the failed replay wrote %d of %d bytes; it should fail part way", partial.Len(), len(want))
	}
	if !cache.disabled || cache.ready {
		t.Fatalf("cache state after a failed read: disabled=%v ready=%v", cache.disabled, cache.ready)
	}
	if info, err := os.Stat(cache.file.Name()); err != nil || info.Size() != 0 {
		t.Fatalf("the cache that failed still holds its file: %v (%v)", info, err)
	}
	got, gotReport := renderCacheBytes(t, score, opts)
	if !bytes.Equal(got, want) || !reflect.DeepEqual(gotReport, wantReport) {
		t.Fatal("render after the failed read differs from a full render")
	}
	if cache.Replays() != 1 {
		t.Fatalf("%d replays, want only the one that failed", cache.Replays())
	}
}

// endBlock is the one place a capture can fail after rendering has started.
func TestMasterCacheEndBlockFailureTurnsTheCacheOff(t *testing.T) {
	cache := newTestMasterCache(t)
	if use, replay, err := cache.begin(masterCacheKey{sampleRate: 48000}, 2); !use || replay || err != nil {
		t.Fatalf("begin = %v, %v, %v; want a capture", use, replay, err)
	}
	readOnly, err := os.Open(cache.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	cache.file = readOnly
	if err := cache.beginBlock(2, false); err != nil {
		t.Fatal(err)
	}
	cache.block = append(cache.block, 1, 2, 3, 4)
	cache.endBlock()
	if !cache.disabled || cache.ready {
		t.Fatalf("a failed write left the cache usable: disabled=%v ready=%v", cache.disabled, cache.ready)
	}
	if err := cache.finishCapture(); err != nil || cache.ready {
		t.Fatalf("finishCapture after a failed write: %v, ready=%v", err, cache.ready)
	}
	if use, _, _ := cache.begin(masterCacheKey{sampleRate: 48000}, 2); use {
		t.Fatal("a disabled cache accepted another render")
	}
}

func TestMasterCacheFinishCaptureRejectsAShortCapture(t *testing.T) {
	cache := newTestMasterCache(t)
	if _, _, err := cache.begin(masterCacheKey{}, 4); err != nil {
		t.Fatal(err)
	}
	if err := cache.beginBlock(2, false); err != nil {
		t.Fatal(err)
	}
	cache.block = append(cache.block, 1, 2, 3, 4)
	cache.endBlock()
	if err := cache.finishCapture(); err == nil || cache.ready {
		t.Fatalf("a capture of 2 of 4 frames was accepted: %v, ready=%v", err, cache.ready)
	}
}

func TestMasterCacheCloseRemovesTheFile(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewMasterCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("a new cache should leave one file, found %d", len(entries))
	}
	score := cacheScoreVariant(t)
	renderCacheBytes(t, score, Options{SampleRate: 48000, Bars: 1, TailSec: .05, MasterCache: cache})
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("Close left %d files behind", len(entries))
	}
	var none *MasterCache
	if err := none.Close(); err != nil || none.Replays() != 0 {
		t.Fatal("a nil cache must be inert")
	}
	// A closed cache is ignored, not an error.
	want, _ := renderCacheBytes(t, score, Options{SampleRate: 48000, Bars: 1, TailSec: .05})
	got, _ := renderCacheBytes(t, score, Options{SampleRate: 48000, Bars: 1, TailSec: .05, MasterCache: cache})
	if !bytes.Equal(got, want) {
		t.Fatal("render with a closed cache differs")
	}
}

// unremovableFile reports the folder that holds the cache file as its name.
// Remove cannot delete a folder that is not empty, as it cannot delete a file
// that another program holds open.
type unremovableFile struct{ *os.File }

func (f unremovableFile) Name() string { return filepath.Dir(f.File.Name()) }

// Close empties the file before it removes it, so a file that cannot be removed
// still gives its space back.
func TestMasterCacheCloseEmptiesAFileItCannotRemove(t *testing.T) {
	cache := newTestMasterCache(t)
	renderCacheBytes(t, cacheScoreVariant(t), Options{SampleRate: 48000, Bars: 1, TailSec: .05, MasterCache: cache})
	file := cache.file.(*os.File)
	if info, err := file.Stat(); err != nil || info.Size() == 0 {
		t.Fatalf("the capture left a file of %v (%v)", info, err)
	}
	cache.file = unremovableFile{file}
	if err := cache.Close(); err == nil {
		t.Fatal("Close removed a folder that holds the cache file")
	}
	info, err := os.Stat(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("the file that could not be removed still holds %d bytes", info.Size())
	}
}

func TestMasterCacheStoresEightBytesPerFrame(t *testing.T) {
	cache := newTestMasterCache(t)
	opts := Options{SampleRate: 48000, Bars: 2, TailSec: .1, MasterCache: cache}
	_, report := renderCacheBytes(t, cacheScoreVariant(t), opts)
	info, err := os.Stat(cache.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	// The capture also holds the frames that the track insert and master chain
	// delay, which a render discards from the front of its output.
	if info.Size() < report.Frames*8 || info.Size() > (report.Frames+8192)*8 || info.Size() != cache.frames*8 {
		t.Fatalf("cache file is %d bytes for %d output frames and %d stored", info.Size(), report.Frames, cache.frames)
	}
}

// The capture starts at bar 0, whatever bar the render starts at, so an excerpt
// stores as much as a render of the song up to the same end.
func TestMasterCacheExcerptStoresTheSongFromTheStart(t *testing.T) {
	score := cacheScoreVariant(t)
	whole, excerpt := newTestMasterCache(t), newTestMasterCache(t)
	renderCacheBytes(t, score, Options{SampleRate: 48000, Bars: 2, TailSec: .1, MasterCache: whole})
	_, report := renderCacheBytes(t, score, Options{SampleRate: 48000, From: 1, Bars: 1, TailSec: .1, MasterCache: excerpt})
	if excerpt.frames != whole.frames || excerpt.frames <= report.Frames {
		t.Fatalf("excerpt stored %d frames for %d output frames; a render of bars 1-2 stored %d", excerpt.frames, report.Frames, whole.frames)
	}
}
