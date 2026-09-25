package liveplay

import (
	"io"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
)

func testScore(t *testing.T, name string, bpmMilli int64) Score {
	t.Helper()
	cfg := engine.Config{SampleRate: 48_000, MaxBlock: blockFrames, Tracks: 1, MaxVoices: 1, BPMMilli: bpmMilli}
	cfg.Track[0].Kind = engine.VoiceAcid
	created, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return Score{Engine: created, SampleRate: 48_000, BPMMilli: bpmMilli, Name: name}
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

func slotScore(t *testing.T, pattern string, slot int) Score {
	t.Helper()
	score := testScore(t, "slots", 120_000)
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
