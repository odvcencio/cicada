package main

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

func TestVocalCompReplacePreservesNeighbors(t *testing.T) {
	old := []vocalSegment{{Take: "a", Start: 0, End: 100}, {Take: "b", Start: 100, End: 200}}
	got := replaceVocalSegment(old, vocalSegment{Take: "c", Start: 40, End: 160})
	want := []vocalSegment{{Take: "a", Start: 0, End: 40}, {Take: "c", Start: 40, End: 160}, {Take: "b", Start: 160, End: 200}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comp %+v", got)
	}
	if old[0].End != 100 || old[1].Start != 100 {
		t.Fatal("mutated old comp")
	}
	got = replaceVocalSegment(got, vocalSegment{Take: "a", Start: 40, End: 160})
	if !reflect.DeepEqual(got, []vocalSegment{{Take: "a", Start: 0, End: 160}, {Take: "b", Start: 160, End: 200}}) {
		t.Fatalf("same-pass join %+v", got)
	}
}

func fakeTimelineTake(t *testing.T, s *studio, start, end int64, frames int, value float32) takejournal.Take {
	t.Helper()
	source, _ := os.ReadFile(s.path)
	timeline := &takejournal.Timeline{StartTick: start, EndTick: end, PlaybackStartTick: max(0, start-seq.TicksPerBar), TempoMilli: 120000}
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 4096)
	for i := range pcm {
		pcm[i] = value
	}
	for at := 0; at < frames; {
		n := min(len(pcm), frames-at)
		b := capture.RecordedBlock{RawFrame: uint64(at), Timing: capture.Block{DeviceEpoch: 1, EngineEpoch: 1, EngineFrame: int64(at), DeviceFrame: uint64(at), SampleRate: 48000, Frames: n, Layout: capture.LayoutMono}, Placement: capture.Placement{EngineFrame: int64(at)}}
		if err = s.takes.Write(id, b, [][]float32{pcm[:n]}); err != nil {
			t.Fatal(err)
		}
		at += n
	}
	if err = s.takes.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err = s.takes.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, _ := s.takes.Get(id)
	return take
}

func TestVocalCompFullSongPunchAndRetainedPasses(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1))
	os.WriteFile(s.path, source, 0600)
	a := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .1)
	if err := s.commitTake(a.ID, a.Expected, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(s.path)
	b := fakeTimelineTake(t, s, seq.TicksPerBar, 2*seq.TicksPerBar, 192000, .7)
	next, err := s.vocalTakeSource(first, b, &studioRecordingSettings{Start: "@2.1.1", End: "@3.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.commitTake(b.ID, b.Expected, next); err != nil {
		t.Fatal(err)
	}
	p, err := compileStudioSource(s.path, next)
	if err != nil {
		t.Fatal(err)
	}
	if p.Arrange == nil || len(p.Song) != 0 || len(p.Assets) != 1 || len(p.Clips) != 1 {
		t.Fatalf("comp source duplicated retained PCM: %d assets, %d clips", len(p.Assets), len(p.Clips))
	}
	comps, err := vocalComps(next)
	if err != nil || len(comps) != 1 || len(comps[0].Segments) != 2 {
		t.Fatalf("comp metadata %+v %v", comps, err)
	}
	raw, err := os.ReadFile(takeRoot(s.path) + "/" + p.Assets[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	sampleAt := func(frame int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(raw[44+frame*4:])) }
	if sampleAt(48000) != .1 || sampleAt(144000) != .7 {
		t.Fatalf("comp lost take coordinates: %f %f", sampleAt(48000), sampleAt(144000))
	}
	if sampleAt(96000) != 0 || sampleAt(95999) != 0 {
		t.Fatal("joins have no de-click fades")
	}
	for _, take := range []takejournal.Take{a, b} {
		if f, err := os.Open(takeRoot(s.path) + "/" + take.Asset.Path); err != nil {
			t.Fatal(err)
		} else {
			f.Close()
		}
	}
	// The first print stays playable for Undo after selecting the second pass.
	old, err := compileStudioSource(s.path, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = schedule.Compile(old, takeRoot(s.path), 48000, 128); err != nil {
		t.Fatalf("Undo audio lost: %v", err)
	}
	if _, err = schedule.Compile(p, takeRoot(s.path), 48000, 128); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.path)
	if _, err = s.vocalTakeSource(before, b, &studioRecordingSettings{Start: "@1.1.1", End: "@2.1.1"}); err == nil {
		t.Fatal("selected outside punch coverage")
	}
}

func TestVocalSceneConversionPreservesBacking(t *testing.T) {
	source := []byte("cicada 2\ntempo 137.333\ntrack lead acid {}\ntrack vox audio {}\npattern one acid steps=4 { 1 . 5 . }\npattern two acid steps=4 { 3 . 6 . }\nscene a { lead=one vox=off }\nscene hold { lead=keep }\nscene b { lead=two }\nscene quiet { lead=off }\nsong { a hold b quiet a }\n")
	dir := t.TempDir()
	s := newTakeStudio(t, dir)
	os.WriteFile(s.path, source, 0600)
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := vocalArrangementSource(source, p)
	if err != nil {
		t.Fatal(err)
	}
	q, err := compileStudioSource(s.path, candidate)
	if err != nil {
		t.Fatal(err)
	}
	makeEngine := func(pSource []byte) *engine.Engine {
		p, err := compileStudioSource(s.path, pSource)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := schedule.Compile(p, dir, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		return e
	}
	a, b := makeEngine(source), makeEngine(candidate)
	left, right := make([]float32, 128), make([]float32, 128)
	ll, rr := make([]float32, 128), make([]float32, 128)
	frames := seq.Clock{SampleRate: 48000, BPMMilli: 137333}.SampleAtTick(5 * seq.TicksPerBar)
	for at := int64(0); at < frames; at += 128 {
		a.Render(left, right)
		b.Render(ll, rr)
		for i := range left {
			if math.Abs(float64(left[i]-ll[i])) > 1e-6 || math.Abs(float64(right[i]-rr[i])) > 1e-6 {
				t.Fatalf("conversion changed backing at %d: %.8f %.8f", at+int64(i), left[i], ll[i])
			}
		}
	}
	if len(q.Arrange.Markers) != 5 {
		t.Fatal("block boundaries lost")
	}
}

func TestVocalFullLengthBeyondOldAssetLimit(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*93 }", 1))
	os.WriteFile(s.path, source, 0600)
	take := fakeTimelineTake(t, s, 0, 93*seq.TicksPerBar, 93*96000, .2)
	if err := s.commitTake(take.ID, take.Expected, nil); err != nil {
		t.Fatal(err)
	}
	candidate, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if p.Assets[0].Frames <= 8<<20 {
		t.Fatal("fixture does not cover the old frame limit")
	}
	cfg, err := schedule.Compile(p, takeRoot(s.path), 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 92})
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	left, right := make([]float32, 128), make([]float32, 128)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	frames, err := e.PrepareRender(ctx, len(left))
	if err != nil {
		t.Fatal(err)
	}
	e.Render(left[:frames], right[:frames])
	for {
		var m cmd.Message
		if !e.Poll(&m) {
			break
		}
		if m.Kind == cmd.Fault {
			t.Fatalf("full-length vocal faulted: %+v", m)
		}
	}
	if cfg.Assets[0].Frames != 93*96000 || cfg.Assets[0].StreamID == 0 || len(cfg.Assets[0].Left) != 0 {
		t.Fatal("long vocal was truncated")
	}
	if stats := e.StreamStats(); stats.MissingFrames != 0 {
		t.Fatalf("late seek missed vocal pages: %+v", stats)
	}
	if left[frames-1] == 0 && right[frames-1] == 0 {
		t.Fatal("late vocal seek produced silence")
	}
}

func TestVocalNativePunchAutoSavesAndRetainsRawInput(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1))
	os.WriteFile(s.path, source, 0600)
	s.transport.sampleRate = 48000
	s.transport.audioNull = true
	s.transport.audioOptions.InputEnabled = true
	s.transport.audioOptions.MonitorMuted = true
	audio, _ := simulatedCaptureAudio(1024, zeroAudioSource{})
	audio.actual.CaptureChannels = 1
	s.transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return audio, nil }
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "record", Track: "vox", Revision: studioRevision(source), Recording: &studioRecordingSettings{Start: "@2.1.1", End: "@3.1.1"}})
	if response.Code != 200 {
		t.Fatalf("record: %d %s", response.Code, response.Body)
	}
	s.mu.Lock()
	id, r := s.captureID, s.captureRecorder
	s.mu.Unlock()
	input := [][]float32{make([]float32, 1024)}
	for i := range input[0] {
		input[0][i] = .05
	}
	output := [][]float32{make([]float32, 1024), make([]float32, 1024)}
	for at := uint64(0); !r.Snapshot().Completed && at < 400000; at += 1024 {
		audio.captureInput(capture.Block{DeviceEpoch: 1, DeviceFrame: at, SampleRate: 48000, Frames: 1024, Period: 1024, Layout: capture.LayoutMono}, input)
		if err := audio.renderPeriod(input, output); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for r.Snapshot().WrittenFrames+8192 < at+1024 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		active := s.captureID
		s.mu.Unlock()
		if active == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.captureID != "" {
		t.Fatal("punch did not finish automatically")
	}
	take, err := s.takes.Get(id)
	if err != nil || take.Stage != takejournal.Committed || take.Incomplete {
		t.Fatalf("take %+v %v; transport %+v; recorder %+v", take, err, s.transport.snapshot(), r.Snapshot())
	}
	if take.Frames != 288000 {
		t.Fatalf("count-in/pre-roll raw length %d", take.Frames)
	}
	raw, err := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	for at := 44; at < len(raw); at += 4 {
		if math.Float32frombits(binary.LittleEndian.Uint32(raw[at:])) != .05 {
			t.Fatal("backing or monitoring contaminated raw input")
		}
	}
	candidate, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if p.Assets[0].Frames != 96000 {
		t.Fatalf("published count-in or pre-roll: %d", p.Assets[0].Frames)
	}
	found := false
	for _, place := range p.Arrange.Placements {
		if place.Track == "vox" {
			found = true
			if place.AtTick != seq.TicksPerBar || place.LengthTicks != seq.TicksPerBar {
				t.Fatalf("punch placement %+v", place)
			}
		}
	}
	if !found {
		t.Fatal("no vocal placement")
	}
}
