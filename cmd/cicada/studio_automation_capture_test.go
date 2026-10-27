package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/fx/ampcab"
	"m31labs.dev/cicada/project"
)

func TestAutomationDuringCaptureKeepsDryTakeAndOwnRevision(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	a, _ := simulatedCaptureAudio(8, zeroAudioSource{})
	score, err := compileLiveScore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	player, err := liveplay.New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.audio, s.transport.stream, s.transport.sampleRate, s.transport.playing = a, player, 48000, true
	s.transport.audioOptions.InputEnabled = true
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "arm", Track: "vox", Scene: "main", Revision: studioRevision([]byte(audioTakeScore))})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	count, _ := capture.NewCountIn(48000, 120000, 0)
	if err := s.captureRecorder.Begin(count, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	id := beginAutomation(t, s, "bass.level", false)
	if _, err := io.CopyN(io.Discard, player, 48000); err != nil {
		t.Fatal(err)
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision([]byte(audioTakeScore)), Name: id, Value: json.RawMessage(`{"number":-9}`)})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Revision: studioRevision([]byte(audioTakeScore)), Name: id})
	if response.Code != http.StatusConflict || !s.automationRecording.Active {
		t.Fatalf("keep during capture lost active pass: %s", response.Body.String())
	}
	input, output := [][]float32{make([]float32, 8), make([]float32, 8)}, [][]float32{make([]float32, 8), make([]float32, 8)}
	input[0][0] = .75
	if err := a.Play(); err != nil {
		t.Fatal(err)
	}
	simulatedCapturePeriod(t, a, 0, input, output)
	response = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "stop", Revision: studioRevision([]byte(audioTakeScore))})
	if response.Code != http.StatusOK {
		t.Fatalf("finish: %s", response.Body.String())
	}
	takes := s.takes.Takes()
	if len(takes) != 1 {
		t.Fatal(takes)
	}
	pcm, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), takes[0].Asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	if got := math.Float32frombits(binary.LittleEndian.Uint32(pcm[44:])); got != .75 {
		t.Fatalf("dry PCM changed: %g", got)
	}
	source, _ := os.ReadFile(s.path)
	if s.automationRecording.Active || s.automationRecording.Revision != studioRevision(source) {
		t.Fatal("pass not frozen and rebased after capture")
	}
	draftBytes, err := os.ReadFile(s.automationDraftPath())
	if err != nil {
		t.Fatal(err)
	}
	var recovered studioAutomationRecording
	if err := json.Unmarshal(draftBytes, &recovered); err != nil || recovered.Revision != studioRevision(source) || recovered.Active {
		t.Fatal("the recovery draft did not retain the committed take revision", err)
	}
	points := append([]recordedAutomationPoint(nil), s.automationRecording.Points...)
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Revision: studioRevision([]byte(audioTakeScore)), Name: id})
	if response.Code != http.StatusOK {
		t.Fatalf("keep: %s", response.Body.String())
	}
	after, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, after)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lane := range p.Automation {
		if lane.Path == "bass.level" {
			found = true
			if len(lane.Points) != len(points) {
				t.Fatal("lost points")
			}
			for i, point := range lane.Points {
				if point.Tick != points[i].Tick || point.Value.Number == nil || *point.Value.Number != points[i].Value {
					t.Fatal("saved curve differs from the recorded movement", i)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing curve")
	}
	response = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	undone, _ := os.ReadFile(s.path)
	if !bytes.Equal(undone, source) {
		t.Fatal("undo did not retain audio take")
	}
	if _, err = project.ReadScoreRevision(s.path); err != nil {
		t.Fatal(err)
	}
}

func TestBoundAmpWriteMonitorsAndKeepsDryCapture(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := strings.Replace(audioTakeScore, "track vox audio {}", "fx rig ampcab { drive = 0.1 tone = 0.4 output = -3dB cabinet = 1 }\ntrack vox audio { insert = rig }", 1)
	source = strings.Replace(source, "bass = pulse", "bass = off", 1)
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	a, _ := simulatedCaptureAudio(128, zeroAudioSource{})
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "mono1", AmpDrive: .35, AmpTone: .5})
	s.transport.audio, s.transport.sampleRate = a, 48000
	s.transport.audioOptions = studioAudioOptions{InputEnabled: true, MonitorMode: "mono1", MonitorGain: 1, AmpDrive: .35, AmpTone: .5, PeriodFrames: 128}
	revision, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	prepare := studioEdit{Action: "prepare", Path: "vox.amp_drive", Revision: studioRevision([]byte(source)), SourceRevision: revision, Value: json.RawMessage(`{"monitor":true}`)}
	response := studioCall(t, s.routes(), "/api/automation/recording", prepare)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	pass := s.automationRecording
	if !pass.Prepared || pass.Active || s.captureID != "" || a.ampFollower.Load() != nil {
		t.Fatal("prepare unexpectedly started audio")
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "discard", Name: pass.ID, Revision: studioRevision([]byte(source))})
	if response.Code != http.StatusOK || s.automationRecording != nil {
		t.Fatal("cannot cancel preparation")
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", prepare)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	pass = s.automationRecording
	bad := studioEdit{Action: "record", Track: "vox", Revision: studioRevision([]byte(source)), Recording: &studioRecordingSettings{Start: "@1.1.1", End: "@2.1.1", Passes: 2}}
	response = studioCall(t, s.routes(), "/api/takes", bad)
	if response.Code == http.StatusOK || !pass.Prepared {
		t.Fatal("accepted multi-pass prepared capture")
	}
	bad.Recording.Passes = 1
	if err := os.WriteFile(s.path, []byte(source+"// external edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	response = studioCall(t, s.routes(), "/api/takes", bad)
	if response.Code != http.StatusConflict || !pass.Prepared || s.captureID != "" {
		t.Fatal("source edit did not retain preparation without recording")
	}
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	response = studioCall(t, s.routes(), "/api/takes", bad)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if pass.Prepared || !pass.Active || pass.MonitorFollower == nil || a.ampFollower.Load() == nil {
		t.Fatal("Record did not bind prepared amp before playback")
	}
	input, output := [][]float32{make([]float32, 128), make([]float32, 128)}, [][]float32{make([]float32, 128), make([]float32, 128)}
	for i := range input[0] {
		input[0][i] = .2 + .1*float32(math.Sin(2*math.Pi*float64(i)/128))
		input[1][i] = .8
	}
	reference, err := ampcab.New(48000, ampcab.Params{Drive: .1, Tone: .4, Output: -3, Cabinet: 1})
	if err != nil {
		t.Fatal(err)
	}
	var expectedMonitor float32
	for i := 0; i < 760; i++ {
		simulatedCapturePeriod(t, a, uint64(i*128), input, output)
		for _, sample := range input[0] {
			expectedMonitor, _ = reference.Process(sample, sample)
		}
		if i%8 == 0 {
			time.Sleep(time.Millisecond)
		}
	}
	low := output[0][127]
	if math.Abs(float64(low-expectedMonitor)) > 1e-4 {
		t.Fatalf("prepared monitor did not start with saved track amp: %g vs %g", low, expectedMonitor)
	}
	// Exercise the capacity boundary without rendering a 64-bar fixture.
	// The synthetic prior ticks precede the current clock; no such draft is kept.
	pointsBeforeCapacity := pass.Points
	pass.Points = make([]recordedAutomationPoint, 1024)
	for i := range pass.Points {
		pass.Points[i] = recordedAutomationPoint{int64(i-1024) * 240, .1}
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Name: pass.ID, Revision: studioRevision([]byte(source)), Value: json.RawMessage(`{"number":0.9}`)})
	if response.Code != http.StatusUnprocessableEntity || pass.Active || a.ampFollower.Load() == nil {
		t.Fatal("capacity did not freeze and retain monitoring", response.Body.String())
	}
	if got, ok := s.transport.stream.OverrideValue(0, kernel.ParamAmpDrive); !ok || math.Abs(float64(got)-.1) > 1e-6 {
		t.Fatal("unsaveable value changed the preview", got, ok)
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Name: pass.ID, Revision: studioRevision([]byte(source)), Value: json.RawMessage(`{"number":0.8}`)})
	if response.Code != http.StatusConflict || a.ampFollower.Load() == nil {
		t.Fatal("inactive capacity draft lost its monitor")
	}
	pass.Points, pass.Active = pointsBeforeCapacity, true
	if err := s.persistAutomationRecording(true); err != nil {
		t.Fatal(err)
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Name: pass.ID, Revision: studioRevision([]byte(source)), Value: json.RawMessage(`{"number":0.9}`)})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	for i := 760; i < 780; i++ {
		simulatedCapturePeriod(t, a, uint64(i*128), input, output)
	}
	// Exercise control publication while the audio callback owns the player.
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-start
		for i := 0; i < 40; i++ {
			a.rendering.Add(1)
			_ = a.renderPeriod(input, output)
		}
		close(done)
	}()
	close(start)
	for i := 0; i < 8; i++ {
		response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Name: pass.ID, Revision: studioRevision([]byte(source)), Value: json.RawMessage(`{"number":0.9}`)})
		if response.Code != http.StatusOK {
			<-done
			t.Fatal(response.Body.String())
		}
	}
	<-done
	high := output[0][127]
	if math.Abs(float64(high-low)) < .001 {
		t.Fatalf("monitor amp did not follow recorded movement: %g %g", low, high)
	}

	s.transport.mu.Lock()
	changed := s.transport.audioOptions
	changed.AmpTone = .9
	configErr := s.transport.checkAudioConfigurationLocked(changed)
	s.transport.mu.Unlock()
	if configErr == nil {
		t.Fatal("allowed independent amp configuration during bound pass")
	}
	s.transport.mu.Lock()
	muted := s.transport.audioOptions
	muted.MonitorMuted = true
	if err := s.transport.checkAudioConfigurationLocked(muted); err != nil {
		s.transport.mu.Unlock()
		t.Fatal("mute was blocked during bound pass", err)
	}
	s.transport.audioOptions = muted
	a.SetMonitor(studioMonitorOptions(muted))
	s.transport.mu.Unlock()
	a.rendering.Add(1)
	if err := a.renderPeriod(input, output); err != nil {
		t.Fatal(err)
	}
	for _, sample := range output[0] {
		if sample != 0 {
			t.Fatal("monitor mute was not immediate")
		}
	}
	if original := a.monitorState.Load(); original.AmpEnabled || original.AmpDrive != .35 {
		t.Fatal("temporary binding changed persistent monitor settings")
	}
	response = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "stop", Revision: studioRevision([]byte(source))})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if a.ampFollower.Load() != nil || s.transport.monitorWrite {
		t.Fatal("monitor binding not released")
	}
	if !s.transport.audioOptions.MonitorMuted {
		t.Fatal("finish reverted the user's mute")
	}
	takes := s.takes.Takes()
	if len(takes) != 1 {
		t.Fatal(takes)
	}
	pcm, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), takes[0].Asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	if got, expected := math.Float32frombits(binary.LittleEndian.Uint32(pcm[44:])), input[0][0]; got != expected {
		t.Fatalf("input was not captured dry: got %g want %g", got, expected)
	}
	if right := math.Float32frombits(binary.LittleEndian.Uint32(pcm[48:])); right != input[0][0] {
		t.Fatal("prepared mono input was not duplicated in the dry take")
	}
	response = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Name: pass.ID, Revision: studioRevision([]byte(source))})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	saved, _ := os.ReadFile(s.path)
	p, err := compileStudioSource(s.path, saved)
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range p.Automation {
		if lane.Path == "vox.amp_drive" {
			if lane.Points[len(lane.Points)-1].Value.Number == nil || *lane.Points[len(lane.Points)-1].Value.Number != .9 {
				t.Fatal("movement not saved")
			}
			return
		}
	}
	t.Fatal("amp curve not saved")
}

func TestConcurrentAutomationAndTakeRetainOutsideEdit(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	a, _ := simulatedCaptureAudio(8, zeroAudioSource{})
	score, err := compileLiveScore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	player, err := liveplay.New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.audio, s.transport.stream, s.transport.sampleRate, s.transport.playing = a, player, 48000, true
	s.transport.audioOptions.InputEnabled = true
	original := []byte(audioTakeScore)
	r := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "arm", Track: "vox", Scene: "main", Revision: studioRevision(original)})
	if r.Code != http.StatusOK {
		t.Fatal(r.Body.String())
	}
	count, _ := capture.NewCountIn(48000, 120000, 0)
	if err = s.captureRecorder.Begin(count, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	id := beginAutomation(t, s, "bass.level", false)
	input, output := [][]float32{make([]float32, 8), make([]float32, 8)}, [][]float32{make([]float32, 8), make([]float32, 8)}
	input[0][0] = .3
	if err = a.Play(); err != nil {
		t.Fatal(err)
	}
	simulatedCapturePeriod(t, a, 0, input, output)
	external := append(bytes.Clone(original), []byte("// external change\n")...)
	if err = os.WriteFile(s.path, external, 0600); err != nil {
		t.Fatal(err)
	}
	r = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "stop", Revision: studioRevision(original)})
	if r.Code == http.StatusOK {
		t.Fatal("outside edit was overwritten")
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, external) {
		t.Fatal("outside edit changed")
	}
	if s.automationRecording == nil || s.automationRecording.Active || s.automationRecording.Revision != studioRevision(original) {
		t.Fatal("automation draft not retained at its original revision")
	}
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Name: id, Revision: studioRevision(external)})
	if r.Code != http.StatusConflict {
		t.Fatalf("stale pass did not conflict: %s", r.Body.String())
	}
	if len(s.takes.Takes()) != 1 {
		t.Fatal("audio take was lost")
	}
}

func TestBoundAmpRejectsReplacedPlayerWithoutLeakingInput(t *testing.T) {
	_, _, player := ampAutomationStudio(t)
	target, err := player.ResolvePreviewTrack("vox")
	if err != nil {
		t.Fatal(err)
	}
	follower := &studioAmpFollower{player: player, target: target}
	a, _ := simulatedCaptureAudio(128, zeroAudioSource{})
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "mono1", AmpDrive: .35, AmpTone: .5})
	if err = a.SetAmpFollower(follower); err != nil {
		t.Fatal(err)
	}
	a.SetSource(zeroAudioSource{})
	if err = a.Play(); err != nil {
		t.Fatal(err)
	}
	input, output := [][]float32{make([]float32, 128), make([]float32, 128)}, [][]float32{make([]float32, 128), make([]float32, 128)}
	for i := range input[0] {
		input[0][i] = .25
	}
	if err = a.renderPeriod(input, output); err != nil {
		t.Fatal(err)
	}
	if !follower.broken.Load() {
		t.Fatal("replacement player was not rejected")
	}
	for _, v := range output[0] {
		if v != 0 {
			t.Fatal("invalid binding leaked monitored input")
		}
	}
	if err = a.SetAmpFollower(nil); err != nil {
		t.Fatal(err)
	}
	if err = a.renderPeriod(input, output); err != nil {
		t.Fatal(err)
	}
	if output[0][127] == 0 {
		t.Fatal("original monitor was not restored")
	}
}

func TestPreparedAmpStartFailureRetainsRetryablePreparation(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := strings.Replace(audioTakeScore, "track vox audio {}", "fx rig ampcab {}\ntrack vox audio { insert=rig }", 1)
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	a, _ := simulatedCaptureAudio(128, zeroAudioSource{})
	a.rawOutput = true
	a.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "stereo", AmpDrive: .35, AmpTone: .5})
	s.transport.audio, s.transport.sampleRate = a, 48000
	s.transport.audioOptions = studioAudioOptions{InputEnabled: true, MonitorMode: "stereo", MonitorGain: 1, AmpDrive: .35, AmpTone: .5, PeriodFrames: 128}
	revision, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	prepare := studioEdit{Action: "prepare", Path: "vox.amp_drive", Revision: studioRevision([]byte(source)), SourceRevision: revision, Value: json.RawMessage(`{"monitor":true}`)}
	r := studioCall(t, s.routes(), "/api/automation/recording", prepare)
	if r.Code != http.StatusOK {
		t.Fatal(r.Body.String())
	}
	pass := s.automationRecording
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Name: pass.ID, Revision: studioRevision([]byte(source))})
	if r.Code != http.StatusConflict {
		t.Fatal("prepared pass accepted Keep")
	}
	record := studioEdit{Action: "record", Track: "vox", Revision: studioRevision([]byte(source)), Recording: &studioRecordingSettings{Start: "@1.1.1", End: "@2.1.1", Passes: 1}}
	r = studioCall(t, s.routes(), "/api/takes", record)
	if r.Code == http.StatusOK || !pass.Prepared || pass.Active || s.captureID != "" || a.ampFollower.Load() != nil {
		t.Fatalf("failure did not roll back preparation: %s", r.Body.String())
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, []byte(source)) {
		t.Fatal("failed start changed project")
	}
	b, _ := simulatedCaptureAudio(128, zeroAudioSource{})
	b.SetMonitor(studioAudioMonitor{Gain: 1, Mode: "stereo", AmpDrive: .35, AmpTone: .5})
	s.transport.audio = b
	r = studioCall(t, s.routes(), "/api/takes", record)
	if r.Code != http.StatusOK || !pass.Active || pass.Prepared || b.ampFollower.Load() == nil {
		t.Fatalf("preparation could not retry: %s", r.Body.String())
	}
	s.releaseAutomationMonitor(pass)
	s.transport.stop()
	_ = s.transport.disarmCapture()
	_ = s.captureRecorder.Close()
	s.captureID = ""
	s.captureRecorder = nil
}

// Exposing only the base interface models a backend without prepared-channel support.
type noPreparedCaptureDevice struct{ studioAudioDevice }

func TestPreparedCaptureUnsupportedDeviceCleanup(t *testing.T) {
	for _, opened := range []bool{false, true} {
		t.Run(fmt.Sprintf("opened=%t", opened), func(t *testing.T) {
			transport := newStudioTransport("")
			transport.sampleRate = 48000
			transport.audioOptions.InputEnabled = true
			a, stream := simulatedCaptureAudio(128, zeroAudioSource{})
			device := noPreparedCaptureDevice{a}
			if opened {
				transport.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return device, nil }
			} else {
				transport.audio = device
			}
			recorder, err := capture.NewRecorder(8, 128, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer recorder.Close()
			if err = transport.armCapture(recorder, "mono1"); err == nil {
				t.Fatal("unsupported device accepted prepared input")
			}
			if opened && (stream.closes != 1 || transport.audio != nil) {
				t.Fatal("new device was not closed")
			}
			if !opened && (stream.closes != 0 || transport.audio == nil) {
				t.Fatal("existing device was closed")
			}
		})
	}
}

func TestPreparedAmpRejectsGenericTakeWithoutStartingCapture(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := strings.Replace(audioTakeScore, "track vox audio {}", "fx rig ampcab {}\ntrack vox audio { insert=rig }", 1)
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	rev, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "prepare", Path: "vox.amp_drive", Revision: studioRevision([]byte(source)), SourceRevision: rev, Value: json.RawMessage(`{"monitor":true}`)})
	if r.Code != http.StatusOK {
		t.Fatal(r.Body.String())
	}
	pass := s.automationRecording
	id, err := s.takes.Begin("vox", "main", studioRevision([]byte(source)), 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := capture.NewRecorder(8, 128, 2, s.takes.Writer(id))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	a, stream := simulatedCaptureAudio(128, zeroAudioSource{})
	s.transport.audio = a
	s.captureID, s.captureRecorder = id, recorder
	if err := s.startAudioTake(); err == nil || !strings.Contains(err.Error(), "single-pass recording range") {
		t.Fatal("generic take was not rejected", err)
	}
	if recorder.Snapshot().Recording || stream.starts != 0 || a.ampFollower.Load() != nil || !pass.Prepared {
		t.Fatal("rejection changed capture, device, or preparation")
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, []byte(source)) {
		t.Fatal("rejection changed source")
	}
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "discard", Name: pass.ID, Revision: studioRevision([]byte(source))})
	if r.Code != http.StatusOK || s.automationRecording != nil {
		t.Fatal("preparation could not be canceled", r.Body.String())
	}
	s.captureID = ""
	s.captureRecorder = nil
}
