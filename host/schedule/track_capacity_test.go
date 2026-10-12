//go:build !tinygo && !js

package schedule_test

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

func TestNativeSixtyFourStreamedTracksUseBoundedReaders(t *testing.T) {
	root, p, _ := streamingFixture(t, 300000, 48000, 1)
	track, placement := p.Tracks[0], p.Arrange.Placements[0]
	p.Tracks = make([]project.Track, 64)
	p.Arrange.Placements = make([]project.Placement, 64)
	for i := 0; i < 64; i++ {
		p.Tracks[i] = track
		p.Tracks[i].ID = fmt.Sprintf("vox%d", i)
		p.Tracks[i].Mixer.GainDB = -42
		p.Arrange.Placements[i] = placement
		p.Arrange.Placements[i].ID = fmt.Sprintf("p%d", i)
		p.Arrange.Placements[i].Track = p.Tracks[i].ID
	}
	cfg, err := schedule.Compile(p, root, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tracks != 64 || cfg.MaxVoices != 64 || cfg.Streaming == nil {
		t.Fatalf("streaming capacity: tracks%d voices%d streamed%v", cfg.Tracks, cfg.MaxVoices, cfg.Streaming != nil)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if stats := e.StreamStats(); stats.ArenaBytes <= 0 || stats.ArenaBytes > 32<<20 {
		t.Fatalf("unbounded64-trackcache: %+v", stats)
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
	var l, r [128]float32
	lastTrackAudible := false
	for block := 0; block < 20; block++ {
		n := prepare(t, e, 128)
		e.Render(l[:n], r[:n])
		var message cmd.Message
		for e.Poll(&message) {
			if message.Kind == cmd.Fault {
				t.Fatal(message)
			}
			if message.Kind == cmd.Meter && message.Track == 63 && message.A == 0 && math.Float32frombits(message.B) > 0 {
				lastTrackAudible = true
			}
		}
	}
	if !lastTrackAudible {
		t.Fatal("last streamedtrack silent or omittedfrommeters")
	}
	if stats := e.StreamStats(); stats.MissingFrames != 0 {
		t.Fatalf("streaming gaps: %+v", stats)
	}
	t.Logf("64streamedtracks: nativearena%dbytes (%.2fMiB), zero missingframes", e.StreamStats().ArenaBytes, float64(e.StreamStats().ArenaBytes)/(1<<20))
}
