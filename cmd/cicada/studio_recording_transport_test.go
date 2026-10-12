package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
)

type recordingTransportFixture struct {
	studio   *studio
	audio    *backendStudioAudio
	recorder *capture.Recorder
	id       string
	source   []byte
	frames   uint64
	countIn  int64
	input    [][]float32
	output   [][]float32
}

func newRecordingTransportFixture(t *testing.T, timeline, begin bool) *recordingTransportFixture {
	t.Helper()
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1))
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	s.transport.sampleRate = 48000
	s.transport.audioNull = true
	s.transport.audioOptions.InputEnabled = true
	s.transport.audioOptions.MonitorMuted = false
	s.transport.audioOptions.MonitorGain = 2
	audio, _ := simulatedCaptureAudio(1024, zeroAudioSource{})
	audio.actual.CaptureChannels = 1
	s.transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return audio, nil }
	edit := studioEdit{Action: "arm", Track: "vox", Scene: "main", Revision: studioRevision(source)}
	if begin {
		edit.Action = "record"
	}
	if timeline {
		edit.Recording = &studioRecordingSettings{Start: "@1.1.1", End: "@3.1.1"}
	}
	response := studioCall(t, s.routes(), "/api/takes", edit)
	if response.Code != http.StatusOK {
		t.Fatalf("prepare recording: %d %s", response.Code, response.Body)
	}
	s.mu.Lock()
	id, recorder := s.captureID, s.captureRecorder
	s.mu.Unlock()
	f := &recordingTransportFixture{studio: s, audio: audio, recorder: recorder, id: id, source: source, countIn: recorder.Snapshot().CountInFrames, input: [][]float32{make([]float32, 1024)}, output: [][]float32{make([]float32, 1024), make([]float32, 1024)}}
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.captureRecorder != nil {
			_ = s.transport.disarmCapture()
			_ = s.captureRecorder.Close()
			s.captureID, s.captureRecorder = "", nil
		}
	})
	return f
}

func (f *recordingTransportFixture) period(t *testing.T) {
	t.Helper()
	for i := range f.input[0] {
		f.input[0][i] = .25
		if int64(f.frames)+int64(i) >= f.countIn {
			f.input[0][i] = .75
		}
	}
	f.audio.captureInput(capture.Block{DeviceEpoch: 1, DeviceFrame: f.frames, SampleRate: 48000, Frames: 1024, Period: 1024, Layout: capture.LayoutMono}, f.input)
	if err := f.audio.renderPeriod(f.input, f.output); err != nil {
		t.Fatal(err)
	}
	f.frames += 1024
}

func (f *recordingTransportFixture) advance(t *testing.T, frames uint64) {
	t.Helper()
	for f.frames < frames {
		f.period(t)
		deadline := time.Now().Add(5 * time.Second)
		for f.recorder.Snapshot().WrittenFrames+8192 < f.frames && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatal("take writer did not keep up with synthetic input")
		}
	}
}

func (f *recordingTransportFixture) assertSaved(t *testing.T, frames uint64, timeline bool) []byte {
	t.Helper()
	s := f.studio
	s.mu.Lock()
	active, recorder := s.captureID, s.captureRecorder
	s.mu.Unlock()
	if active != "" || recorder != nil || s.transport.snapshot().Playing {
		t.Fatal("Stop left capture or backing active")
	}
	take, err := s.takes.Get(f.id)
	if err != nil || take.Stage != takejournal.Committed || take.Incomplete || take.Frames != frames {
		t.Fatalf("saved take: %+v, %v; expected %d frames", take, err, frames)
	}
	if len(s.takes.Takes()) != 1 {
		t.Fatal("Stop created a duplicate take")
	}
	raw, err := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 44+int(frames)*4 {
		t.Fatalf("retained input bytes = %d", len(raw))
	}
	for frame := uint64(0); frame < frames; frame++ {
		want := float32(.25)
		if int64(frame) >= f.countIn {
			want = .75
		}
		if got := math.Float32frombits(binary.LittleEndian.Uint32(raw[44+frame*4:])); got != want {
			t.Fatalf("raw input changed by backing/monitoring at frame %d: %g != %g", frame, got, want)
		}
	}
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = compileStudioSource(s.path, source); err != nil {
		t.Fatalf("Stop published invalid source: %v", err)
	}
	if !bytes.Contains(source, []byte("// retain this comment")) || !bytes.Contains(source, []byte("track bass acid {}")) {
		t.Fatal("Stop lost backing or source comments")
	}
	if timeline {
		comps, err := vocalComps(source)
		lo, hi := takeCoverage(take)
		if err != nil || len(comps) != 1 || len(comps[0].Segments) != 1 || comps[0].Segments[0] != (vocalSegment{Take: f.id, Start: lo, End: hi}) {
			t.Fatalf("Stop did not choose the recorded coverage: %+v %v", comps, err)
		}
	}
	return source
}

func TestVocalTransportStopSavesPartialTake(t *testing.T) {
	for _, timeline := range []bool{true, false} {
		name := "scene"
		if timeline {
			name = "timeline"
		}
		t.Run(name, func(t *testing.T) {
			f := newRecordingTransportFixture(t, timeline, true)
			f.advance(t, uint64(f.countIn+8192))
			response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
			if response.Code != http.StatusOK {
				t.Fatalf("Stop: %d %s", response.Code, response.Body)
			}
			f.assertSaved(t, f.frames, timeline)
		})
	}
}

func TestVocalTransportRejectsClockChanges(t *testing.T) {
	f := newRecordingTransportFixture(t, true, true)
	f.advance(t, uint64(f.countIn+8192))
	before := f.studio.transport.snapshot()
	for _, action := range []string{"play", "pause", "home", "fullSong", "loop", "loopSection", "audition", "trackStop", "launch", "slot", "playFrom"} {
		t.Run(action, func(t *testing.T) {
			response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: action, Revision: studioRevision(f.source), Scene: "main", Track: "bass", Pattern: "pulse", Start: "@1.1.1", End: "@2.1.1"})
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "finish the take") {
				t.Fatalf("clock mutation %s: %d %s", action, response.Code, response.Body)
			}
			after := f.studio.transport.snapshot()
			if !after.Playing || after.Listening == nil || *after.Listening != *before.Listening || after.Bar != before.Bar || after.Step != before.Step {
				t.Fatalf("rejected command changed backing: before %+v, after %+v", before, after)
			}
			source, err := os.ReadFile(f.studio.path)
			if err != nil || !bytes.Equal(source, f.source) {
				t.Fatal("rejected command changed source")
			}
			f.studio.mu.Lock()
			active, recorder := f.studio.captureID, f.studio.captureRecorder
			f.studio.mu.Unlock()
			if active != f.id || recorder != f.recorder || !recorder.Snapshot().Recording {
				t.Fatal("rejected command changed recorder")
			}
		})
	}
	f.advance(t, f.frames+1024)
	response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
	if response.Code != http.StatusOK {
		t.Fatalf("Stop after conflicts: %d %s", response.Code, response.Body)
	}
	f.assertSaved(t, f.frames, true)
}

func TestVocalTransportArmedPreparationKeepsDevice(t *testing.T) {
	f := newRecordingTransportFixture(t, true, false)
	for _, action := range []string{"play", "pause", "stop"} {
		response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: action})
		if response.Code != http.StatusOK {
			t.Fatalf("armed %s: %d %s", action, response.Code, response.Body)
		}
		if !f.audio.Armed() || f.recorder.Snapshot().Recording || f.studio.captureID != f.id {
			t.Fatal("preparation lost armed, not-yet-started take")
		}
	}
	current, err := os.ReadFile(f.studio.path)
	if err != nil || !bytes.Equal(current, f.source) {
		t.Fatal("armed preparation changed source")
	}
}

func TestVocalTransportStopAndPunchFinishSerialize(t *testing.T) {
	f := newRecordingTransportFixture(t, true, true)
	end := uint64(f.countIn + 2*96000)
	f.advance(t, end-1024)
	// Let the exact punch boundary and user Stop become ready together while
	// holding the same lock used by the automatic take finisher.
	f.studio.mu.Lock()
	f.period(t)
	if !f.recorder.Snapshot().Completed {
		f.studio.mu.Unlock()
		t.Fatal("synthetic input missed the punch boundary")
	}
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		ready.Done()
		<-start
		f.studio.mu.Lock()
		defer f.studio.mu.Unlock()
		results <- f.studio.finishVocalCapture(f.id)
	}()
	go func() {
		ready.Done()
		<-start
		response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
		if response.Code != http.StatusOK {
			results <- fmt.Errorf("Stop at punch boundary: %d %s", response.Code, response.Body)
			return
		}
		results <- nil
	}()
	ready.Wait()
	close(start)
	f.studio.mu.Unlock()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Stop raced with punch completion and did not finish")
		}
	}
	first := f.assertSaved(t, end, true)
	response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
	if response.Code != http.StatusOK {
		t.Fatalf("idempotent Stop: %d %s", response.Code, response.Body)
	}
	second, _ := os.ReadFile(f.studio.path)
	if !bytes.Equal(first, second) {
		t.Fatal("Stop after automatic finish changed the selected vocal")
	}
	if lo, hi := takeCoverage(mustTransportTake(t, f)); lo != 0 || hi != 2*seq.TicksPerBar {
		t.Fatalf("completed punch coverage = %d → %d", lo, hi)
	}
}

func TestVocalTransportStopRetainsRevisionConflict(t *testing.T) {
	f := newRecordingTransportFixture(t, true, true)
	f.advance(t, uint64(f.countIn+8192))
	external := append(bytes.Clone(f.source), []byte("// external score edit\n")...)
	if err := os.WriteFile(f.studio.path, external, 0600); err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"retained":true`) || !strings.Contains(response.Body.String(), f.id) {
		t.Fatalf("conflicted Stop: %d %s", response.Code, response.Body)
	}
	source, err := os.ReadFile(f.studio.path)
	if err != nil || !bytes.Equal(source, external) {
		t.Fatal("Stop overwrote an external score edit")
	}
	take := mustTransportTake(t, f)
	if take.Stage != takejournal.Conflict || take.Frames != f.frames || take.Incomplete {
		t.Fatalf("conflicted Stop failed to retain take: %+v", take)
	}
	raw, err := os.ReadFile(takeRoot(f.studio.path) + "/" + take.Asset.Path)
	if err != nil || len(raw) != 44+int(f.frames)*4 || math.Float32frombits(binary.LittleEndian.Uint32(raw[44+f.countIn*4:])) != .75 {
		t.Fatal("conflicted Stop lost retained input")
	}
	f.studio.mu.Lock()
	defer f.studio.mu.Unlock()
	if f.studio.captureID != "" || f.studio.captureRecorder != nil || f.studio.transport.snapshot().Playing {
		t.Fatal("conflicted Stop left capture or backing active")
	}
}

func mustTransportTake(t *testing.T, f *recordingTransportFixture) takejournal.Take {
	t.Helper()
	take, err := f.studio.takes.Get(f.id)
	if err != nil {
		t.Fatal(err)
	}
	return take
}
