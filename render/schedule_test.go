package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestScheduledOfflineNativePCMAndStems(t *testing.T) {
	root := t.TempDir()
	asset := testwav.Bytes(48000, 2, 16, 24000, 1)
	if err := os.WriteFile(filepath.Join(root, "clip.wav"), asset, 0600); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`cicada 2
tempo 137.123
asset a "clip.wav" { sha256 = "%x" format = wav frames = 24000 rate = 48000Hz channels = 2 }
clip region a { start = 3frames end = 20003frames fade_in = 17frames fade_out = 29frames }
track vox audio {}
arrange {
 place vox-1 vox region { at = 240ticks length = 960ticks }
 place vox-2 vox region { at = 480ticks length = 960ticks }
 marker end { at = 1440ticks }
}
`, sha256.Sum256(asset))
	score, ds := notation.Parse([]byte(source))
	p, ds := project.FromScore(score)
	if p == nil {
		t.Fatal(ds)
	}
	var reference []byte
	for _, block := range []int{1, 128, 4096} {
		var wav bytes.Buffer
		report, err := WAV(score, Options{AssetRoot: root, Bits: 32, Block: block, TailSec: .02}, &wav)
		if err != nil {
			t.Fatal(err)
		}
		if block == 1 {
			reference = append([]byte(nil), wav.Bytes()...)
		} else if !bytes.Equal(reference, wav.Bytes()) {
			t.Fatal("offline clip PCM depends on block size", block)
		}
		cfg, err := schedule.Compile(p, root, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		native, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		events, err := project.CompileSchedule(p)
		if err != nil {
			t.Fatal(err)
		}
		last := events[len(events)-1].Tick
		clock, err := seq.NewClock(48000, int64(p.TempoMilli))
		if err != nil {
			t.Fatal(err)
		}
		end := clock.SampleAtTick(last)
		native.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		var l, r [1]float32
		var message cmd.Message
		latency := int64(native.LatencyFrames())
		pcm := wav.Bytes()[44:]
		for frame := int64(0); frame < report.Frames+latency; frame++ {
			if frame == end {
				native.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
			}
			native.Render(l[:], r[:])
			for native.Poll(&message) {
				if message.Kind == cmd.Fault {
					t.Fatal(message)
				}
			}
			if frame < latency {
				continue
			}
			i := int(frame-latency) * 8
			if got := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:])); got != l[0] {
				t.Fatalf("offline/native left drift at %d: %g/%g", frame, got, l[0])
			}
			if got := math.Float32frombits(binary.LittleEndian.Uint32(pcm[i+4:])); got != r[0] {
				t.Fatal("offline/native right drift", frame)
			}
		}
	}
	dir := filepath.Join(root, "stems")
	if _, err := Stems(score, Options{AssetRoot: root, Bits: 32, Block: 128, TailSec: .02}, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyStems(nil, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatal(err)
	}
}
