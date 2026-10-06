package sampleasset

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func audioEngineFixture(t *testing.T, sampler bool) (string, *project.Project) {
	t.Helper()
	dir := t.TempDir()
	data := testwav.Bytes(48000, 2, 32, 240000, 3)
	for frame := 0; frame < 240000; frame++ {
		l := float32(math.Sin(float64(frame)*.07) * .2)
		binary.LittleEndian.PutUint32(data[44+frame*8:], math.Float32bits(l))
		binary.LittleEndian.PutUint32(data[48+frame*8:], math.Float32bits(-l*.5))
	}
	if err := os.WriteFile(filepath.Join(dir, "take.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("cicada 2\ntempo 120\nasset take \"take.wav\" { sha256 = \"%x\" format = wav frames = 240000 rate = 48000Hz channels = 2 }\nclip take-a take { start = 100frames end = 230100frames gain = -2dB fade_in = 100frames fade_out = 200frames }\ntrack vox audio {}\nscene main { vox = take-a }\nscene hold { vox = keep }\nscene quiet { vox = off }\nsong { main hold quiet main }\n", sha256.Sum256(data))
	if sampler {
		text += "sampler hit { asset = take root = c3 mode = loop voices = 2 }\ntrack keys hit {}\npattern notes { c3 . g3 . }\n"
		text = strings.Replace(text, "scene main { vox = take-a }", "scene main { vox = take-a keys = notes }", 1)
	}
	score, ds := notation.Parse([]byte(text))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("parse/compile: %+v %+v", ds, more)
	}
	return dir, p
}

func TestPreparedAudioSharesPCMAndSeparatesOwners(t *testing.T) {
	dir, p := audioEngineFixture(t, true)
	factories, err := Prepare(dir, p)
	if err != nil {
		t.Fatal(err)
	}
	clip := factories[0].(*clipFactory).slots[0]
	sampler := factories[1].(*samplerFactory)
	if &clip.region.Left[0] != &sampler.region.Left[0] {
		t.Fatal("shared asset decoded twice")
	}
	a, err := factories[0].NewStereoVoice(48000)
	if err != nil {
		t.Fatal(err)
	}
	b, err := factories[0].NewStereoVoice(48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SelectSlot(0, 0, true); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		a.NextStereo()
	}
	if l, r := b.NextStereo(); l != 0 || r != 0 {
		t.Fatal("owners share playback state")
	}
	if err := b.SelectSlot(0, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := a.SelectSlot(0, 0, true); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	a.SelectSlot(0, 0, true)
	for range 128 {
		al, ar := a.NextStereo()
		bl, br := b.NextStereo()
		if al != bl || ar != br {
			t.Fatal("independent restart differs")
		}
	}
	p.Assets[0].Frames = MaxFrames + 1
	if _, err := Prepare(dir, p); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("resident budget: %v", err)
	}
}

func TestClipFadesPauseAndSeek(t *testing.T) {
	pcm := make([]float32, 1000)
	for i := range pcm {
		pcm[i] = float32(i) / 1000
	}
	f := &clipFactory{}
	f.slots[0] = &clipRegion{region: sample.Region{Left: pcm, SampleRate: 48000, RootKey: 60, Start: 100, End: 900}, gain: 1, fadeIn: 100, fadeOut: 100}
	owned, err := f.NewStereoVoice(48000)
	if err != nil {
		t.Fatal(err)
	}
	v := owned.(*clipVoice)
	v.SelectSlot(0, 0, true)
	for i := range 200 {
		l, r := v.NextStereo()
		want := float32(float64(pcm[100+i]) * min(1, float64(i)/100))
		if l != want || r != want {
			t.Fatalf("fade %d: %v/%v != %v", i, l, r, want)
		}
	}
	v.NoteOff()
	for range 120 {
		v.NextStereo()
	}
	if v.position[0] != 200 {
		t.Fatal("pause advanced the clip cursor")
	}
	if err := v.Play(); err != nil {
		t.Fatal(err)
	}
	if l, _ := v.NextStereo(); l != pcm[300] {
		t.Fatalf("resume %v", l)
	}
	v.Reset()
	v.SelectSlot(0, 750, true)
	if l, _ := v.NextStereo(); l != float32(float64(pcm[850])*.5) {
		t.Fatalf("fade-out seek %v", l)
	}
	v.Reset()
	v.SelectSlot(0, 1000, true)
	if l, r := v.NextStereo(); l != 0 || r != 0 {
		t.Fatal("seek past clip replayed it")
	}
}

func renderPreparedEngine(t *testing.T, cfg engine.Config, block, frames int, commands ...cmd.Command) ([]float32, []float32) {
	t.Helper()
	cfg.MaxBlock = block
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if !e.Push(command) {
			t.Fatal("command rejected")
		}
	}
	left, right := make([]float32, frames), make([]float32, frames)
	for at := 0; at < frames; at += block {
		end := min(at+block, frames)
		e.Render(left[at:end], right[at:end])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatalf("engine fault: %+v", message)
			}
		}
	}
	return left, right
}

func TestAudioEngineChunkInvarianceAndInheritedSeek(t *testing.T) {
	dir, p := audioEngineFixture(t, false)
	cfg, err := CompileEngine(dir, p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	a, b := renderPreparedEngine(t, cfg, 128, 100000, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	c, d := renderPreparedEngine(t, cfg, 31, 100000, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	if !reflect.DeepEqual(a, c) || !reflect.DeepEqual(b, d) {
		t.Fatal("audio depends on callback block size")
	}
	l, r := renderPreparedEngine(t, cfg, 128, 4000, cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 1}, cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	// The limiter's lookahead is freshly empty after seek. Once it fills,
	// below-ceiling PCM matches continuous playback at exactly bar two.
	for i := 512; i < len(l); i++ {
		if l[i] != a[96000+i] || r[i] != b[96000+i] {
			t.Fatalf("inherited clip seek differs at %d: %v/%v vs %v/%v", i, l[i], r[i], a[96000+i], b[96000+i])
		}
	}
}

func TestPreparedEngineSamplerAndNoRenderAllocations(t *testing.T) {
	dir, p := audioEngineFixture(t, true)
	cfg, err := CompileEngine(dir, p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		t.Fatal("play rejected")
	}
	var l, r [128]float32
	allocs := testing.AllocsPerRun(100, func() {
		e.Render(l[:], r[:])
		var message cmd.Message
		for e.Poll(&message) {
		}
	})
	if allocs != 0 {
		t.Fatalf("audio callback allocations: %v", allocs)
	}
	if l[100] == 0 || r[100] == 0 || l[100] == r[100] {
		t.Fatal("prepared stereo audio did not reach the mixer")
	}
}
