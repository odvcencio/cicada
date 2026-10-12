package schedule_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func streamingFixture(t *testing.T, frames, rate, tracks int) (string, *project.Project, *notation.Score) {
	t.Helper()
	root := t.TempDir()
	var source strings.Builder
	source.WriteString("cicada 2\ntempo 120\n")
	for track := 0; track < tracks; track++ {
		path := filepath.Join(root, fmt.Sprintf("take%d.wav", track))
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		var header [44]byte
		copy(header[:], "RIFF")
		binary.LittleEndian.PutUint32(header[4:], uint32(36+frames*2))
		copy(header[8:], "WAVEfmt ")
		binary.LittleEndian.PutUint32(header[16:], 16)
		binary.LittleEndian.PutUint16(header[20:], 1)
		binary.LittleEndian.PutUint16(header[22:], 1)
		binary.LittleEndian.PutUint32(header[24:], uint32(rate))
		binary.LittleEndian.PutUint32(header[28:], uint32(rate*2))
		binary.LittleEndian.PutUint16(header[32:], 2)
		binary.LittleEndian.PutUint16(header[34:], 16)
		copy(header[36:], "data")
		binary.LittleEndian.PutUint32(header[40:], uint32(frames*2))
		hash := sha256.New()
		_, _ = file.Write(header[:])
		_, _ = hash.Write(header[:])
		var buffer [8192]byte
		for at := 0; at < frames; {
			n := min(4096, frames-at)
			for i := 0; i < n; i++ {
				// Quiet periodic PCM exposes channel, phase and gain changes without
				// driving the shared safety limiter into gain reduction.
				value := int16(math.Sin(float64(at+i)*(.027+float64(track)*.003)) * 1000)
				binary.LittleEndian.PutUint16(buffer[2*i:], uint16(value))
			}
			if _, err := file.Write(buffer[:n*2]); err != nil {
				t.Fatal(err)
			}
			_, _ = hash.Write(buffer[:n*2])
			at += n
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&source, "asset a%d \"take%d.wav\" { sha256 = \"%x\" format = wav frames = %d rate = %dHz channels = 1 source = recorded }\nclip c%d a%d { start = 97frames end = %dframes gain = -3dB fade_in = 173frames fade_out = 211frames }\ntrack vox%d audio {}\n", track, track, hash.Sum(nil), frames, rate, track, track, frames-113, track)
	}
	source.WriteString("arrange {\n")
	// Ninety bars at120BPM are three minutes, independent of source rate.
	for track := 0; track < tracks; track++ {
		fmt.Fprintf(&source, "place p%d vox%d c%d { at = 0bars length = 90bars }\n", track, track, track)
	}
	source.WriteString("marker end { at = 90bars }\n}\n")
	score, ds := notation.Parse([]byte(source.String()))
	p, more := project.FromScore(score)
	if p == nil {
		t.Fatalf("fixture parse %+v %+v\n%s", ds, more, source.String())
	}
	return root, p, score
}
func prepare(t *testing.T, e *engine.Engine, n int) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames, err := e.PrepareRender(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	return frames
}
func drain(t *testing.T, e *engine.Engine) {
	t.Helper()
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			t.Fatal(message)
		}
	}
}
func TestThreeMinuteLayeredVocalsStreamBeyondResidentBudget(t *testing.T) {
	const frames = 180 * 48000
	root, p, _ := streamingFixture(t, frames, 48000, 3)
	if int64(frames*3*4) <= 64<<20 {
		t.Fatal("fixture must exceed resident PCM budget")
	}
	if _, err := schedule.CompileResident(p, root, 48000, 128); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("portable resident budget bypassed: %v", err)
	}
	cfg, err := schedule.Compile(p, root, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range cfg.Assets {
		if len(asset.Left) != 0 || len(asset.Right) != 0 || asset.StreamID == 0 {
			t.Fatal("long vocal decoded into resident memory")
		}
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if stats := e.StreamStats(); stats.ArenaBytes <= 0 || stats.ArenaBytes > 16<<20 {
		t.Fatalf("unbounded PCM arena: %+v", stats)
	}
	t.Logf("three3minute48kmono layers: decodedPCM=%dbytes (%.2fMiB), nativearena=%dbytes (%.2fMiB)", frames*3*4, float64(frames*3*4)/(1<<20), e.StreamStats().ArenaBytes, float64(e.StreamStats().ArenaBytes)/(1<<20))
	var l, r [128]float32
	for _, bar := range []uint32{0, 44, 89, 2, 89} {
		if !e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: bar}) || !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
			t.Fatal("seek rejected")
		}
		peak := float32(0)
		for range 12 {
			n := prepare(t, e, len(l))
			e.Render(l[:n], r[:n])
			drain(t, e)
			for _, v := range l[:n] {
				peak = max(peak, float32(math.Abs(float64(v))))
			}
		}
		if peak == 0 {
			t.Fatalf("layered vocals silent at bar%d", bar+1)
		}
		if stats := e.StreamStats(); stats.MissingFrames != 0 || stats.ReadErrors != 0 {
			t.Fatalf("primed seek lost PCM: %+v", stats)
		}
	}
}
func TestStreamedClipsMatchResidentSRCSeekOverlapAndLoop(t *testing.T) {
	for _, rates := range [][2]int{{48000, 48000}, {44100, 48000}, {96000, 44100}, {192000, 48000}, {48000, 96000}} {
		t.Run(fmt.Sprintf("%d-to-%d", rates[0], rates[1]), func(t *testing.T) {
			root, p, _ := streamingFixture(t, 300000, rates[0], 1)
			streamed, err := schedule.Compile(p, root, rates[1], 4096)
			if err != nil {
				t.Fatal(err)
			}
			if streamed.Streaming == nil {
				t.Fatal("fixture did not use streaming")
			}
			resident, err := schedule.CompileResident(p, root, rates[1], 4096)
			if err != nil {
				t.Fatal(err)
			}
			// Add an overlapping region and loop at three bars to exercise boundary
			// prefetch across source starts and transport restart.
			for _, cfg := range []*engine.Config{&streamed, &resident} {
				cfg.LoopSong = true
				cfg.Schedule = []engine.ScheduleEvent{{Tick: 0, EndTick: 7680, Kind: engine.ScheduleClip, ID: 1}, {Tick: 240, EndTick: 2160, Kind: engine.ScheduleClip, ID: 2}, {Tick: 2160, EndTick: 2160, Kind: engine.ScheduleClipEnd, ID: 2}, {Tick: 7680, EndTick: 7680, Kind: engine.ScheduleClipEnd, ID: 1}}
			}
			a, err := engine.New(streamed)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := engine.New(resident)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			var al, ar, bl, br [4096]float32
			for _, tick := range []uint32{0, 720, 7400, 960} {
				commands := [2]cmd.Command{{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(int64(tick) / seq.TicksPerBar), Arg1: uint32(int64(tick) % seq.TicksPerBar)}, {Op: cmd.OpPlay, Track: 0xff}}
				a.PushBatch(commands[:])
				b.PushBatch(commands[:])
				for block := 0; block < 9; block++ {
					n := prepare(t, a, 4096)
					a.Render(al[:n], ar[:n])
					b.Render(bl[:n], br[:n])
					drain(t, a)
					drain(t, b)
					for i := 0; i < n; i++ {
						if al[i] != bl[i] || ar[i] != br[i] {
							t.Fatalf("tick%d block%d frame%d: streamed %g/%g resident %g/%g", tick, block, i, al[i], ar[i], bl[i], br[i])
						}
					}
				}
			}
			if stats := a.StreamStats(); stats.MissingFrames != 0 {
				t.Fatalf("SRC/loop missed frames: %+v", stats)
			}
		})
	}
}

func TestStreamedOfflineWAVAndStemsMatchResidentEngine(t *testing.T) {
	root, p, _ := streamingFixture(t, 300000, 48000, 3)
	for i := range p.Arrange.Placements {
		p.Arrange.Placements[i].LengthTicks = 3 * seq.TicksPerBar
	}
	p.Arrange.Markers[0].AtTick = 3 * seq.TicksPerBar
	// Formatting/reparsing exercises the actual exported score path.
	source, err := project.ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	if score == nil {
		t.Fatal(ds)
	}
	for _, block := range []int{128, 4096} {
		var wav bytes.Buffer
		report, err := render.WAV(score, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Block: block}, &wav)
		if err != nil {
			t.Fatal(err)
		}
		header, err := audioasset.ReadWAVHeader(bytes.NewReader(wav.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := schedule.CompileResident(p, root, 48000, block)
		if err != nil {
			t.Fatal(err)
		}
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		end := report.Frames
		latency := int64(e.LatencyFrames())
		left, right := make([]float32, block), make([]float32, block)
		for at := int64(0); at < end+latency; {
			n := int(min(int64(block), end+latency-at))
			if at < end {
				n = int(min(int64(n), end-at))
			} else if at == end {
				e.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
			}
			e.Render(left[:n], right[:n])
			drain(t, e)
			for i := 0; i < n; i++ {
				frame := at + int64(i) - latency
				if frame < 0 {
					continue
				}
				offset := header.DataOffset + frame*8
				l := math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[offset:]))
				r := math.Float32frombits(binary.LittleEndian.Uint32(wav.Bytes()[offset+4:]))
				if l != left[i] || r != right[i] {
					t.Fatalf("offline block%d frame%d: stream %g/%g resident %g/%g", block, frame, l, r, left[i], right[i])
				}
			}
			at += int64(n)
		}
	}
	stemDir := filepath.Join(root, "stems")
	if _, err := render.Stems(score, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Block: 4096}, stemDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01-vox0.wav", "02-vox1.wav", "03-vox2.wav"} {
		data, err := os.ReadFile(filepath.Join(stemDir, name))
		if err != nil {
			t.Fatal(err)
		}
		header, err := audioasset.ReadWAVHeader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		nonzero := false
		for frame := int64(0); frame < header.Frames; frame++ {
			if binary.LittleEndian.Uint32(data[header.DataOffset+frame*8:]) != 0 {
				nonzero = true
				break
			}
		}
		if !nonzero {
			t.Fatalf("streamed stem %s silent", name)
		}
	}
}

func TestThreeMinuteSceneVocalsExportBeyondResidentBudget(t *testing.T) {
	root, _, score := streamingFixture(t, 180*48000, 48000, 3)
	score.Arrange = nil
	score.Scenes = []notation.Scene{{Name: "main", Bindings: []notation.Binding{{Track: "vox0", Pattern: "c0"}, {Track: "vox1", Pattern: "c1"}, {Track: "vox2", Pattern: "c2"}}}}
	score.Song = []notation.SongEntry{{Scene: "main", Bars: 90}}
	var wav bytes.Buffer
	report, err := render.WAV(score, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Block: 4096, Bars: 1}, &wav)
	if err != nil {
		t.Fatal(err)
	}
	if report.Frames != 96000 || report.OutputPeak <= 0 {
		t.Fatalf("long scene vocal export was empty: %+v", report)
	}
}

func TestStreamedExportReportsPageErrorsAndAbortsStems(t *testing.T) {
	root, p, _ := streamingFixture(t, 300000, 48000, 1)
	data := testwav.Bytes(48000, 1, 32, 300000, 3)
	binary.LittleEndian.PutUint32(data[44+6000*4:], math.Float32bits(float32(math.NaN())))
	if err := os.WriteFile(filepath.Join(root, "take0.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	p.Assets[0].SHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	p.Arrange.Placements[0].LengthTicks = seq.TicksPerBar
	p.Arrange.Markers[0].AtTick = seq.TicksPerBar
	source, err := project.ToSource(p)
	if err != nil {
		t.Fatal(err)
	}
	score, _ := notation.Parse(source)
	var wav bytes.Buffer
	if _, err := render.WAV(score, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Block: 4096}, &wav); err == nil || !strings.Contains(err.Error(), "non-finite streamed PCM") {
		t.Fatalf("invalid page became silent successful export: %v", err)
	}
	stems := filepath.Join(root, "failed-stems")
	if _, err := render.Stems(score, render.Options{AssetRoot: root, SampleRate: 48000, Bits: 32, Block: 4096}, stems); err == nil {
		t.Fatal("invalid page became successful stems")
	}
	if _, err := os.Stat(stems); !os.IsNotExist(err) {
		t.Fatalf("failed stems were published: %v", err)
	}
}
