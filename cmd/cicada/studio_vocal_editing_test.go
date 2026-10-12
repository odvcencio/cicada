package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
)

func vocalRampTake(t *testing.T, s *studio) takejournal.Take {
	t.Helper()
	source, _ := os.ReadFile(s.path)
	timeline := &takejournal.Timeline{EndTick: 2 * seq.TicksPerBar, TempoMilli: 120000}
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	for at := 0; at < 192000; {
		n := min(4096, 192000-at)
		pcm := make([]float32, n)
		for i := range pcm {
			pcm[i] = float32(at+i) / 1000000
		}
		b := capture.RecordedBlock{RawFrame: uint64(at), Timing: capture.Block{Frames: n, SampleRate: 48000, DeviceEpoch: 1, EngineFrame: int64(at)}, Placement: capture.Placement{EngineFrame: int64(at)}}
		if err = s.takes.Write(id, b, [][]float32{pcm}); err != nil {
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

func TestVocalEditingOffsetFadesAndImmutablePrints(t *testing.T) {
	s, source, _ := vocalGainFixture(t)
	take := vocalRampTake(t, s)
	rawBefore, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	zero := 0.
	fade := 10.
	cross := 20.
	for _, advance := range []float64{-10, 0, 10} {
		candidate, err := s.vocalTakeSource(source, take, &studioRecordingSettings{Start: "@1.3.1", End: "@1.4.1", OffsetMS: advance, FadeMS: &zero})
		if err != nil {
			t.Fatal(err)
		}
		_, pcm := vocalGainPrint(t, s, candidate)
		shift := vocalMillisecondsFrames(advance, 48000)
		for _, at := range []int64{0, 12000, 23999} {
			if got, want := vocalGainSample(pcm, at), float32(48000+at+shift)/1000000; got != want {
				t.Fatalf("advance %g at%d=%g want%g", advance, at, got, want)
			}
		}
		comps, _ := vocalComps(candidate)
		if comps[0].Segments[0].OffsetMS != advance || comps[0].Segments[0].FadeMS == nil || *comps[0].Segments[0].FadeMS != 0 {
			t.Fatal("timing/fade choices lost")
		}
	}
	a := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .125)
	b := fakeTimelineTake(t, s, 0, 2*seq.TicksPerBar, 192000, .375)
	comp := vocalComp{Track: "vox", Tempo: 120000, Segments: []vocalSegment{{Take: a.ID, Start: 0, End: seq.TicksPerBar, FadeMS: &fade}, {Take: b.ID, Start: seq.TicksPerBar, End: 2 * seq.TicksPerBar, FadeMS: &fade, CrossfadeMS: &cross}}}
	asset, err := s.printVocalComp(comp, 48000)
	if err != nil {
		t.Fatal(err)
	}
	pcm, _ := os.ReadFile(takeRoot(s.path) + "/" + asset.Path)
	for at, want := range map[int64]float32{0: 0, 100: .125 * float32(100./480), 95520: .125, 96000: .125*(1-float32(480./959)) + .375*float32(480./959), 96479: .375, 191999: 0} {
		if got := vocalGainSample(pcm, at); math.Abs(float64(got-want)) > 1e-7 {
			t.Fatalf("fade at%d=%g want%g", at, got, want)
		}
	}
	for at := int64(95520); at < 96480; at++ {
		if got := vocalGainSample(pcm, at); got < .125 || got > .375 {
			t.Fatalf("crossfade had hole/overshoot:%g", got)
		}
	}
	// A punch ending at the join supplies no outgoing handle after it; a bounded
	// one-sided crossfade still reaches the incoming pass without a zero dip.
	aPunch := fakeTimelineTake(t, s, 0, seq.TicksPerBar, 96000, .125)
	comp.Segments[0].Take = aPunch.ID
	bounded, err := s.printVocalComp(comp, 48000)
	if err != nil {
		t.Fatal(err)
	}
	boundedPCM, _ := os.ReadFile(takeRoot(s.path) + "/" + bounded.Path)
	if vocalGainSample(boundedPCM, 95999) != .375 || vocalGainSample(boundedPCM, 96000) != .375 {
		t.Fatal("bounded join lost continuity")
	}
	comp.Segments[1].CrossfadeMS = &zero
	if _, err = s.printVocalComp(comp, 48000); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(takeRoot(s.path) + "/" + asset.Path)
	after, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(old, pcm) || !bytes.Equal(rawBefore, after) || !bytes.Equal(source, current) {
		t.Fatal("edit changed original PCM, print or score")
	}
}

func TestVocalEditingPreservesMappingsAndRejectsMissingHandles(t *testing.T) {
	fade, cross := 12., 24.
	old := []vocalSegment{{Take: "a", Start: 0, End: 100, GainDB: -3, OffsetMS: 10, FadeMS: &fade, CrossfadeMS: &cross}}
	next := vocalSegment{Take: "a", Start: 25, End: 75, GainDB: -3, OffsetMS: -10, FadeMS: &fade, CrossfadeMS: &cross}
	got := replaceVocalSegment(old, next)
	if len(got) != 3 || got[0].OffsetMS != 10 || got[2].OffsetMS != 10 || got[1].OffsetMS != -10 || *got[0].FadeMS != fade || *got[2].CrossfadeMS != cross {
		t.Fatalf("neighbor mapping lost:%+v", got)
	}
	next.OffsetMS = 10
	if got = replaceVocalSegment(got, next); !reflect.DeepEqual(got, old) {
		t.Fatal("same mapping did not merge")
	}
	next.OffsetMS = 10
	zero := 0.
	next.FadeMS = &zero
	if got = replaceVocalSegment(old, next); len(got) != 3 {
		t.Fatal("different fades merged")
	}
	s, source, take := vocalGainFixture(t)
	for _, offset := range []float64{-10, 10} {
		if _, err := s.vocalTakeSource(source, take, &studioRecordingSettings{OffsetMS: offset}); err == nil {
			t.Fatal("full-range offset silently cropped PCM")
		}
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), -501, 501} {
		if _, _, err := vocalPhraseSettings(&studioRecordingSettings{OffsetMS: bad}); err == nil {
			t.Fatal("accepted invalid offset")
		}
		if err := validateStudioAudioOptions(studioAudioOptions{RecordingOffsetMS: bad, MonitorMode: "stereo"}); err == nil {
			t.Fatal("accepted invalid correction")
		}
	}
	for _, bad := range []float64{math.NaN(), math.Inf(-1), -1, 101} {
		for _, settings := range []*studioRecordingSettings{{FadeMS: &bad}, {CrossfadeMS: &bad}} {
			if _, _, err := vocalPhraseSettings(settings); err == nil {
				t.Fatal("accepted invalid transition")
			}
		}
	}
	metadata := vocalCompPrefix + `{"track":"vox","tempoMilli":120000,"segments":[{"take":"a","startTick":0,"endTick":100,"offsetMS":501}]}`
	if _, err := vocalComps([]byte(metadata)); err == nil {
		t.Fatal("accepted invalid saved timing")
	}
	legacy, _ := vocalComps([]byte(vocalCompPrefix + `{"track":"vox","tempoMilli":120000,"segments":[{"take":"a","startTick":0,"endTick":100}]}`))
	if vocalEditValue(legacy[0].Segments[0].FadeMS, 5) != 5 {
		t.Fatal("legacy fades changed")
	}
	for _, passes := range []int{-1, 9} {
		if _, _, err := vocalPhraseSettings(&studioRecordingSettings{Passes: passes}); err == nil {
			t.Fatal("invalid pass count accepted")
		}
	}
}

func TestVocalEditingResidualCalibrationPreservesRawAndExactBoundary(t *testing.T) {
	f := newRecordingTransportFixture(t, true, false)
	f.studio.transport.audioOptions.RecordingOffsetMS = 10
	f.audio.captureDeviceEpoch.Store(7)
	if err := f.studio.startAudioTake(); err != nil {
		t.Fatal(err)
	}
	f.countIn = f.recorder.Snapshot().CountInFrames
	for !f.recorder.Snapshot().Completed {
		for i := range f.input[0] {
			f.input[0][i] = 0
			if int64(f.frames)+int64(i) == f.countIn+480+24000 {
				f.input[0][i] = .75
			}
		}
		f.audio.captureInput(capture.Block{DeviceEpoch: 7, DeviceFrame: f.frames, SampleRate: 48000, Frames: 1024, Period: 1024, Layout: capture.LayoutMono, InputLatencyValid: true, OutputLatencyValid: true}, f.input)
		if err := f.audio.renderPeriod(f.input, f.output); err != nil {
			t.Fatal(err)
		}
		f.frames += 1024
		deadline := time.Now().Add(time.Second)
		for f.recorder.Snapshot().WrittenFrames+8192 < f.frames && !f.recorder.Snapshot().Completed && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	f.studio.mu.Lock()
	err := f.studio.finishVocalCapture(f.id)
	f.studio.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	take, _ := f.studio.takes.Get(f.id)
	if take.Frames != uint64(f.countIn+192000+480) || takeOrigin(take) != f.countIn+480 || !take.FirstBlock.Placement.Calibrated || take.FirstBlock.Placement.CorrectionFrames != -480 {
		t.Fatalf("wrong calibrated origin/boundary:%+v", take)
	}
	raw, _ := os.ReadFile(takeRoot(f.studio.path) + "/" + take.Asset.Path)
	if vocalGainSample(raw, f.countIn+480+24000) != .75 {
		t.Fatal("calibration changed raw source")
	}
	source, _ := os.ReadFile(f.studio.path)
	_, pcm := vocalGainPrint(t, f.studio, source)
	if vocalGainSample(pcm, 24000) != .75 || vocalGainSample(pcm, 24480) != 0 {
		t.Fatal("residual correction did not align captured event")
	}
	// A rejected correction preserves the configuration and the active recorder.
	stateJSON, _ := json.Marshal(f.studio.transport.audioOptions)
	var saved studioAudioOptions
	if json.Unmarshal(stateJSON, &saved) != nil || saved.RecordingOffsetMS != 10 {
		t.Fatal("correction omitted from options JSON")
	}
}

func TestVocalEditingCalibrationKeepsInputAliveAfterBackingEnds(t *testing.T) {
	f := newRecordingTransportFixture(t, true, false)
	f.studio.transport.audioOptions.RecordingOffsetMS = 500
	f.audio.captureDeviceEpoch.Store(7)
	if err := f.studio.startAudioTake(); err != nil {
		t.Fatal(err)
	}
	f.countIn = f.recorder.Snapshot().CountInFrames
	period := func() {
		for i := range f.input[0] {
			f.input[0][i] = .25
		}
		f.audio.captureInput(capture.Block{DeviceEpoch: 7, DeviceFrame: f.frames, SampleRate: 48000, Frames: 1024, Period: 1024, Layout: capture.LayoutMono, InputLatencyValid: true, OutputLatencyValid: true}, f.input)
		if err := f.audio.renderPeriod(f.input, f.output); err != nil {
			t.Fatal(err)
		}
		f.frames += 1024
		deadline := time.Now().Add(time.Second)
		for f.recorder.Snapshot().WrittenFrames+8192 < f.frames && !f.recorder.Snapshot().Completed && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if time.Now().After(deadline) {
			t.Fatalf("synthetic input writer stalled: %+v", f.recorder.Snapshot())
		}
	}
	for f.frames < uint64(f.countIn+192000) {
		period()
	}
	if !f.studio.transport.snapshot().Listening.Completed || !f.recorder.Snapshot().Recording {
		t.Fatal("fixture missed backing/input timing separation")
	}
	time.Sleep(75 * time.Millisecond)
	if !f.audio.playing.Load() || !f.recorder.Snapshot().Recording {
		t.Fatal("backing completion stopped input before delayed punch boundary")
	}
	for !f.recorder.Snapshot().Completed {
		period()
	}
	f.studio.mu.Lock()
	err := f.studio.finishVocalCapture(f.id)
	f.studio.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	take, _ := f.studio.takes.Get(f.id)
	if take.Frames != uint64(f.countIn+192000+24000) || take.Incomplete {
		t.Fatal("recording timing tail was not retained exactly")
	}
}

func TestVocalEditingKeepsInsertedEffectsThroughCompAndAudition(t *testing.T) {
	s, source, take := vocalGainFixture(t)
	rawBefore, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if err := s.commitTake(take.ID, studioRevision(source), nil); err != nil {
		t.Fatal(err)
	}
	withPrint, _ := os.ReadFile(s.path)
	oldAsset, oldPrint := vocalGainPrint(t, s, withPrint)
	reply := studioCall(t, s.routes(), "/api/mixer", studioEdit{Action: "vocal-chain", Track: "vox", Revision: studioRevision(withPrint)})
	if reply.Code != 200 {
		t.Fatalf("chain creation:%d %s", reply.Code, reply.Body)
	}
	chained, _ := os.ReadFile(s.path)
	// The generic declaration insertion currently lands before the end marker.
	if !bytes.Contains(chained, []byte("vox_vocal_hp")) {
		t.Fatal("fixture lacks vocal chain")
	}
	gain := -3.
	fade, cross := 8., 12.
	settings := &studioRecordingSettings{Start: "@1.3.1", End: "@1.4.1", GainDB: &gain, OffsetMS: 5, FadeMS: &fade, CrossfadeMS: &cross, AuditionMode: "solo"}
	candidate, err := s.vocalTakeSource(chained, take, settings)
	if err != nil {
		t.Fatal(err)
	}
	p, err := compileStudioSource(s.path, candidate)
	if err != nil || len(p.Effects) != 3 {
		t.Fatalf("comp deleted inserted effects:%v", err)
	}
	s.transport.audioNull = true
	if err = s.listenVocalTake(candidate, chained, take, settings, false); err != nil {
		t.Fatal(err)
	}
	s.transport.stop()
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, chained) {
		t.Fatal("audition changed chained source")
	}
	if err = s.commitTake(take.ID, studioRevision(chained), candidate); err != nil {
		t.Fatal(err)
	}
	_, err = compileStudioSource(s.path, candidate)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := os.ReadFile(takeRoot(s.path) + "/" + oldAsset.Path)
	rawAfter, _ := os.ReadFile(takeRoot(s.path) + "/" + take.Asset.Path)
	if !bytes.Equal(rawBefore, rawAfter) || !bytes.Equal(previous, oldPrint) {
		t.Fatal("comp with effects changed raw or prior print")
	}
}
