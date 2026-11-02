package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

func tempoVocalStudio(t *testing.T) (*studio, []byte, string) {
	t.Helper()
	s, entry, _, owner := sceneControlsStudio(t, t.TempDir())
	scenes := []byte("scene first { lead=one vox=off tempo=137.333 lead.level=-18dB }\nscene held { lead=keep tempo=96 lead.level=-18dB }\nscene next { lead=two tempo=120 lead.level=-6dB }\nscene quiet { lead=off }\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(s.path), "scenes.cicada"), scenes, 0600); err != nil {
		t.Fatal(err)
	}
	return s, entry, owner
}

func mappedVocalTake(t *testing.T, s *studio, start, end int64, silent bool, signal ...func(int) float32) (takejournal.Take, []float32) {
	t.Helper()
	source, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		t.Fatal(err)
	}
	timeline, err := recordingTimeline(p, &studioRecordingSettings{Start: listeningPosition(start), End: listeningPosition(end)})
	if err != nil {
		t.Fatal(err)
	}
	clock, err := timeline.Clock(48000)
	if err != nil {
		t.Fatal(err)
	}
	frames := clock.SampleAtTick(end) - clock.SampleAtTick(timeline.PlaybackStartTick)
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, frames)
	if !silent {
		for i := range pcm {
			pcm[i] = float32(.2*math.Sin(float64(i)*.037) + .03*math.Sin(float64(i)*.0013))
			if len(signal) > 0 {
				pcm[i] = signal[0](i)
			}
		}
	}
	for at := 0; at < len(pcm); {
		n := min(4096, len(pcm)-at)
		record := capture.RecordedBlock{RawFrame: uint64(at), Timing: capture.Block{SampleRate: 48000, Frames: n}, Placement: capture.Placement{EngineFrame: int64(at)}}
		if err = s.takes.Write(id, record, [][]float32{pcm[at : at+n]}); err != nil {
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
	take, err := s.takes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return take, pcm
}

func TestVocalTempoConversionPreservesBackingAndAllSources(t *testing.T) {
	isolateState(t)
	s, entry, owner := tempoVocalStudio(t)
	root := filepath.Dir(s.path)
	before, err := compileStudioSource(s.path, entry)
	if err != nil {
		t.Fatal(err)
	}
	take, _ := mappedVocalTake(t, s, 0, 6*3840, true)
	result, _, err := s.vocalTakeResult(entry, take, nil)
	if err != nil {
		t.Fatal(err)
	}
	overrides := map[string][]byte{}
	for _, f := range result.Files {
		overrides[f.Path] = f.After
	}
	after, err := compileStudioSourceWithOverrides(s.path, result.Source, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project.SongTempoChanges(before), project.SongTempoChanges(after)) {
		t.Fatal("tempo map changed")
	}
	render := func(p *project.Project) []byte {
		cfg, err := schedule.CompileResident(p, root, 48000, 128)
		if err != nil {
			t.Fatal(err)
		}
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer e.Close()
		frames := e.FramesBetweenTicks(0, 6*3840)
		out := make([]byte, frames*8)
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		var l, r [128]float32
		for at := int64(0); at < frames; {
			n := min(int64(128), frames-at)
			e.Render(l[:n], r[:n])
			for i := int64(0); i < n; i++ {
				binary.LittleEndian.PutUint32(out[(at+i)*8:], math.Float32bits(l[i]))
				binary.LittleEndian.PutUint32(out[(at+i)*8+4:], math.Float32bits(r[i]))
			}
			at += n
		}
		return out
	}
	a, b := render(before), render(after)
	if !bytes.Equal(a, b) {
		for i := 0; i < min(len(a), len(b)); i += 4 {
			if !bytes.Equal(a[i:i+4], b[i:i+4]) {
				t.Fatalf("tempo conversion changed PCM at frame %d: %g -> %g", i/8, math.Float32frombits(binary.LittleEndian.Uint32(a[i:])), math.Float32frombits(binary.LittleEndian.Uint32(b[i:])))
			}
		}
		t.Fatal("frame count", len(a), len(b))
	}
	t.Logf("exact six-bar tempo conversion PCM %x (%d frames)", sha256.Sum256(a), len(a)/8)
	paths := []string{s.path, owner, filepath.Join(root, "curves.cicada"), filepath.Join(root, "scenes.cicada")}
	original := map[string][]byte{}
	for _, path := range paths {
		original[path], _ = os.ReadFile(path)
	}
	if err = s.commitTake(take.ID, studioRevision(entry), nil); err != nil {
		t.Fatal(err)
	}
	committed := map[string][]byte{}
	for _, path := range paths {
		committed[path], _ = os.ReadFile(path)
	}
	if !bytes.Equal(original[paths[3]], committed[paths[3]]) || !bytes.Contains(committed[owner], []byte("tempo @3.1.1 96")) {
		t.Fatal("source ownership or tempo step lost")
	}
	for _, step := range []struct {
		route string
		want  map[string][]byte
	}{{"/api/undo", original}, {"/api/redo", committed}} {
		current, _ := os.ReadFile(s.path)
		r := studioCall(t, s.routes(), step.route, studioEdit{Revision: studioRevision(current)})
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		for path, want := range step.want {
			actual, _ := os.ReadFile(path)
			if !bytes.Equal(actual, want) {
				t.Fatal(step.route, path)
			}
		}
	}
	if err = s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.shutdown()
	stored, err := reopened.takes.Get(take.ID)
	if err != nil || !reflect.DeepEqual(stored.Timeline, take.Timeline) {
		t.Fatal("recording tempo snapshot lost", err)
	}
}

func TestVocalTempoPhraseReadsOriginalPCMAndRejectsChangedMap(t *testing.T) {
	isolateState(t)
	s, entry, _ := tempoVocalStudio(t)
	take, pcm := mappedVocalTake(t, s, 2*3840, 5*3840, false)
	clock, err := takeClock(take)
	if err != nil {
		t.Fatal(err)
	}
	if lo, hi := takeCoverage(take); lo != 2*3840 || hi != 5*3840 {
		t.Fatal("tempo coverage", lo, hi)
	}
	zero := float64(0)
	comp := vocalComp{Track: "vox", Tempo: take.Timeline.TempoMilli, TempoChanges: take.Timeline.TempoChanges, Segments: []vocalSegment{{Take: take.ID, Start: 2 * 3840, End: 4 * 3840, FadeMS: &zero}}}
	asset, err := s.printVocalComp(comp, 48000)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	a := clock.SampleAtTick(2*3840) - clock.SampleAtTick(take.Timeline.PlaybackStartTick)
	b := clock.SampleAtTick(4*3840) - clock.SampleAtTick(take.Timeline.PlaybackStartTick)
	if asset.Frames != b-a {
		t.Fatal("phrase duration", asset.Frames, b-a)
	}
	for i, v := range pcm[a:b] {
		if got := math.Float32frombits(binary.LittleEndian.Uint32(data[44+i*4:])); got != v {
			t.Fatal("raw frame changed", i, got, v)
		}
	}
	root := filepath.Dir(s.path)
	path := filepath.Join(root, "scenes.cicada")
	scenes, _ := os.ReadFile(path)
	external := bytes.Replace(scenes, []byte("tempo=96"), []byte("tempo=84"), 1)
	if err = os.WriteFile(path, external, 0600); err != nil {
		t.Fatal(err)
	}
	if result, _, err := s.vocalTakeResult(entry, take, nil); err == nil || result != nil {
		t.Fatal("selected against changed tempo map")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, external) {
		t.Fatal("changed tempo map overwritten")
	}
}

func TestVocalTempoSyntheticCaptureAndFinitePasses(t *testing.T) {
	for _, test := range []struct {
		name, start, end string
		passes           int
	}{{"full twice", "@1.1.1", "@7.1.1", 2}, {"punch across steps", "@2.1.1", "@5.1.1", 1}, {"fractional offset twice", "@2.1.1", "@6.1.1", 2}} {
		t.Run(test.name, func(t *testing.T) {
			isolateState(t)
			s, entry, _ := tempoVocalStudio(t)
			s.transport.sampleRate = 48000
			s.transport.audioNull = true
			s.transport.audioOptions.InputEnabled = true
			audio, _ := simulatedCaptureAudio(1024, zeroAudioSource{})
			audio.actual.CaptureChannels = 1
			s.transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return audio, nil }
			r := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "record", Track: "vox", Revision: studioRevision(entry), Recording: &studioRecordingSettings{Start: test.start, End: test.end, Passes: test.passes}})
			if r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
			f := &recordingTransportFixture{studio: s, audio: audio, recorder: s.captureRecorder, id: s.captureID, source: entry, countIn: s.captureRecorder.Snapshot().CountInFrames, input: [][]float32{make([]float32, 1024)}, output: [][]float32{make([]float32, 1024), make([]float32, 1024)}}
			take, err := s.takes.Get(f.id)
			if err != nil {
				t.Fatal(err)
			}
			clock, err := takeClock(take)
			if err != nil {
				t.Fatal(err)
			}
			countClock, err := seq.NewClock(48000, clock.ClockAtTick(take.Timeline.PlaybackStartTick).BPMMilli)
			if err != nil {
				t.Fatal(err)
			}
			if f.countIn != countClock.SampleAtTick(3840) {
				t.Fatal("count-in used header tempo", f.countIn)
			}
			duration := clock.SampleAtTick(take.Timeline.EndTick) - clock.SampleAtTick(take.Timeline.PlaybackStartTick)
			total := uint64(f.countIn + duration*int64(test.passes))
			f.advance(t, total-1024)
			s.mu.Lock()
			f.period(t)
			if !f.recorder.Snapshot().Completed {
				s.mu.Unlock()
				t.Fatal("finite tempo capture missed end")
			}
			err = s.finishVocalCapture(f.id)
			s.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			for _, stored := range s.takes.Takes() {
				lo, hi := takeCoverage(stored)
				if stored.Incomplete || lo != stored.Timeline.StartTick || hi != stored.Timeline.EndTick {
					t.Fatal("recorded musical coverage", lo, hi, stored.Timeline, stored.Frames)
				}
			}
		})
	}
}

func TestVocalTempoRangeUsesDeclaredCountIn(t *testing.T) {
	isolateState(t)
	s, entry, _ := tempoVocalStudio(t)
	entry = append(entry, []byte("\nlive { count_in = 2bars }\n")...)
	if err := os.WriteFile(s.path, entry, 0600); err != nil {
		t.Fatal(err)
	}
	s.transport.sampleRate = 48000
	s.transport.audioNull = true
	s.transport.audioOptions.InputEnabled = true
	audio, _ := simulatedCaptureAudio(1024, zeroAudioSource{})
	audio.actual.CaptureChannels = 1
	s.transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return audio, nil }
	r := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "record", Track: "vox", Revision: studioRevision(entry), Recording: &studioRecordingSettings{Start: "@2.1.1", End: "@5.1.1", Passes: 1}})
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	take, err := s.takes.Get(s.captureID)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := takeClock(take)
	if err != nil {
		t.Fatal(err)
	}
	countClock, err := seq.NewClock(48000, clock.ClockAtTick(take.Timeline.PlaybackStartTick).BPMMilli)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.captureRecorder.Snapshot().CountInFrames, countClock.SampleAtTick(2*3840); got != want {
		t.Fatalf("range count-in frames = %d, want %d", got, want)
	}
}

func TestVocalTempoWaveformTransientStaysUnderItsMusicalBar(t *testing.T) {
	isolateState(t)
	s, entry, _ := tempoVocalStudio(t)
	p, err := compileStudioSource(s.path, entry)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := project.NewTempoMap(48000, int64(p.TempoMilli), project.SongTempoChanges(p))
	if err != nil {
		t.Fatal(err)
	}
	transient := int(clock.SampleAtTick(2 * 3840))
	take, _ := mappedVocalTake(t, s, 0, 6*3840, false, func(at int) float32 {
		if at == transient {
			return .9
		}
		return 0
	})
	wave, err := analyzeVocalWaveform(context.Background(), filepath.Dir(s.path), take, 96)
	if err != nil {
		t.Fatal(err)
	}
	for i, peak := range wave.Peaks {
		want := float32(0)
		if i == 32 {
			want = .9
		}
		if peak[1] != want {
			t.Fatal("transient moved away from bar 3", i, peak[1], want)
		}
	}
}
