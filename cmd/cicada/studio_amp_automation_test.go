package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

// Use an actual retained audio take and two tracks sharing one declaration.
// The playback controls must affect only the selected track, not its sibling
// or the separately configured live input monitor.
func ampAutomationStudio(t *testing.T) (*studio, []byte, *liveplay.Player) {
	t.Helper()
	s := newTakeStudio(t, t.TempDir())
	source := strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1)
	source = strings.Replace(source, "track vox audio {}", "fx rig ampcab { drive = 0.1 tone = 0.4 output = -3dB cabinet = 1 }\ntrack vox audio { insert = rig }\ntrack harmony audio { insert = rig }", 1)
	source = strings.Replace(source, "bass = pulse", "bass = off", 1)
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	take := ampSyntheticTake(t, s)
	if err := s.commitTake(take.ID, take.Expected, nil); err != nil {
		t.Fatal(err)
	}
	sourceBytes, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	score, err := compileLiveScore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	player, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.stream, s.transport.playing, s.transport.audioNull, s.transport.sampleRate = player, true, true, liveSampleRate
	return s, sourceBytes, player
}

func ampSyntheticTake(t *testing.T, s *studio) takejournal.Take {
	t.Helper()
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	timeline := &takejournal.Timeline{StartTick: 0, EndTick: seq.TicksPerBar, PlaybackStartTick: 0, TempoMilli: 120000}
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 1, timeline)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]float32, 4096)
	for at := 0; at < 96000; {
		n := min(len(pcm), 96000-at)
		for i := 0; i < n; i++ {
			pcm[i] = float32(.2 * math.Sin(2*math.Pi*440*float64(at+i)/48000))
		}
		block := capture.RecordedBlock{RawFrame: uint64(at), Timing: capture.Block{DeviceEpoch: 1, EngineEpoch: 1, EngineFrame: int64(at), DeviceFrame: uint64(at), SampleRate: 48000, Frames: n, Layout: capture.LayoutMono}, Placement: capture.Placement{EngineFrame: int64(at)}}
		if err := s.takes.Write(id, block, [][]float32{pcm[:n]}); err != nil {
			t.Fatal(err)
		}
		at += n
	}
	if err := s.takes.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err := s.takes.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, err := s.takes.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return take
}

func ampReadEnergy(t *testing.T, player *liveplay.Player, frames int) float64 {
	t.Helper()
	data := make([]byte, frames*8)
	if _, err := io.ReadFull(player, data); err != nil {
		t.Fatal(err)
	}
	energy := 0.0
	for i := 0; i < len(data); i += 8 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(data[i:]))
		energy += float64(v) * float64(v)
	}
	return energy / float64(frames)
}

func TestAmpAutomationWritesRecordedTrackOnlyAndSurvivesReopen(t *testing.T) {
	s, source, player := ampAutomationStudio(t)
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		t.Fatal(err)
	}
	address, err := project.ParamAddressByName(p, "vox.amp_drive")
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := address.Value.(float64); !ok || math.Abs(n-.1) > 1e-6 {
		t.Fatalf("authored amp value missing: %+v", address)
	}
	monitorBefore := s.transport.audioOptions
	baseline := ampReadEnergy(t, player, 4096)
	id := beginAutomation(t, s, "vox.amp_drive", false)
	if _, err := io.CopyN(io.Discard, player, int64(liveSampleRate/8*8)); err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision(source), Name: id, Value: json.RawMessage(`{"number":0.9}`)})
	if r.Code != http.StatusOK {
		t.Fatalf("write movement: %d %s", r.Code, r.Body)
	}
	if got, ok := player.OverrideValue(0, kernel.ParamAmpDrive); !ok || math.Abs(float64(got)-.9) > 1e-6 {
		t.Fatalf("missing selected-track override: %g %t", got, ok)
	}
	if _, ok := player.OverrideValue(1, kernel.ParamAmpDrive); ok {
		t.Fatal("shared effect changed sibling track")
	}
	_ = ampReadEnergy(t, player, 4096)
	wet := ampReadEnergy(t, player, 4096)
	if math.Abs(wet-baseline) < 1e-6 {
		t.Fatalf("amp movement did not change rendered audio: before=%g after=%g", baseline, wet)
	}
	if got, _ := os.ReadFile(s.path); !bytes.Equal(got, source) {
		t.Fatal("preview wrote source")
	}
	if s.transport.audioOptions != monitorBefore {
		t.Fatal("playback automation changed input monitor")
	}
	points := append([]recordedAutomationPoint(nil), s.automationRecording.Points...)
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "keep", Revision: studioRevision(source), Name: id})
	if r.Code != http.StatusOK {
		t.Fatalf("keep: %d %s", r.Code, r.Body)
	}
	after, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	r = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(after)})
	if r.Code != http.StatusOK {
		t.Fatalf("undo: %d %s", r.Code, r.Body)
	}
	restored, _ := os.ReadFile(s.path)
	if !bytes.Equal(restored, source) {
		t.Fatal("one undo failed to restore original score")
	}
	r = studioCall(t, s.routes(), "/api/redo", studioEdit{Revision: studioRevision(restored)})
	if r.Code != http.StatusOK {
		t.Fatalf("redo: %d %s", r.Code, r.Body)
	}
	redone, _ := os.ReadFile(s.path)
	if !bytes.Equal(redone, after) {
		t.Fatal("redo failed to restore recorded curve")
	}
	if err = s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.shutdown()
	reopenedSource, err := os.ReadFile(reopened.path)
	if err != nil || !bytes.Equal(reopenedSource, after) {
		t.Fatalf("saved score changed during reopen: %v", err)
	}
	p, err = compileStudioSource(reopened.path, reopenedSource)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, lane := range p.Automation {
		if lane.Path == "vox.amp_drive" {
			found = true
			if len(lane.Points) != len(points) {
				t.Fatal("curve point count changed")
			}
			for i, point := range lane.Points {
				if point.Tick != points[i].Tick || point.Value.Number == nil || math.Abs(*point.Value.Number-points[i].Value) > 1e-6 {
					t.Fatalf("curve point %d changed", i)
				}
			}
		}
	}
	if !found {
		t.Fatal("saved track amp curve missing after reopen")
	}
}

func TestAmpAutomationRejectsInvalidTargetsAndCaptureConflicts(t *testing.T) {
	for _, tc := range []struct{ name, definition string }{
		{"missing", "track vox audio {}"},
		{"multiple", "fx a ampcab {}\nfx b ampcab {}\ntrack vox audio { insert = a -> b }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTakeStudio(t, t.TempDir())
			source := strings.Replace(audioTakeScore, "track vox audio {}", tc.definition, 1)
			if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			revision, err := project.ReadScoreRevision(s.path)
			if err != nil {
				t.Fatal(err)
			}
			r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "start", Path: "vox.amp_drive", Revision: studioRevision([]byte(source)), SourceRevision: revision})
			if r.Code < 400 || s.automationRecording != nil {
				t.Fatalf("invalid target accepted: %d %s", r.Code, r.Body)
			}
			after, _ := os.ReadFile(s.path)
			if string(after) != source {
				t.Fatal("invalid target changed source")
			}
		})
	}
	s, source, _ := ampAutomationStudio(t)
	revision, err := project.ReadScoreRevision(s.path)
	if err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "start", Path: "vox.amp_drive", Revision: "stale", SourceRevision: revision})
	if r.Code != http.StatusConflict || s.automationRecording != nil {
		t.Fatalf("stale revision accepted: %d %s", r.Code, r.Body)
	}
	s.captureID = "active-take"
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "start", Path: "vox.amp_drive", Revision: studioRevision(source), SourceRevision: revision})
	s.captureID = ""
	if r.Code != http.StatusConflict || s.automationRecording != nil {
		t.Fatalf("capture and Write combined: %d %s", r.Code, r.Body)
	}
	id := beginAutomation(t, s, "vox.amp_drive", false)
	r = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "arm", Track: "vox", Revision: studioRevision(source)})
	if r.Code < 400 || s.captureID != "" {
		t.Fatalf("armed audio during Write: %d %s", r.Code, r.Body)
	}
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision(source), Name: id, Value: json.RawMessage(`{"number":1.1}`)})
	if r.Code < 400 || len(s.automationRecording.Points) != 1 {
		t.Fatalf("invalid amp value recorded: %d %s", r.Code, r.Body)
	}
	r = studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "discard", Revision: studioRevision(source), Name: id})
	if r.Code != http.StatusOK || s.automationRecording != nil {
		t.Fatalf("discard: %d %s", r.Code, r.Body)
	}
}

func TestAmpAutomationAcceptsInstrumentTrack(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := strings.Replace(audioTakeScore, "track bass acid {}", "fx rig ampcab {}\ntrack bass acid { insert = rig }", 1)
	if err := os.WriteFile(s.path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	score, err := compileLiveScore(s.path)
	if err != nil {
		t.Fatal(err)
	}
	player, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	s.transport.stream, s.transport.playing, s.transport.audioNull, s.transport.sampleRate = player, true, true, liveSampleRate
	id := beginAutomation(t, s, "bass.amp_drive", false)
	r := studioCall(t, s.routes(), "/api/automation/recording", studioEdit{Action: "value", Revision: studioRevision([]byte(source)), Name: id, Value: json.RawMessage(`{"number":0.6}`)})
	if r.Code != http.StatusOK {
		t.Fatalf("instrument amp automation: %d %s", r.Code, r.Body)
	}
	if got, ok := player.OverrideValue(1, kernel.ParamAmpDrive); !ok || math.Abs(float64(got)-.6) > 1e-6 {
		t.Fatalf("missing instrument override: %g %t", got, ok)
	}
}
