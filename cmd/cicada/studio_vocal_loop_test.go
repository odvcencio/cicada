package main

import (
	"bytes"
	"encoding/json"
	"errors"
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

func newVocalLoopFixture(t *testing.T, passes int) *recordingTransportFixture {
	t.Helper()
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1), "song { main }", "song { main*2 }", 1))
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	s.transport.sampleRate = 48000
	s.transport.audioNull = true
	s.transport.audioOptions.InputEnabled = true
	audio, _ := simulatedCaptureAudio(1024, zeroAudioSource{})
	audio.actual.CaptureChannels = 1
	s.transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return audio, nil }
	edit := studioEdit{Action: "record", Track: "vox", Revision: studioRevision(source), Recording: &studioRecordingSettings{Start: "@1.1.1", End: "@2.1.1", Passes: passes}}
	response := studioCall(t, s.routes(), "/api/takes", edit)
	if response.Code != http.StatusOK {
		t.Fatalf("loop arm: %d %s", response.Code, response.Body)
	}
	f := &recordingTransportFixture{studio: s, audio: audio, recorder: s.captureRecorder, id: s.captureID, source: source, countIn: s.captureRecorder.Snapshot().CountInFrames, input: [][]float32{make([]float32, 1024)}, output: [][]float32{make([]float32, 1024), make([]float32, 1024)}}
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.captureRecorder != nil {
			_ = s.transport.disarmCapture()
			_ = s.captureRecorder.Close()
			s.captureRecorder = nil
			s.captureID = ""
			s.vocalLoop = nil
		}
	})
	return f
}

func vocalLoopPeriod(t *testing.T, f *recordingTransportFixture) {
	t.Helper()
	for i := range f.input[0] {
		at := int64(f.frames) + int64(i)
		f.input[0][i] = .25
		if at >= f.countIn {
			f.input[0][i] = float32((at-f.countIn)/96000+1) / 10
		}
	}
	f.audio.captureInput(capture.Block{DeviceEpoch: 1, DeviceFrame: f.frames, SampleRate: 48000, Frames: 1024, Period: 1024, Layout: capture.LayoutMono}, f.input)
	if err := f.audio.renderPeriod(f.input, f.output); err != nil {
		t.Fatal(err)
	}
	f.frames += 1024
	deadline := time.Now().Add(5 * time.Second)
	for f.recorder.Snapshot().WrittenFrames+8192 < f.frames && !f.recorder.Snapshot().Completed && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if time.Now().After(deadline) {
		t.Fatal("loop writer did not keep up")
	}
}
func vocalLoopAdvance(t *testing.T, f *recordingTransportFixture, frames uint64) {
	t.Helper()
	for f.frames < frames {
		vocalLoopPeriod(t, f)
	}
}

func assertLoopTake(t *testing.T, s *studio, take takejournal.Take, countIn, frames uint64, value float32) []byte {
	t.Helper()
	if take.Frames != frames || take.Incomplete || take.Stage != takejournal.Published && take.Stage != takejournal.Committed {
		t.Fatalf("wrong retained pass:%+v", take)
	}
	if take.Timeline.PlaybackStartTick != take.Timeline.StartTick || takeOrigin(take) != int64(countIn) {
		t.Fatalf("pass retained incorrect count-in/origin:%+v", take)
	}
	raw, err := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if err != nil {
		t.Fatal(err)
	}
	for at := uint64(0); at < frames; at++ {
		want := value
		if at < countIn {
			want = .25
		}
		if got := vocalGainSample(raw, int64(at)); got != want {
			t.Fatalf("pass raw frame%d=%g want%g", at, got, want)
		}
	}
	return raw
}

func TestVocalLoopFinitePassesKeepExactPCMAndOneCountIn(t *testing.T) {
	f := newVocalLoopFixture(t, 3)
	loop := f.studio.vocalLoop
	if f.countIn != 96000 {
		t.Fatal("wrong initial count-in")
	}
	vocalLoopAdvance(t, f, uint64(f.countIn+3*96000)-1024)
	f.studio.mu.Lock()
	vocalLoopPeriod(t, f)
	if !f.recorder.Snapshot().Completed {
		f.studio.mu.Unlock()
		t.Fatal("loop did not stop at finite capture boundary")
	}
	err := f.studio.finishVocalCapture(f.id)
	f.studio.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if f.studio.captureID != "" || f.studio.vocalLoop != nil || f.studio.transport.snapshot().Playing {
		t.Fatal("completed loop stayed active")
	}
	if len(loop.ids) != 3 || len(f.studio.takes.Takes()) != 3 {
		t.Fatal("passes were lost or duplicated")
	}
	for i, id := range loop.ids {
		take, _ := f.studio.takes.Get(id)
		countIn := uint64(0)
		if i == 0 {
			countIn = 96000
		}
		assertLoopTake(t, f.studio, take, countIn, countIn+96000, float32(i+1)/10)
		if lo, hi := takeCoverage(take); lo != 0 || hi != seq.TicksPerBar {
			t.Fatalf("pass coverage%d -> %d", lo, hi)
		}
	}
	source, _ := os.ReadFile(f.studio.path)
	comps, err := vocalComps(source)
	if err != nil || len(comps) != 1 || len(comps[0].Segments) != 1 || comps[0].Segments[0].Take != loop.ids[2] {
		t.Fatalf("last pass was not chosen:%+v %v", comps, err)
	}
	// Any earlier pass remains usable, and creating its candidate leaves the last
	// committed score and every original raw pass intact.
	first, _ := f.studio.takes.Get(loop.ids[0])
	if _, err = f.studio.vocalTakeSource(source, first, nil); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(f.studio.path)
	if !bytes.Equal(current, source) {
		t.Fatal("earlier pass candidate changed score")
	}
}

func TestVocalLoopStopPartialAndRevisionConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			f := newVocalLoopFixture(t, 3)
			loop := f.studio.vocalLoop
			vocalLoopAdvance(t, f, uint64(f.countIn+96000+48000))
			before, _ := os.ReadFile(f.studio.path)
			if !bytes.Equal(before, f.source) {
				t.Fatal("automatic pass split changed source during capture")
			}
			if conflict {
				before = append(before, []byte("\n// external edit\n")...)
				if err := os.WriteFile(f.studio.path, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			response := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
			if !conflict {
				// The take endpoint must focus the last retained pass, which was
				// chosen in the comp, rather than its first capture anchor.
				// Use a separate fixture so each Stop is an actual active drain.
				g := newVocalLoopFixture(t, 3)
				other := g.studio.vocalLoop
				vocalLoopAdvance(t, g, uint64(g.countIn+96000+48000))
				reply := studioCall(t, g.studio.routes(), "/api/takes", studioEdit{Action: "stop", Revision: studioRevision(g.source)})
				var body struct {
					Take string `json:"take"`
				}
				if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &body) != nil || body.Take != other.ids[1] {
					t.Fatalf("loop Stop did not return the chosen final pass: %d %s", reply.Code, reply.Body)
				}
			}
			want := http.StatusOK
			if conflict {
				want = http.StatusConflict
			}
			if response.Code != want {
				t.Fatalf("Stop=%d want%d %s", response.Code, want, response.Body)
			}
			if len(loop.ids) != 2 || len(f.studio.takes.Takes()) != 2 || f.studio.captureID != "" {
				t.Fatal("Stop lost/duplicated a pass or stayed active")
			}
			first, _ := f.studio.takes.Get(loop.ids[0])
			assertLoopTake(t, f.studio, first, 96000, 192000, .1)
			last, _ := f.studio.takes.Get(loop.ids[1])
			if conflict {
				last.Stage = takejournal.Published
			}
			assertLoopTake(t, f.studio, last, 0, f.frames-192000, .2)
			current, _ := os.ReadFile(f.studio.path)
			if conflict && !bytes.Equal(current, before) {
				t.Fatal("Stop overwrote conflicting source")
			}
			if !conflict {
				comps, _ := vocalComps(current)
				if comps[0].Segments[0].Take != loop.ids[1] || comps[0].Segments[0].End >= seq.TicksPerBar {
					t.Fatal("partial Stop did not choose partial last pass")
				}
			}
		})
	}
}

func TestVocalLoopBoundaryStopSerializesWithoutEmptyPass(t *testing.T) {
	f := newVocalLoopFixture(t, 2)
	loop := f.studio.vocalLoop
	vocalLoopAdvance(t, f, uint64(f.countIn+2*96000)-1024)
	f.studio.mu.Lock()
	vocalLoopPeriod(t, f)
	if !f.recorder.Snapshot().Completed {
		f.studio.mu.Unlock()
		t.Fatal("missing final boundary")
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
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
		r := studioCall(t, f.studio.routes(), "/api/transport", studioTransportInput{Action: "stop"})
		if r.Code != http.StatusOK {
			results <- fmt.Errorf("Stop %d %s", r.Code, r.Body)
		} else {
			results <- nil
		}
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
			t.Fatal("loop finish race hung")
		}
	}
	if len(loop.ids) != 2 || len(f.studio.takes.Takes()) != 2 {
		t.Fatal("completion race created an empty/duplicate pass")
	}
	first, _ := f.studio.takes.Get(loop.ids[0])
	second, _ := f.studio.takes.Get(loop.ids[1])
	assertLoopTake(t, f.studio, first, 96000, 192000, .1)
	assertLoopTake(t, f.studio, second, 0, 96000, .2)
}

func TestVocalLoopInterruptedInputRetainsAcceptedPCM(t *testing.T) {
	s, source, _ := vocalGainFixture(t)
	timeline := &takejournal.Timeline{EndTick: seq.TicksPerBar, TempoMilli: 120000}
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	take, _ := s.takes.Get(id)
	loop := newVocalLoopCapture(s.takes, take, 2)
	original := []float32{.1, .2, .3, .4}
	block := capture.RecordedBlock{Timing: capture.Block{Frames: 4, SampleRate: 48000, Flags: capture.DeviceDiscontinuity}, Placement: capture.Placement{EngineFrame: 0}}
	if err = loop.write(block, [][]float32{original}); err == nil {
		t.Fatal("interrupted repetition continued")
	}
	if err = s.finishVocalLoop(loop, capture.Snapshot{Incomplete: true}, nil); err == nil {
		t.Fatal("incomplete loop selected automatically")
	}
	retained, _ := s.takes.Get(id)
	if !retained.Incomplete || retained.Stage != takejournal.Published || retained.Frames != 4 {
		t.Fatalf("interrupted prefix not published:%+v", retained)
	}
	raw, _ := os.ReadFile(takeRoot(s.path) + "/" + retained.Asset.Path)
	for i, want := range original {
		if got := vocalGainSample(raw, int64(i)); math.Abs(float64(got-want)) > 1e-7 {
			t.Fatal("interruption lost accepted PCM")
		}
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, source) {
		t.Fatal("interruption changed score")
	}
	if !errors.Is(loop.fault, err) && loop.fault == nil {
		t.Fatal("writer fault not retained")
	}
}

func TestVocalLoopTimestampJitterDoesNotLoseBoundarySamples(t *testing.T) {
	s, source, _ := vocalGainFixture(t)
	timeline := &takejournal.Timeline{EndTick: 1, TempoMilli: 120000}
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	take, _ := s.takes.Get(id)
	loop := newVocalLoopCapture(s.takes, take, 2)
	raw := 0
	for blockIndex, n := range []int{13, 14, 23} {
		jitter := int64(0)
		if blockIndex == 1 {
			jitter = 1
		}
		if blockIndex == 2 {
			jitter = -1
		}
		pcm := make([]float32, n)
		for i := range pcm {
			pcm[i] = float32(raw+i) / 100
		}
		block := capture.RecordedBlock{RawFrame: uint64(raw), Timing: capture.Block{SampleRate: 48000, Frames: n}, Placement: capture.Placement{EngineFrame: int64(raw) + jitter}}
		if err = loop.write(block, [][]float32{pcm}); err != nil {
			t.Fatal(err)
		}
		raw += n
	}
	if err = s.finishVocalLoop(loop, capture.Snapshot{Completed: true}, nil); err != nil {
		t.Fatal(err)
	}
	if len(loop.ids) != 2 {
		t.Fatal("jitter lost pass")
	}
	for i, id := range loop.ids {
		take, _ := s.takes.Get(id)
		if take.Frames != 25 || takeOrigin(take) != 0 {
			t.Fatalf("jitter changed boundary:%+v", take)
		}
		pcm, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
		for at := 0; at < 25; at++ {
			if got := vocalGainSample(pcm, int64(at)); got != float32(i*25+at)/100 {
				t.Fatal("jitter dropped/duplicated source PCM")
			}
		}
	}
}
