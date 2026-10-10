package liveplay

import (
	"io"
	"math"
	"testing"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/graph"
)

func testScore(t *testing.T, name string, bpmMilli int64, scenes ...engine.Scene) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: bpmMilli}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Scenes = scenes
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: bpmMilli, Name: name}
}

func meterScore(t *testing.T, name string, gain float64) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000}
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Len, program.Output = 2, 1
	cfg.Track[0].Kind, cfg.Track[0].Graph = engine.VoiceGraph, program
	cfg.Track[0].GainDB, cfg.Track[0].GainSet = gain, true
	cfg.Track[0].Pan, cfg.Track[0].BusSFX = -1, true
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: name,
		Tracks: []TrackSlots{{ID: "bass"}}, HasSFX: true,
		Parameters: []ParameterValue{{Track: 0, ID: kernel.ParamMixGain, Value: float32(gain)}},
	}
}

func TestCommittedValueClearsMatchingOverrideOnTheAudioThread(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.SetParam(0, kernel.ParamMixGain, -3); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, blockFrames*8); err != nil {
		t.Fatal(err)
	}
	snapshot := p.overrides.Load()
	version := snapshot.values[0][kernel.ParamMixGain].version
	p.noteCommitted(0, kernel.ParamMixGain, -3) // The test owns Read.
	if p.clearedVersions[0][kernel.ParamMixGain] != version {
		t.Fatal("matching committed value did not clear the override")
	}
	if p.overrides.Load() != snapshot {
		t.Fatal("committed value changed the control-thread snapshot")
	}
	p.noteCommitted(0, kernel.ParamMixGain, -4)
	if p.clearedVersions[0][kernel.ParamMixGain] != version {
		t.Fatal("a different committed value must not clear a newer override")
	}
	if err := p.SetParam(0, kernel.ParamMixGain, -5); err != nil {
		t.Fatal(err)
	}
	p.noteCommitted(0, kernel.ParamMixGain, -3)
	if p.clearedVersions[0][kernel.ParamMixGain] == p.overrides.Load().values[0][kernel.ParamMixGain].version {
		t.Fatal("stale commit cleared a newer gesture")
	}
}

func TestCommittedOverrideUsesExactBitsAndGlobalSlotWithoutAllocating(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.SetParam(0, kernel.ParamMixGain, 0); err != nil {
		t.Fatal(err)
	}
	p.noteCommitted(0, kernel.ParamMixGain, float32(math.Copysign(0, -1)))
	if p.clearedVersions[0][kernel.ParamMixGain] != 0 {
		t.Fatal("different float bits cleared an override")
	}
	if err := p.SetParam(0xff, kernel.ParamFxDelayFeedback, .4); err != nil {
		t.Fatal(err)
	}
	version := p.overrides.Load().values[16][kernel.ParamFxDelayFeedback].version
	if allocs := testing.AllocsPerRun(100, func() {
		p.noteCommitted(0xff, kernel.ParamFxDelayFeedback, .4)
	}); allocs != 0 {
		t.Fatalf("committed override clear allocated %g", allocs)
	}
	if p.clearedVersions[16][kernel.ParamFxDelayFeedback] != version {
		t.Fatal("matching global value did not clear its override")
	}
}

func TestLiveParameterOverrideSurvivesOfferedScore(t *testing.T) {
	initial := meterScore(t, "initial", -6)
	p, err := New(initial, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetParam(0, kernel.ParamMixGain, 0); err != nil {
		t.Fatal(err)
	}
	offered := meterScore(t, "offered", -3)
	if err := p.Offer(offered); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, int64(100_000*8)); err != nil {
		t.Fatal(err)
	}
	if p.current.Name != "offered" {
		t.Fatalf("offered score did not land: %s", p.current.Name)
	}
	select {
	case frame := <-p.Meters():
		if frame.TrackCount != 1 || frame.TrackIDs[0] != "bass" {
			t.Fatalf("meter track metadata: %+v", frame)
		}
		if frame.Tracks[0].Peak < .95 {
			t.Fatalf("live gain override did not reach offered engine; track peak=%g", frame.Tracks[0].Peak)
		}
	default:
		t.Fatal("render thread did not deliver meter data")
	}
	override := p.overrides.Load().values[0][kernel.ParamMixGain]
	if !override.active || p.appliedVersions[0][kernel.ParamMixGain] != override.version || p.clearedVersions[0][kernel.ParamMixGain] == override.version {
		t.Fatalf("offered score lost live override: override=%+v applied=%d cleared=%d", override, p.appliedVersions[0][kernel.ParamMixGain], p.clearedVersions[0][kernel.ParamMixGain])
	}
}

func TestLiveParameterOverrideFollowsTrackIDAcrossReorderedScore(t *testing.T) {
	initial := reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12)
	p, err := New(initial, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetParam(0, kernel.ParamMixGain, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(reorderedMeterScore(t, "offered", [2]string{"lead", "bass"}, -12, -6)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, int64(100_000*8)); err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-p.Meters():
		if frame.TrackIDs[0] != "lead" || frame.TrackIDs[1] != "bass" || frame.Tracks[1].Peak < .95 || frame.Tracks[0].Peak > .3 {
			t.Fatalf("override did not follow bass through score reorder: %+v", frame)
		}
	default:
		t.Fatal("reordered score emitted no meter frame")
	}
}

func TestPreviewOverrideAppliesOnTheNextBlock(t *testing.T) {
	p, err := New(meterScore(t, "preview", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var pcm [blockFrames * 8]byte
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	initialPeak := float64(0)
	for _, sample := range p.left {
		initialPeak = max(initialPeak, math.Abs(float64(sample)))
	}
	if err := p.SetParam(0, kernel.ParamMixGain, 0); err != nil {
		t.Fatal(err)
	}
	version := p.overrides.Load().values[0][kernel.ParamMixGain].version
	if _, err := p.Read(pcm[:]); err != nil {
		t.Fatal(err)
	}
	if p.appliedVersions[0][kernel.ParamMixGain] != version {
		t.Fatal("preview did not reach the engine on the next block")
	}
	previewPeak := float64(0)
	for _, sample := range p.left {
		previewPeak = max(previewPeak, math.Abs(float64(sample)))
	}
	if previewPeak <= initialPeak {
		t.Fatalf("preview did not change the next block's audio: peak %g, initial %g", previewPeak, initialPeak)
	}
}

func TestPreviewValuesFollowThePlayingScoreSnapshot(t *testing.T) {
	p, err := New(reorderedMeterScore(t, "initial", [2]string{"bass", "lead"}, -6, -12), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.SetParam(0, kernel.ParamMixGain, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(reorderedMeterScore(t, "offered", [2]string{"lead", "bass"}, -12, -3)); err != nil {
		t.Fatal(err)
	}
	if value, ok := p.CommittedValue(0, kernel.ParamMixGain); !ok || value != -6 {
		t.Fatalf("pending score changed the committed value: %g, %v", value, ok)
	}
	if _, err := io.CopyN(io.Discard, p, 100_000*8); err != nil {
		t.Fatal(err)
	}
	track, ok := p.TrackIndex("bass")
	if !ok || track != 1 {
		t.Fatalf("playing bass track: %d, %v", track, ok)
	}
	if value, ok := p.OverrideValue(track, kernel.ParamMixGain); !ok || value != 0 {
		t.Fatalf("override did not follow the playing bass track: %g, %v", value, ok)
	}
	if value, ok := p.CommittedValue(track, kernel.ParamMixGain); !ok || value != -3 {
		t.Fatalf("committed value did not follow the playing bass track: %g, %v", value, ok)
	}
	if _, ok := p.OverrideValue(0, kernel.ParamMixGain); ok {
		t.Fatal("bass override leaked to the lead track")
	}
}

func reorderedMeterScore(t *testing.T, name string, ids [2]string, firstGain, secondGain float64) Score {
	t.Helper()
	program := graph.Program{}
	program.Nodes[0] = graph.Node{Op: graph.Constant, Value: 375}
	program.Nodes[1] = graph.Node{Op: graph.Sine, A: 0}
	program.Len, program.Output = 2, 1
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 2, MaxVoices: 2, BPMMilli: 120_000}
	cfg.Track[0].Kind, cfg.Track[0].Graph, cfg.Track[0].GainDB, cfg.Track[0].GainSet = engine.VoiceGraph, program, firstGain, true
	cfg.Track[1].Kind, cfg.Track[1].Graph, cfg.Track[1].GainDB, cfg.Track[1].GainSet = engine.VoiceGraph, program, secondGain, true
	cfg.Track[0].Pan, cfg.Track[1].Pan = -1, -1
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{
		Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: name,
		Tracks:     []TrackSlots{{ID: ids[0]}, {ID: ids[1]}},
		Parameters: []ParameterValue{{Track: 0, ID: kernel.ParamMixGain, Value: float32(firstGain)}, {Track: 1, ID: kernel.ParamMixGain, Value: float32(secondGain)}},
	}
}

func TestLiveParameterControlsKeepLastValueAcrossConcurrentCalls(t *testing.T) {
	p, err := New(meterScore(t, "controls", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 8)
	for i := range 8 {
		go func(index int) { done <- p.SetParam(0, kernel.ParamMixGain, float32(-7+index)) }(i)
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := p.SetMute(0, true); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSolo(0, true); err != nil {
		t.Fatal(err)
	}
	snapshot := p.overrides.Load()
	if got := snapshot.values[0][kernel.ParamMixGain].value; got < -7 || got > 0 {
		t.Fatalf("latest concurrent gain is out of range: %g", got)
	}
	if snapshot.values[0][kernel.ParamMixMute].value != 1 || snapshot.values[0][kernel.ParamMixSolo].value != 1 {
		t.Fatal("mute or solo control was lost")
	}
	if err := p.SetParam(0xff, kernel.ParamMixGain, 0); err == nil {
		t.Fatal("accepted a global route for a track parameter")
	}
	if err := p.SetParam(0, kernel.ParamMixGain, float32(math.Inf(1))); err == nil {
		t.Fatal("accepted positive infinity")
	}
	if err := p.SetParam(1, kernel.ParamMixGain, 0); err == nil {
		t.Fatal("accepted a parameter for a missing track")
	}
}

func TestValidatedEditsReplaceAtExactBarAndUseNewTempo(t *testing.T) {
	p, err := New(testScore(t, "first", 120_000), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, (96_000-1)*8); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(testScore(t, "superseded", 120_000)); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(testScore(t, "latest", 60_000)); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("edit landed before bar boundary: %+v", event)
	default:
	}
	// A one-byte read must start the next frame and land the newest edit.
	if _, err := io.ReadFull(p, frame[:1]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		if event.Bar != 2 || event.Name != "latest" {
			t.Fatalf("wrong landing: %+v", event)
		}
	default:
		t.Fatal("validated edit did not land at bar 2")
	}
	if _, err := io.ReadFull(p, frame[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, (192_000-2)*8); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(testScore(t, "bar three", 60_000)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("new-tempo edit landed early: %+v", event)
	default:
	}
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		if event.Bar != 3 || event.Name != "bar three" {
			t.Fatalf("wrong bar-three landing: %+v", event)
		}
	default:
		t.Fatal("new-tempo bar was not reached")
	}
}

func TestLivePlayerRejectsMismatchedSampleRate(t *testing.T) {
	wrong := testScore(t, "wrong", 120_000)
	wrong.SampleRate = 44_100
	if _, err := New(wrong, 48_000); err == nil {
		t.Fatal("accepted a score built for a different sample rate")
	}
	p, err := New(testScore(t, "initial", 120_000), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(wrong); err == nil {
		t.Fatal("queued a score built for a different sample rate")
	}
}

func TestPositionTracksRenderedBarAndStep(t *testing.T) {
	p, err := New(testScore(t, "position", 120_000), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Position(); got != (Position{Bar: 1, Step: 1}) {
		t.Fatalf("initial position: %+v", got)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := io.ReadFull(p, frame[:]); err != nil {
		t.Fatal(err)
	}
	if got := p.Position(); got != (Position{Bar: 2, Step: 1}) {
		t.Fatalf("second bar position: %+v", got)
	}
}

func sceneScore(t *testing.T, ids ...string) Score {
	t.Helper()
	score := testScore(t, "scene score", 120_000)
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000, Scenes: make([]engine.Scene, len(ids))}
	cfg.Track[0].Kind = engine.VoiceAcid
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	score.Engine, score.SceneIDs = created, ids
	return score
}

func TestSceneLaunchLandsAtBarAndUsesNewestRequest(t *testing.T) {
	p, err := New(sceneScore(t, "intro", "chorus"), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.LaunchScene("intro"); err != nil {
		t.Fatal(err)
	}
	if err := p.LaunchScene("chorus"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("scene launched before bar boundary: %+v", event)
	default:
	}
	var byteAtBoundary [1]byte
	if _, err := p.Read(byteAtBoundary[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		if event.Kind != "scene" || event.Bar != 2 || event.Name != "chorus" {
			t.Fatalf("wrong scene landing: %+v", event)
		}
	default:
		t.Fatal("scene did not land at bar 2")
	}
}

func TestSceneLaunchAfterEditResolvesNewScoreAndRejectsMissingName(t *testing.T) {
	p, err := New(sceneScore(t, "old"), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.LaunchScene("new"); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(sceneScore(t, "other", "new")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "edit" || event.Bar != 2 {
		t.Fatalf("wrong score edit: %+v", event)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("scene launched during score swap: %+v", event)
	default:
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "scene" || event.Bar != 3 || event.Name != "new" {
		t.Fatalf("scene did not resolve against new score: %+v", event)
	}
	if err := p.LaunchScene("old"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "scene-error" || event.Name != "old" {
		t.Fatalf("missing scene was not rejected: %+v", event)
	}
}

func TestCanceledSceneDoesNotLaunchOnResume(t *testing.T) {
	p, err := New(sceneScore(t, "intro"), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.LaunchScene("intro"); err != nil {
		t.Fatal(err)
	}
	p.CancelScene()
	if _, err := io.CopyN(io.Discard, p, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("canceled scene launched: %+v", event)
	default:
	}
}

func slotScore(t *testing.T, pattern string, slot int, scenes ...engine.Scene) Score {
	t.Helper()
	score := testScore(t, "slots", 120_000, scenes...)
	var track TrackSlots
	track.ID = "bass"
	track.Slots[slot] = pattern
	score.Tracks = []TrackSlots{track}
	return score
}

func TestSingleTrackPatternLandsAtBar(t *testing.T) {
	p, err := New(slotScore(t, "riff", 1), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SelectPattern("bass", "riff"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("slot launched early: %+v", event)
	default:
	}
	var one [1]byte
	if _, err := p.Read(one[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		if event.Kind != "slot" || event.Bar != 2 || event.Track != "bass" || event.Name != "riff" {
			t.Fatalf("wrong slot landing: %+v", event)
		}
	default:
		t.Fatal("slot did not launch at bar 2")
	}
}

func TestPatternLaunchResolvesNewSlotAfterScoreSwap(t *testing.T) {
	p, err := New(slotScore(t, "old", 1), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SelectPattern("bass", "riff"); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(slotScore(t, "riff", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "edit" {
		t.Fatalf("wrong first event: %+v", event)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "slot" || event.Bar != 3 || event.Name != "riff" {
		t.Fatalf("new slot did not launch: %+v", event)
	}
	if err := p.SelectPattern("bass", "old"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "slot-error" || event.Name != "old" {
		t.Fatalf("missing slot was not rejected: %+v", event)
	}
}

func TestMultipleTracksKeepLatestSlotPerTrack(t *testing.T) {
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 2, MaxVoices: 2, BPMMilli: 120_000}
	cfg.Track[0].Kind, cfg.Track[1].Kind = engine.VoiceAcid, engine.VoiceAcid
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	score := Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Tracks: []TrackSlots{{ID: "bass", Slots: [16]string{"old", "new"}}, {ID: "lead", Slots: [16]string{"tone"}}}}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []SlotRequest{{"bass", "old"}, {"lead", "tone"}, {"bass", "new"}} {
		if err := p.SelectPattern(request.Track, request.Pattern); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for range 2 {
		select {
		case event := <-p.Events():
			if event.Kind != "slot" {
				t.Fatalf("unexpected slot event: %+v", event)
			}
			seen[event.Track] = event.Name
		default:
			t.Fatal("missing queued track launch")
		}
	}
	if seen["bass"] != "new" || seen["lead"] != "tone" {
		t.Fatalf("wrong launches: %+v", seen)
	}
}

func liveSongScore(t *testing.T, name string, firstBars uint16) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000, LoopSong: true}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Scenes = []engine.Scene{{Track: [16]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 0}}}, {Track: [16]engine.SceneBinding{{Mode: engine.SceneSlot, Slot: 1}}}}
	cfg.Song = []engine.SongEntry{{Scene: 0, Bars: firstBars}, {Scene: 1, Bars: 2}}
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: 120_000, Name: name, Song: []SongEntry{{Scene: "dusk", StartBar: 1}, {Scene: "chorus", StartBar: uint32(firstBars) + 1}}}
}

func TestSongEntryStartsAtItsBarAndReanchorsTransport(t *testing.T) {
	p, err := New(liveSongScore(t, "song", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.StartSongEntry(1, "chorus"); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := p.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "song" || event.Bar != 3 || event.Name != "chorus" {
		t.Fatalf("wrong song start: %+v", event)
	}
	if got := p.Position(); got != (Position{Bar: 3, Step: 1}) {
		t.Fatalf("start position: %+v", got)
	}
	if _, err := io.CopyN(io.Discard, p, (96_000-1)*8); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if got := p.Position(); got.Bar != 4 {
		t.Fatalf("song clock did not advance: %+v", got)
	}
}

func TestSongJumpWaitsForCompleteStereoFrame(t *testing.T) {
	p, err := New(liveSongScore(t, "song", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	var first [3]byte
	if _, err := p.Read(first[:]); err != nil {
		t.Fatal(err)
	}
	if err := p.StartSongEntry(1, "chorus"); err != nil {
		t.Fatal(err)
	}
	var rest [5]byte
	if _, err := p.Read(rest[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("jump split a stereo frame: %+v", event)
	default:
	}
	var next [8]byte
	if _, err := p.Read(next[:]); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "song" || event.Bar != 3 {
		t.Fatalf("jump did not start on next frame: %+v", event)
	}
}

func TestSongJumpSupersedesQueuedManualLaunches(t *testing.T) {
	score := liveSongScore(t, "song", 2)
	score.SceneIDs = []string{"dusk", "chorus"}
	score.Tracks = []TrackSlots{{ID: "bass", Slots: [16]string{"riff"}}}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.LaunchScene("dusk"); err != nil {
		t.Fatal(err)
	}
	if err := p.SelectPattern("bass", "riff"); err != nil {
		t.Fatal(err)
	}
	if err := p.StartSongEntry(1, "chorus"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8+1); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "song" || event.Bar != 3 {
		t.Fatalf("wrong song jump: %+v", event)
	}
	select {
	case event := <-p.Events():
		t.Fatalf("old manual launch survived song jump: %+v", event)
	default:
	}
}

func TestSongJumpDoesNotAllocateInAudioReader(t *testing.T) {
	p, err := New(liveSongScore(t, "song", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	allocs := testing.AllocsPerRun(100, func() {
		if err := p.StartSongEntry(1, "chorus"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Read(frame[:]); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("song jump allocated %.2f times per read", allocs)
	}
}

func TestSongEntryUsesOfferedScoreAndDiscardsBufferedOldAudio(t *testing.T) {
	p, err := New(liveSongScore(t, "old", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := p.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(liveSongScore(t, "new", 4)); err != nil {
		t.Fatal(err)
	}
	if err := p.StartSongEntry(1, "chorus"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "edit" || event.Name != "new" || event.Bar != 5 {
		t.Fatalf("edit did not lead jump: %+v", event)
	}
	if event := <-p.Events(); event.Kind != "song" || event.Bar != 5 {
		t.Fatalf("jump used old song bar: %+v", event)
	}
	if got := p.Position(); got != (Position{Bar: 5, Step: 1}) {
		t.Fatalf("jump position: %+v", got)
	}
}

func TestMissingSongEntryDoesNotConsumePendingEdit(t *testing.T) {
	p, err := New(liveSongScore(t, "old", 2), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Offer(liveSongScore(t, "new", 4)); err != nil {
		t.Fatal(err)
	}
	if err := p.StartSongEntry(1, "missing"); err != nil {
		t.Fatal(err)
	}
	var frame [8]byte
	if _, err := p.Read(frame[:]); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "song-error" {
		t.Fatalf("missing song entry: %+v", event)
	}
	if _, err := io.CopyN(io.Discard, p, (96_000-1)*8+1); err != nil {
		t.Fatal(err)
	}
	if event := <-p.Events(); event.Kind != "edit" || event.Name != "new" {
		t.Fatalf("pending edit lost after invalid request: %+v", event)
	}
}

func TestCurrentSceneFollowsSongOrderAndAutomaticTransitions(t *testing.T) {
	score := liveSongScore(t, "song", 1)
	score.SceneIDs = []string{"dusk", "chorus"}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := p.CurrentScene(); got != "dusk" {
		t.Fatalf("initial scene=%q", got)
	}
	if _, err := io.CopyN(io.Discard, p, 96_000*8+8); err != nil {
		t.Fatal(err)
	}
	if got := p.CurrentScene(); got != "chorus" {
		t.Fatalf("advanced scene=%q", got)
	}
	select {
	case event := <-p.Events():
		if event.Kind != "song-scene" || event.Name != "chorus" || event.Bar != 2 {
			t.Fatalf("transition=%+v", event)
		}
	default:
		t.Fatal("automatic transition was not published")
	}
}

func TestInitialSceneUsesSongOrderInsteadOfDeclarationOrder(t *testing.T) {
	score := liveSongScore(t, "song", 1)
	score.SceneIDs = []string{"chorus", "dusk"}
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: 120_000, LoopSong: true}
	cfg.Track[0].Kind = engine.VoiceAcid
	cfg.Scenes = []engine.Scene{{}, {}}
	cfg.Song = []engine.SongEntry{{Scene: 1, Bars: 1}, {Scene: 0, Bars: 2}}
	var err error
	score.Engine, err = engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(score, 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := p.CurrentScene(); got != "dusk" {
		t.Fatalf("initial scene=%q", got)
	}
	if _, err := io.CopyN(io.Discard, p, 1024); err != nil {
		t.Fatal(err)
	}
	if got := p.CurrentScene(); got != "dusk" {
		t.Fatalf("rendered scene=%q", got)
	}
}

func TestPlayerPublishesTransportFramesWhileRendering(t *testing.T) {
	p, err := New(meterScore(t, "frames", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pub := NewPublisher()
	p.SetPublisher(pub)
	if _, err := io.CopyN(io.Discard, p, 48_000*8); err != nil { // one second = two beats at 120 BPM
		t.Fatal(err)
	}
	p.PublishTelemetry()
	frame, err := DecodeTransportFrame(pub.Latest()[FrameTransport])
	if err != nil || !frame.Playing || frame.SampleFrame < 47_000 || frame.Bar != 1 || frame.Beat != 3 {
		t.Fatalf("transport frame after one second (bar 1, beat 3, both 1-based): %+v %v", frame, err)
	}
	if m, err := DecodeMetersFrame(pub.Latest()[FrameMeters]); err != nil || m.TrackCount != 1 || m.Tracks[0].PeakL <= 0 {
		t.Fatalf("meters frame: %+v %v", m, err)
	}
}

func TestTelemetrySkipsTransportFrameBeforeFirstBlock(t *testing.T) {
	p, err := New(meterScore(t, "early", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pub := NewPublisher()
	p.SetPublisher(pub)
	p.PublishTelemetry()
	if pub.Latest()[FrameTransport] != nil {
		t.Fatal("transport frame published before any block rendered")
	}
}

func TestTelemetryFollowsHostPlayingFlag(t *testing.T) {
	p, err := New(meterScore(t, "pause", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pub := NewPublisher()
	p.SetPublisher(pub)
	if _, err := io.CopyN(io.Discard, p, 48_000*4); err != nil {
		t.Fatal(err)
	}
	p.SetTransportPlaying(false)
	p.PublishTelemetry()
	frame, err := DecodeTransportFrame(pub.Latest()[FrameTransport])
	if err != nil || frame.Playing || frame.SampleFrame == 0 {
		t.Fatalf("paused frame: %+v %v", frame, err)
	}
	p.SetTransportPlaying(true)
	p.PublishTelemetry()
	if frame, _ := DecodeTransportFrame(pub.Latest()[FrameTransport]); !frame.Playing {
		t.Fatalf("resumed frame: %+v", frame)
	}
}

func TestTelemetryKeepsPreviousFrameWhileCounterIsOdd(t *testing.T) {
	p, err := New(meterScore(t, "odd", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pub := NewPublisher()
	p.SetPublisher(pub)
	if _, err := io.CopyN(io.Discard, p, 48_000*4); err != nil {
		t.Fatal(err)
	}
	p.PublishTelemetry()
	before := pub.Latest()
	p.telemetry.seq.Add(1) // simulate a preempted writer
	p.PublishTelemetry()
	after := pub.Latest()
	if string(before[FrameTransport]) != string(after[FrameTransport]) || string(before[FrameMeters]) != string(after[FrameMeters]) {
		t.Fatal("inconsistent read replaced the previous frames")
	}
}

func TestTelemetryDetachedPublisherReceivesNothing(t *testing.T) {
	p, err := New(meterScore(t, "detach", -6), 48_000)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pub := NewPublisher()
	p.SetPublisher(pub)
	if _, err := io.CopyN(io.Discard, p, 48_000*4); err != nil {
		t.Fatal(err)
	}
	p.SetPublisher(nil)
	p.PublishTelemetry()
	if pub.Latest()[FrameTransport] != nil {
		t.Fatal("detached player published")
	}
}
