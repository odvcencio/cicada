package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/projectcopy"
	"m31labs.dev/cicada/host/takejournal"
)

const audioTakeScore = "cicada 2\n// retain this comment\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { bass = pulse vox = off // keep binding comment\n}\nsong { main }\n"

func newTakeStudio(t *testing.T, dir string) *studio {
	t.Helper()
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(audioTakeScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := newStudio(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.openTakes(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.transport.close(); s.takes.Close() })
	return s
}
func captureTestTake(t *testing.T, s *studio) string {
	t.Helper()
	source, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.takes.Begin("vox", "main", studioRevision(source), 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	r, err := capture.NewRecorder(4, 48, 2, s.takes.Writer(id))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := capture.NewCountIn(48000, 120000, 0)
	if err = r.Begin(c, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	pcm := [][]float32{make([]float32, 48), make([]float32, 48)}
	for i := range pcm[0] {
		pcm[0][i] = float32(i) / 48
		pcm[1][i] = -float32(i) / 24
	}
	r.Capture(capture.Block{DeviceEpoch: 1, EngineEpoch: 1, SampleRate: 48000, Frames: 48, Layout: capture.LayoutStereo}, pcm)
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.takes.Finalize(id, r.Snapshot().Incomplete); err != nil {
		t.Fatal(err)
	}
	if err = s.takes.Publish(id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The child exits abruptly from the durable boundary, bypassing all cleanup.
func TestTakeCrashChild(t *testing.T) {
	stage := os.Getenv("CICADA_TAKE_CRASH_STAGE")
	if stage == "" {
		return
	}
	dir := os.Getenv("CICADA_TAKE_CRASH_DIR")
	s := newTakeStudio(t, dir)
	s.takes.Boundary = func(at takejournal.Stage) {
		if string(at) == stage {
			os.Exit(73)
		}
	}
	id := captureTestTake(t, s)
	if stage == string(takejournal.Conflict) {
		if err := os.WriteFile(s.path, []byte(audioTakeScore+"// external edit\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash stage was not reached")
}
func TestTakeCrashRecoveryEveryStage(t *testing.T) {
	for _, stage := range []takejournal.Stage{takejournal.Begun, takejournal.Chunk, takejournal.WAVWritten, takejournal.Finalized, takejournal.BlobLinked, takejournal.Blob, takejournal.TakeLinked, takejournal.Published, takejournal.Prepared, takejournal.SourceStaged, takejournal.SourceSwapped, takejournal.Source, takejournal.Committed, takejournal.Conflict} {
		t.Run(string(stage), func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestTakeCrashChild$")
			cmd.Env = append(os.Environ(), "CICADA_TAKE_CRASH_STAGE="+string(stage), "CICADA_TAKE_CRASH_DIR="+dir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 73 {
				t.Fatalf("child did not crash at %s: %v\n%s", stage, err, output)
			}
			s, err := newStudio(filepath.Join(dir, "main.cicada"))
			if err != nil {
				t.Fatal(err)
			}
			takes := s.takes.Takes()
			if len(takes) != 1 {
				t.Fatalf("retained takes=%d", len(takes))
			}
			take := takes[0]
			recovered := 0
			if stage == takejournal.Begun {
				if take.Frames != 0 {
					t.Fatal("empty begin gained audio")
				}
			} else {
				recovered = 1
				if take.Frames != 48 {
					t.Fatalf("frames=%d", take.Frames)
				}
				data, err := os.ReadFile(filepath.Join(dir, take.Asset.Path))
				if err != nil {
					t.Fatal(err)
				}
				if len(data) != 44+48*8 {
					t.Fatalf("WAV bytes=%d", len(data))
				}
				for i := 0; i < 48; i++ {
					for ch := 0; ch < 2; ch++ {
						want := float32(i) / 48
						if ch == 1 {
							want = -float32(i) / 24
						}
						got := math.Float32frombits(binary.LittleEndian.Uint32(data[44+(i*2+ch)*4:]))
						if got != want {
							t.Fatalf("lost finalized PCM at %d/%d: %v != %v", i, ch, got, want)
						}
					}
				}
				before, _ := os.ReadFile(s.path)
				if stage == takejournal.Conflict {
					if !strings.Contains(string(before), "// external edit") {
						t.Fatal("external edit lost")
					}
					if take.Stage != takejournal.Conflict {
						t.Fatal("conflict not retained")
					}
				}
				if stage != takejournal.Prepared && stage != takejournal.SourceStaged && stage != takejournal.SourceSwapped && stage != takejournal.Source && stage != takejournal.Committed {
					if err = s.commitTake(take.ID, studioRevision(before), nil); err != nil {
						t.Fatal(err)
					}
				}
				now, _ := os.ReadFile(s.path)
				if !strings.Contains(string(now), take.ID+"-clip") {
					t.Fatal("recovered take missing from score")
				}
			}
			first, _ := os.ReadFile(s.path)
			s.transport.close()
			s.takes.Close()
			reopened, err := newStudio(s.path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.transport.close()
			defer reopened.takes.Close()
			again, _ := os.ReadFile(s.path)
			if !bytes.Equal(first, again) {
				t.Fatal("second recovery changed source")
			}
			t.Logf("METRIC crash_stage=%s recovered_takes=%d lost_finalized_frames=0", stage, recovered)
		})
	}
}
func TestTakeRetainedPassSelectionUndoAndSaveAs(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	first := captureTestTake(t, s)
	if err := s.commitTake(first, studioRevision([]byte(audioTakeScore)), nil); err != nil {
		t.Fatal(err)
	}
	beforeSecond, _ := os.ReadFile(s.path)
	second := captureTestTake(t, s)
	if err := s.commitTake(second, studioRevision(beforeSecond), nil); err != nil {
		t.Fatal(err)
	}
	secondSource, _ := os.ReadFile(s.path)
	for _, id := range []string{first, second} {
		if !strings.Contains(string(secondSource), "asset "+id) || !strings.Contains(string(secondSource), "clip "+id+"-clip") {
			t.Fatalf("pass %s was discarded", id)
		}
	}
	if !strings.Contains(string(secondSource), "vox = "+second+"-clip // keep binding comment") {
		t.Fatal("newest pass not active or comment changed")
	}
	result := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "select", TakeID: first, Revision: studioRevision(secondSource)})
	if result.Code != http.StatusOK {
		t.Fatal(result.Body.String())
	}
	selected, _ := os.ReadFile(s.path)
	if !strings.Contains(string(selected), "vox = "+first+"-clip") {
		t.Fatal("older pass not selected")
	}
	response := studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(selected)})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	undone, _ := os.ReadFile(s.path)
	if !bytes.Equal(secondSource, undone) {
		t.Fatal("selection undo did not restore exact source")
	}
	// Undo take publication itself: its asset remains a history/journal root.
	response = studioCall(t, s.routes(), "/api/undo", studioEdit{Revision: studioRevision(undone)})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	target := filepath.Join(t.TempDir(), "saved.cicada")
	if err := projectcopy.SaveAs(s.path, target); err != nil {
		t.Fatal(err)
	}
	s.transport.close()
	s.takes.Close()
	if err := os.RemoveAll(filepath.Dir(s.path)); err != nil {
		t.Fatal(err)
	}
	copy, err := newStudio(target)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.transport.close()
	defer copy.takes.Close()
	if len(copy.takes.Takes()) != 2 {
		t.Fatal("Save As lost retained/history pass")
	}
	source, _ := os.ReadFile(target)
	if strings.Contains(string(source), "asset "+second) {
		t.Fatal("recovery replayed an undone take")
	}
	if err := copy.commitTake(second, studioRevision(source), nil); err != nil {
		t.Fatal(err)
	}
}
func TestTakeRevisionConflictRecovery(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id := captureTestTake(t, s)
	external := []byte(audioTakeScore + "// edit while recording\n")
	if err := os.WriteFile(s.path, external, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); !errors.Is(err, errTakeRevision) {
		t.Fatalf("conflict=%v", err)
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, external) {
		t.Fatal("conflict overwrote external source")
	}
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "recover", TakeID: id, Revision: studioRevision(current)})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	recovered, _ := os.ReadFile(s.path)
	if !bytes.Contains(recovered, []byte("// edit while recording")) {
		t.Fatal("recovery dropped intervening edit")
	}
}

func TestTakeConflictAtSourceExchangeRetainsBothVersions(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id := captureTestTake(t, s)
	external := []byte(audioTakeScore + "// concurrent replacement\n")
	s.takes.Boundary = func(stage takejournal.Stage) {
		if stage == takejournal.SourceStaged {
			if err := os.WriteFile(s.path, external, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.commitTake(id, studioRevision([]byte(audioTakeScore)), nil); !errors.Is(err, errTakeRevision) {
		t.Fatalf("exchange conflict=%v", err)
	}
	current, _ := os.ReadFile(s.path)
	if !bytes.Equal(current, external) {
		t.Fatal("exchange conflict lost external source")
	}
	take, _ := s.takes.Get(id)
	if take.Stage != takejournal.Conflict {
		t.Fatal("exchange conflict discarded recovery")
	}
	s.takes.Boundary = nil
	if err := s.commitTake(id, studioRevision(current), nil); err != nil {
		t.Fatal(err)
	}
	current, _ = os.ReadFile(s.path)
	if !bytes.Contains(current, []byte("// concurrent replacement")) {
		t.Fatal("reapplication lost external edit")
	}
}

func TestTakeAPIArmStopUsesLaneDCaptureWriter(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	a, _ := simulatedCaptureAudio(8, zeroAudioSource{})
	s.transport.mu.Lock()
	s.transport.audio = a
	s.transport.sampleRate = 48000
	s.transport.audioOptions.InputEnabled = true
	s.transport.mu.Unlock()
	response := studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "arm", Track: "vox", Scene: "main", Revision: studioRevision([]byte(audioTakeScore))})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	c, _ := capture.NewCountIn(48000, 120000, 0)
	if err := s.captureRecorder.Begin(c, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	input, output := [][]float32{make([]float32, 8), make([]float32, 8)}, [][]float32{make([]float32, 8), make([]float32, 8)}
	input[0][0] = 0.75
	if err := a.Play(); err != nil {
		t.Fatal(err)
	}
	simulatedCapturePeriod(t, a, 0, input, output)
	response = studioCall(t, s.routes(), "/api/takes", studioEdit{Action: "stop", Revision: studioRevision([]byte(audioTakeScore))})
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	takes := s.takes.Takes()
	if len(takes) != 1 || takes[0].Stage != takejournal.Committed || takes[0].Frames != 8 {
		t.Fatalf("capture publication=%+v", takes)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.path), takes[0].Asset.Path))
	if err != nil {
		t.Fatal(err)
	}
	if math.Float32frombits(binary.LittleEndian.Uint32(data[44:])) != 0.75 {
		t.Fatal("capture writer lost PCM")
	}
}

func TestTakeShutdownFinalizesShortBatch(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	id, err := s.takes.Begin("vox", "main", studioRevision([]byte(audioTakeScore)), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	r, err := capture.NewRecorder(4, 4, 1, s.takes.Writer(id))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := capture.NewCountIn(48000, 120000, 0)
	if err = r.Begin(c, capture.Calibration{}); err != nil {
		t.Fatal(err)
	}
	s.captureID, s.captureRecorder = id, r
	r.Capture(capture.Block{DeviceEpoch: 1, EngineEpoch: 1, SampleRate: 48000, Frames: 4, Layout: capture.LayoutMono}, [][]float32{{0.25, 0.5, 1, 2}})
	if err = s.shutdown(); err != nil {
		t.Fatal(err)
	}
	reopened, err := newStudio(s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.transport.close()
	defer reopened.takes.Close()
	take, err := reopened.takes.Get(id)
	if err != nil || take.Frames != 4 || take.Stage != takejournal.Committed {
		t.Fatalf("shutdown take=%+v %v", take, err)
	}
}
