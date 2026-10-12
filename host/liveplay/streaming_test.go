package liveplay

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

type countedStreamingFactory struct {
	plan    engine.StreamingFactory
	runtime *countedStreamingRuntime
}
type countedStreamingRuntime struct {
	engine.StreamingRuntime
	retires, closes atomic.Uint64
}

func (f *countedStreamingFactory) New(n, rate, block int) (engine.StreamingRuntime, error) {
	runtime, err := f.plan.New(n, rate, block)
	if err != nil {
		return nil, err
	}
	f.runtime = &countedStreamingRuntime{StreamingRuntime: runtime}
	return f.runtime, nil
}
func (r *countedStreamingRuntime) Retire()      { r.retires.Add(1); r.StreamingRuntime.Retire() }
func (r *countedStreamingRuntime) Close() error { r.closes.Add(1); return r.StreamingRuntime.Close() }
func liveStreamingFixture(t *testing.T) (Score, *countedStreamingRuntime) {
	t.Helper()
	root := t.TempDir()
	data := testwav.Bytes(48000, 1, 16, 300000, 1)
	if err := os.WriteFile(filepath.Join(root, "take.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := sampleasset.PrepareStreamPlan(root, []project.Asset{{Name: "take", Path: "take.wav", Format: "wav", Frames: 300000, RateHz: 48000, Channels: 1, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}}, []uint32{1})
	if err != nil {
		t.Fatal(err)
	}
	factory := &countedStreamingFactory{plan: plan}
	cfg := engine.Config{SampleRate: 48000, MaxBlock: 4096, Tracks: 1, MaxVoices: 32, BPMMilli: 120000, Streaming: factory, LoopSong: true}
	cfg.Track[0] = engine.TrackConfig{Kind: engine.VoiceAudio, GainSet: true}
	cfg.Assets = []engine.AudioAsset{{StreamID: 1, Frames: 300000, SampleRate: 48000}}
	cfg.Clips = []engine.ClipConfig{{EndFrame: 300000}}
	cfg.Schedule = []engine.ScheduleEvent{{EndTick: 15360, Kind: engine.ScheduleClip, ID: 1}, {Tick: 15360, EndTick: 15360, Kind: engine.ScheduleClipEnd, ID: 1}}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close(); factory.runtime.Close() })
	return Score{Engine: e, SampleRate: 48000, BPMMilli: 120000, Name: "stream-test"}, factory.runtime
}
func TestStreamedPlayerClosesDiscardedPendingCurrentAndRejectedScores(t *testing.T) {
	first, firstRuntime := liveStreamingFixture(t)
	player, err := New(first, 48000)
	if err != nil {
		t.Fatal(err)
	}
	second, secondRuntime := liveStreamingFixture(t)
	if err := player.Offer(second); err != nil {
		t.Fatal(err)
	}
	third, thirdRuntime := liveStreamingFixture(t)
	if err := player.Offer(third); err != nil {
		t.Fatal(err)
	}
	if secondRuntime.closes.Load() == 0 {
		t.Fatal("superseded pending engine retained streaming resources")
	}
	player.Close()
	if firstRuntime.closes.Load() == 0 || thirdRuntime.closes.Load() == 0 {
		t.Fatal("player teardown did not close current/pending engines")
	}
	late, lateRuntime := liveStreamingFixture(t)
	if err := player.Offer(late); err == nil || lateRuntime.closes.Load() == 0 {
		t.Fatal("closed player admitted/leaked a score")
	}
}
func TestStreamedPlayerRetiresEngineAfterCrossfadeAndRangeReplacement(t *testing.T) {
	first, firstRuntime := liveStreamingFixture(t)
	player, err := New(first, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	second, secondRuntime := liveStreamingFixture(t)
	if err := player.Offer(second); err != nil {
		t.Fatal(err)
	}
	var pcm [2048]byte
	for i := 0; i < 400; i++ {
		if _, err := io.ReadFull(player, pcm[:]); err != nil {
			t.Fatal(err)
		}
	}
	if firstRuntime.retires.Load() == 0 {
		t.Fatal("completed edit fade did not retire old stream")
	}
	third, thirdRuntime := liveStreamingFixture(t)
	third.Range = &PlaybackRange{StartTick: 240, EndTick: 480, Loop: true}
	if err := player.Offer(third); err != nil {
		t.Fatal(err)
	}
	if err := player.PlayRange(*third.Range); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(player, pcm[:]); err != nil {
		t.Fatal(err)
	}
	if secondRuntime.retires.Load() == 0 {
		t.Fatal("listening range replacement retained old engine")
	}
	player.Close()
	if thirdRuntime.closes.Load() == 0 {
		t.Fatal("range engine not closed on teardown")
	}
}

// Compile-time assertions keep the native lifecycle wrappers aligned with the
// engine's explicit readiness and bounded cache telemetry contract.
var _ engine.StreamingRuntime = (*countedStreamingRuntime)(nil)
