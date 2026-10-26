package main

import (
	"bytes"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/render"
)

func TestAmpMonitorKeepsCapturedInputDry(t *testing.T) {
	for _, tc := range []struct {
		mode        string
		left, right float32
	}{{"mono1", .125, .125}, {"mono2", -.25, -.25}, {"stereo", .125, -.25}} {
		t.Run(tc.mode, func(t *testing.T) {
			a, _ := simulatedCaptureAudio(256, zeroAudioSource{})
			a.actual.SampleRate = 48000
			var changed bool
			var frames int
			rec, err := capture.NewRecorder(32, 256, 2, func(_ capture.RecordedBlock, in [][]float32) error {
				frames += len(in[0])
				for i := range in[0] {
					changed = changed || in[0][i] != tc.left || in[1][i] != tc.right
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer rec.Close()
			a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: tc.mode, AmpEnabled: true, AmpDrive: .7, AmpTone: .5, AmpCabinet: 1})
			if err := a.ArmCapture(rec); err != nil {
				t.Fatal(err)
			}
			if err := rec.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
				t.Fatal(err)
			}
			a.playing.Store(true)
			input := [][]float32{make([]float32, 256), make([]float32, 256)}
			output := [][]float32{make([]float32, 256), make([]float32, 256)}
			for i := range input[0] {
				input[0][i], input[1][i] = .125, -.25
			}
			for i := 0; i < 4; i++ {
				simulatedCapturePeriod(t, a, uint64(i*256), input, output)
			}
			if err := a.DisarmCapture(); err != nil {
				t.Fatal(err)
			}
			if changed || frames != 1024 || input[0][0] != .125 || input[1][0] != -.25 {
				t.Fatalf("capture changed: %t frames %d", changed, frames)
			}
			if math.Abs(float64(output[0][255]-tc.left)) < .001 || (tc.mode != "stereo" && output[0][255] != output[1][255]) {
				t.Fatalf("monitor not processed: %g %g", output[0][255], output[1][255])
			}
			if a.Snapshot().MonitorFXLatencyFrames != 31 {
				t.Fatal("amp latency missing")
			}
		})
	}
}

func TestAmpSettingsPersistMutedAndTrackReamp(t *testing.T) {
	t.Setenv("CICADA_AUDIO_SETTINGS_DIR", t.TempDir())
	path, _ := studioAudioSettingsPath(audiobackend.Tymbal)
	options := defaultStudioAudioOptionsFor(audiobackend.Tymbal)
	options.AmpEnabled, options.AmpDrive, options.AmpTone, options.AmpOutput, options.AmpCabinet = true, .7, .4, -3, 2
	options.MonitorMuted = false
	if err := saveStudioAudioSettings(path, options); err != nil {
		t.Fatal(err)
	}
	got, err := loadStudioAudioSettings(path, defaultStudioAudioOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !got.MonitorMuted || !got.AmpEnabled || got.AmpDrive != options.AmpDrive || got.AmpTone != options.AmpTone || got.AmpOutput != options.AmpOutput || got.AmpCabinet != 2 {
		t.Fatalf("settings lost: %+v", got)
	}
	scorePath := mixerProject(t, "project test\ncicada 2\n", audioTakeScore)
	drive, tone, output, cab := .7, .4, -3., 2
	apply := studioEdit{Action: "amp-chain", Track: "vox", AmpDrive: &drive, AmpTone: &tone, AmpOutput: &output, AmpCabinet: &cab}
	reply := mixerRoute(t, scorePath, apply)
	if reply.Code != http.StatusOK {
		t.Fatalf("apply: %d %+v", reply.Code, reply.Body)
	}
	if !bytes.Contains(reply.Score, []byte("vox_live_amp")) {
		t.Fatalf("missing amp: %s", reply.Score)
	}
	view, err := buildStudioMixerView(scorePath, reply.Score)
	if err != nil || !view.Tracks[0].InsertOK {
		t.Fatalf("saved amp chain is not editable: %+v %v", view.Tracks, err)
	}
	bypass := mixerRoute(t, scorePath, studioEdit{Path: "vox_live_amp.bypass", Value: []byte(`"on"`)})
	if bypass.Code != http.StatusOK {
		t.Fatalf("bypass: %d %+v", bypass.Code, bypass.Body)
	}
	before := string(reply.Score)
	drive = .2
	reply = mixerRoute(t, scorePath, apply)
	if reply.Code != http.StatusOK || strings.Count(string(reply.Score), "fx vox_live_amp ") != 1 || before == string(reply.Score) || !strings.Contains(string(reply.Score), "bypass = false") {
		t.Fatalf("reapply: %d %+v %s", reply.Code, reply.Body, reply.Score)
	}
	original, _ := os.ReadFile(scorePath)
	apply.Track = "bass"
	reply = mixerRoute(t, scorePath, apply)
	after, _ := os.ReadFile(scorePath)
	if reply.Code < 400 || !bytes.Equal(original, after) {
		t.Fatalf("invalid target altered score: %d", reply.Code)
	}
}

func TestAmpMonitorCallbackDoesNotAllocate(t *testing.T) {
	a, _ := simulatedCaptureAudio(256, zeroAudioSource{})
	a.actual.SampleRate = 48000
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "mono1", AmpEnabled: true, AmpDrive: .7, AmpTone: .5, AmpCabinet: 1})
	a.playing.Store(true)
	input := [][]float32{make([]float32, 256), make([]float32, 256)}
	output := [][]float32{make([]float32, 256), make([]float32, 256)}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := a.renderPeriod(input, output); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("callback allocations: %g", allocations)
	}
}

// The saved track effect is exercised against a real journaled dry take and
// its score binding through the same renderer used for export.
func TestAmpReampRetainsTakeAndChangesRenderedPlayback(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id := captureTestTake(t, s)
	if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); err != nil {
		t.Fatal(err)
	}
	dry, err := s.takes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	beforeSource, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	renderSource := func(source []byte) []byte {
		p, ds, err := parseScoreForPath(s.path, source)
		if err != nil {
			t.Fatal(err)
		}
		if hasDiagnosticErrors(ds) {
			t.Fatal(ds)
		}
		var out bytes.Buffer
		if _, err = render.WAV(p, render.Options{AssetRoot: takeRoot(s.path), SampleRate: 48000, Bits: 32, Block: 128, Bars: 1}, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	baseline := renderSource(beforeSource)
	drive, tone, output, cab := float64(float32(.8)), float64(float32(.4)), -3., 1
	req := studioEdit{Action: "amp-chain", Track: "vox", Revision: studioRevision(beforeSource), AmpDrive: &drive, AmpTone: &tone, AmpOutput: &output, AmpCabinet: &cab}
	response := studioCall(t, s.routes(), "/api/mixer", req)
	if response.Code != http.StatusOK {
		t.Fatalf("apply amp: %d %s", response.Code, response.Body)
	}
	afterSource, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	wet := renderSource(afterSource)
	after, err := s.takes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Asset != after.Asset || dry.Frames != after.Frames {
		t.Fatal("reamp changed retained take")
	}
	if bytes.Equal(baseline, wet) {
		t.Fatal("track insert did not change rendered playback")
	}
	p, err := compileStudioSource(s.path, afterSource)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := schedule.Compile(p, takeRoot(s.path), 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	params := cfg.Track[0].InsertChain[0].AmpCab
	if params == nil {
		t.Fatal("saved track did not compile to an amp")
	}
	replay, err := ampcab.New(48000, *params)
	if err != nil {
		t.Fatal(err)
	}
	monitor, err := ampcab.New(48000, studioAmpParams(studioAudioMonitor{AmpEnabled: true, AmpDrive: float32(drive), AmpTone: float32(tone), AmpOutput: float32(output), AmpCabinet: cab}))
	if err != nil {
		t.Fatal(err)
	}
	source, err := sampleasset.LoadRegion(takeRoot(s.path), dry.Asset, 0, 0, 60, false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(source.Left)+128; i++ {
		var l, r float32
		if i < len(source.Left) {
			l, r = source.Left[i], source.Right[i]
		}
		ml, mr := monitor.Process(l, r)
		pl, pr := replay.Process(l, r)
		if math.Abs(float64(ml-pl)) > 1e-6 || math.Abs(float64(mr-pr)) > 1e-6 {
			t.Fatalf("monitor/reamp mismatch frame %d: %g/%g != %g/%g", i, ml, mr, pl, pr)
		}
	}
}

func TestAmpPracticeWhileArmedDoesNotRecord(t *testing.T) {
	a, _ := simulatedCaptureAudio(256, zeroAudioSource{})
	a.actual.SampleRate = 48000
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "mono1", AmpEnabled: true, AmpDrive: .7, AmpTone: .5, AmpCabinet: 1})
	var written int
	rec, err := capture.NewRecorder(8, 256, 2, func(_ capture.RecordedBlock, in [][]float32) error { written += len(in[0]); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ArmCapture(rec); err != nil {
		t.Fatal(err)
	}
	input := [][]float32{make([]float32, 256), make([]float32, 256)}
	output := [][]float32{make([]float32, 256), make([]float32, 256)}
	for i := range input[0] {
		input[0][i] = .2
	}
	for i := 0; i < 2; i++ {
		simulatedCapturePeriod(t, a, uint64(i*256), input, output)
	}
	if rec.Snapshot().Recording || output[0][255] == 0 {
		t.Fatalf("armed practice missing output or started recording: %+v", rec.Snapshot())
	}
	if err = rec.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	a.playing.Store(true)
	simulatedCapturePeriod(t, a, 512, input, output)
	if err = a.DisarmCapture(); err != nil {
		t.Fatal(err)
	}
	if written != 256 {
		t.Fatalf("practice recorded before start or missed record: %d", written)
	}
}

func TestAmpInputSelectionLockedWhileArmed(t *testing.T) {
	a, _ := simulatedCaptureAudio(256, zeroAudioSource{})
	a.actual.SampleRate = 48000
	options := studioAudioOptions{InputEnabled: true, MonitorMode: "mono1", PeriodFrames: 256, AmpEnabled: true, AmpDrive: .5, AmpTone: .5}
	a.SetMonitor(studioMonitorOptions(options))
	rec, err := capture.NewRecorder(4, 256, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ArmCapture(rec); err != nil {
		t.Fatal(err)
	}
	defer a.DisarmCapture()
	tr := &studioTransport{audio: a, audioOptions: options}
	changed := options
	changed.MonitorMode = "mono2"
	if err = tr.checkAudioConfigurationLocked(changed); err == nil {
		t.Fatal("input selection changed while armed")
	}
	changed = options
	changed.AmpEnabled = false
	if err = tr.checkAudioConfigurationLocked(changed); err == nil {
		t.Fatal("capture source changed while armed")
	}
	changed = options
	changed.AmpDrive = .8
	if err = tr.checkAudioConfigurationLocked(changed); err != nil {
		t.Fatalf("amp controls should remain adjustable: %v", err)
	}
}

func TestAmpCaptureAndMonitorCallbackDoesNotAllocate(t *testing.T) {
	a, _ := simulatedCaptureAudio(256, zeroAudioSource{})
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "mono1", AmpEnabled: true, AmpDrive: .7, AmpTone: .5, AmpCabinet: 1})
	rec, err := capture.NewRecorder(256, 256, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = a.ArmCapture(rec); err != nil {
		t.Fatal(err)
	}
	defer a.DisarmCapture()
	if err = rec.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	a.playing.Store(true)
	input := [][]float32{make([]float32, 256), make([]float32, 256)}
	output := [][]float32{make([]float32, 256), make([]float32, 256)}
	block := capture.Block{DeviceEpoch: 1, SampleRate: 48000, Period: 256, Frames: 256, Layout: capture.LayoutStereo}
	if n := testing.AllocsPerRun(100, func() {
		a.captureInput(block, input)
		if err := a.renderPeriod(input, output); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("capture + monitor allocations: %g", n)
	}
}

func TestAmpToggleSmoothsAndRetainsProcessor(t *testing.T) {
	a, _ := simulatedCaptureAudio(128, zeroAudioSource{})
	m := studioAudioMonitor{Gain: 1, Mode: "mono1", AmpDrive: .8, AmpTone: .5, AmpCabinet: 1}
	a.SetMonitor(m)
	initial := a.monitorState.Load().amp
	if initial == nil {
		t.Fatal("missing prepared bypass")
	}
	a.playing.Store(true)
	input := [][]float32{make([]float32, 128), make([]float32, 128)}
	output := [][]float32{make([]float32, 128), make([]float32, 128)}
	for i := range input[0] {
		input[0][i] = .2
	}
	for i := 0; i < 16; i++ {
		if err := a.renderPeriod(input, output); err != nil {
			t.Fatal(err)
		}
	}
	prior := output[0][127]
	m.AmpEnabled = true
	a.SetMonitor(m)
	if a.monitorState.Load().amp != initial {
		t.Fatal("enable reset amp history")
	}
	if err := a.renderPeriod(input, output); err != nil {
		t.Fatal(err)
	}
	if diff := math.Abs(float64(output[0][0] - prior)); diff > .01 {
		t.Fatalf("enable discontinuity %g", diff)
	}
	for i := 0; i < 24; i++ {
		if err := a.renderPeriod(input, output); err != nil {
			t.Fatal(err)
		}
	}
	prior = output[0][127]
	m.AmpEnabled = false
	a.SetMonitor(m)
	if a.monitorState.Load().amp != initial {
		t.Fatal("bypass reset amp history")
	}
	if err := a.renderPeriod(input, output); err != nil {
		t.Fatal(err)
	}
	if diff := math.Abs(float64(output[0][0] - prior)); diff > .01 {
		t.Fatalf("bypass discontinuity %g", diff)
	}
	if a.Snapshot().MonitorFXLatencyFrames != 31 {
		t.Fatal("bypass latency not reported")
	}
}

func BenchmarkAmpMonitorPeriod128(b *testing.B) {
	a, _ := simulatedCaptureAudio(128, zeroAudioSource{})
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "mono1", AmpEnabled: true, AmpDrive: .7, AmpTone: .5, AmpCabinet: 1})
	a.playing.Store(true)
	input := [][]float32{make([]float32, 128), make([]float32, 128)}
	output := [][]float32{make([]float32, 128), make([]float32, 128)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.renderPeriod(input, output); err != nil {
			b.Fatal(err)
		}
	}
}
