package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func audioRenderFixture(t *testing.T, withSampler bool) (string, *notation.Score) {
	t.Helper()
	dir := t.TempDir()
	data := testwav.Bytes(48000, 2, 32, 120000, 3)
	for i := 0; i < 120000; i++ {
		sample := float32(math.Sin(float64(i)*.09) * .2)
		binary.LittleEndian.PutUint32(data[44+i*8:], math.Float32bits(sample))
		binary.LittleEndian.PutUint32(data[48+i*8:], math.Float32bits(-sample*.3))
	}
	if err := os.WriteFile(filepath.Join(dir, "take.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("cicada 2\ntempo 120\nasset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 120000 rate = 48000Hz channels = 2 }\nclip take-a take { start = 100frames end = 119000frames gain = -3dB fade_in = 100frames fade_out = 200frames }\ntrack vox audio {}\nscene main { vox = take-a }\nscene hold { vox = keep }\nscene quiet { vox = off }\nsong { main hold quiet main }\n", sha256.Sum256(data))
	if withSampler {
		text += "sampler hit { asset = take root = c3 mode = loop voices = 2 }\ntrack keys hit {}\npattern notes { c3 . g3 . }\nscene keys { keys=notes vox=off }\n"
		text = string(bytes.Replace([]byte(text), []byte("song { main hold quiet main }"), []byte("song { main hold quiet keys }"), 1))
	}
	score, ds := notation.Parse([]byte(text))
	if score == nil || hasAudioErrors(ds) {
		t.Fatal(ds)
	}
	return dir, score
}
func hasAudioErrors(ds []notation.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

func TestNativeAudioMatchesWAVAndRange(t *testing.T) {
	dir, score := audioRenderFixture(t, false)
	var wav, small, ranged bytes.Buffer
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	report, err := WAV(score, opts, &wav)
	if err != nil {
		t.Fatal(err)
	}
	opts.Block = 31
	if _, err := WAV(score, opts, &small); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wav.Bytes(), small.Bytes()) {
		t.Fatal("audio WAV depends on block size")
	}
	opts.From, opts.Bars = 1, 1
	if _, err := WAV(score, opts, &ranged); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ranged.Bytes()[44:44+96000*8], wav.Bytes()[44+96000*8:44+192000*8]) {
		t.Fatal("audio range does not match full-song playback")
	}
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	cfg, err := sampleasset.CompileEngine(dir, p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	limiter, _ := mix.NewLimiter(48000)
	latency := limiter.LatencyFrames()
	l, r := make([]float32, int(report.Frames)+latency), make([]float32, int(report.Frames)+latency)
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	for at := 0; at < len(l); at += 128 {
		end := min(len(l), at+128)
		e.Render(l[at:end], r[at:end])
		var m cmd.Message
		for e.Poll(&m) {
			if m.Kind == cmd.Fault {
				t.Fatalf("fault: %+v", m)
			}
		}
	}
	pcm := wav.Bytes()[44 : 44+int(report.Frames)*8]
	for i := 0; i < int(report.Frames); i++ {
		wl := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8:]))
		wr := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i*8+4:]))
		if math.Abs(float64(wl-l[i+latency])) > 1e-7 || math.Abs(float64(wr-r[i+latency])) > 1e-7 {
			t.Fatalf("native/WAV mismatch frame %d: %v/%v vs %v/%v", i, l[i+latency], r[i+latency], wl, wr)
		}
	}
}

func TestSamplerWAVAndImmutableAssetFailure(t *testing.T) {
	dir, score := audioRenderFixture(t, true)
	var wav bytes.Buffer
	report, err := WAV(score, Options{AssetDir: dir, SampleRate: 48000, Bits: 32}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputPeak <= 0 || report.ClippedSamples != 0 {
		t.Fatalf("sampler output: %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(dir, "take.wav"))
	if err != nil {
		t.Fatal(err)
	}
	data[44] ^= 1
	os.WriteFile(filepath.Join(dir, "take.wav"), data, 0600)
	wav.Reset()
	if _, err := WAV(score, Options{AssetDir: dir}, &wav); err == nil || wav.Len() != 0 {
		t.Fatal("changed asset was rendered or partially published")
	}
	if _, err := WAV(score, Options{}, &wav); err == nil {
		t.Fatal("audio render implicitly trusted the current directory")
	}
}

func TestRepeatedAudioSceneRetriggersAndKeepContinues(t *testing.T) {
	dir, score := audioRenderFixture(t, false)
	score.Song = []notation.SongEntry{{Scene: "main", Bars: 1}, {Scene: "main", Bars: 1}}
	var repeated, held bytes.Buffer
	opts := Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Block: 128}
	if _, err := WAV(score, opts, &repeated); err != nil {
		t.Fatal(err)
	}
	score.Song[1].Scene = "hold"
	if _, err := WAV(score, opts, &held); err != nil {
		t.Fatal(err)
	}
	// Allow the bounded switch declick to finish before comparing the phrase.
	first := repeated.Bytes()[44+512*8 : 44+1024*8]
	second := repeated.Bytes()[44+(96000+512)*8 : 44+(96000+1024)*8]
	if !bytes.Equal(first, second) {
		t.Fatal("explicit scene assignment did not retrigger the clip")
	}
	if bytes.Equal(first, held.Bytes()[44+(96000+512)*8:44+(96000+1024)*8]) {
		t.Fatal("keep retriggered the clip")
	}
}
