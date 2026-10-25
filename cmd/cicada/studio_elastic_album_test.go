package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/takejournal"
)

// The album fixture records Cicada's own original melodic guide into a retained
// Session take. It never opens a microphone or claims to contain Danilo's voice.
func TestElasticAlbumWorkflow(t *testing.T) {
	isolateState(t)
	root := os.Getenv("CICADA_ELASTIC_ALBUM_FIXTURE")
	if root == "" {
		root = filepath.Join(t.TempDir(), "anywhere-you-are")
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../../examples/songs/anywhere-you-are.cicada")
	if err != nil {
		t.Fatal(err)
	}
	// This project is an instrumental with one retained, synthesized guide.
	source = []byte(strings.Replace(string(source), "track guide lanternvoice {", "track guide lanternvoice { mute=on", 1))
	path := filepath.Join(root, "anywhere-you-are.cicada")
	if err = os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project anywhere-you-are\ncicada 2\nentry \"anywhere-you-are.cicada\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.shutdown()
	if err = s.openTakes(); err != nil {
		t.Fatal(err)
	}
	initial, err := compileLiveScore(path)
	if err != nil {
		t.Fatal(err)
	}
	initial.Range = &liveplay.PlaybackRange{StartTick: 24 * 3840, EndTick: 28 * 3840}
	player, err := liveplay.New(initial, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer player.Close()
	p, err := compileStudioSource(path, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, track := range p.Tracks {
		index, ok := player.TrackIndex(track.ID)
		if !ok {
			t.Fatal(track.ID)
		}
		if err = player.SetMute(index, track.ID != "guide"); err != nil {
			t.Fatal(err)
		}
	}
	timeline := &takejournal.Timeline{StartTick: 24 * 3840, EndTick: 28 * 3840, PlaybackStartTick: 24 * 3840, TempoMilli: 108000}
	clock, _ := timeline.Clock(48000)
	frames := clock.SampleAtTick(timeline.EndTick) - clock.SampleAtTick(timeline.StartTick)
	id, err := s.takes.BeginTimeline("danilo", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4096*8)
	mono := make([]float32, 4096)
	var energy float64
	for at := int64(0); at < frames; {
		n := min(int64(len(mono)), frames-at)
		if _, err = io.ReadFull(player, buffer[:n*8]); err != nil {
			t.Fatal(err)
		}
		for i := int64(0); i < n; i++ {
			l := math.Float32frombits(binary.LittleEndian.Uint32(buffer[i*8:]))
			r := math.Float32frombits(binary.LittleEndian.Uint32(buffer[i*8+4:]))
			mono[i] = l*.5 + r*.5
			energy += float64(mono[i]) * float64(mono[i])
		}
		record := capture.RecordedBlock{RawFrame: uint64(at), Timing: capture.Block{SampleRate: 48000, Frames: int(n)}, Placement: capture.Placement{EngineFrame: at}}
		if err = s.takes.Write(id, record, [][]float32{mono[:n]}); err != nil {
			t.Fatal(err)
		}
		at += n
	}
	if energy < 1e-5 {
		t.Fatal("album guide is silent")
	}
	if err = s.takes.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err = s.takes.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, err := s.takes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	follow := true
	selection, env, err := s.vocalTakeResult(source, take, &studioRecordingSettings{Elastic: &follow})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTakeFiles(id, studioRevision(source), selection.Source, &env, selection.Files, ""); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(path)
	currentProject, err := compileStudioSource(path, current)
	if err != nil {
		t.Fatal(err)
	}
	if studioArrangementEnd(currentProject) != 80*3840 {
		t.Fatal("comp conversion changed album form")
	}
	manifest := map[string]any{"title": "Anywhere You Are", "score": path, "takeId": id, "track": "danilo", "tempoMilli": 108000, "startTick": 24 * 3840, "endTick": 28 * 3840, "frames": frames, "rawSHA256": take.Asset.SHA256, "sourceRevision": studioRevision(current), "syntheticGuide": true, "humanVocalRecorded": false}
	data, _ := json.MarshalIndent(manifest, "", "  ")
	if err = os.WriteFile(filepath.Join(root, "fixture.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Original album fixture: %s; retained synthetic guide %s", path, id)
}
