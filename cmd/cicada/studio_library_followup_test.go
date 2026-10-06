package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

type restartablePreviewStream struct{ *simulatedCaptureStream }

func (*restartablePreviewStream) StopClosesDevice() bool { return false }

func libraryReviewPlayer(t *testing.T, filename string) *liveplay.Player {
	t.Helper()
	p, err := compileStudioSource(filename, []byte(studioLibraryScore))
	if err != nil {
		t.Fatal(err)
	}
	score, err := compileLiveProjectAtRate(filename, p, 48000)
	if err != nil {
		t.Fatal(err)
	}
	player, err := liveplay.New(score, 48000)
	if err != nil {
		t.Fatal(err)
	}
	return player
}

func TestStudioPreviewUntimedCallbackPreservesPausedScore(t *testing.T) {
	_, filename := libraryStudio(t)
	player := libraryReviewPlayer(t, filename)
	audio, device := simulatedCaptureAudio(128, player)
	audio.stream = &restartablePreviewStream{device}
	tx := &studioTransport{path: filename, sampleRate: 48000, stream: player, audio: audio, playing: true}
	defer tx.close()
	if err := audio.Play(); err != nil {
		t.Fatal(err)
	}
	output := [][]float32{make([]float32, 128), make([]float32, 128)}
	for range 100 {
		if err := audio.renderPeriod(nil, output); err != nil {
			t.Fatal(err)
		}
	}
	tx.pause()
	paused := player.Position()
	p, _, err := studioLibraryPreviewProject(filename, studioLibraryItem{Path: "std/synth", Name: "glassbass", Kind: "instrument"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.startPreview(filename, p); err != nil {
		t.Fatal(err)
	}
	preview := tx.preview
	for range 200 {
		if err := audio.renderPeriod(nil, output); err != nil {
			t.Fatal(err)
		}
	}
	if got := player.Position(); got != paused {
		t.Fatalf("preview advanced paused score: %+v -> %+v", paused, got)
	}
	if preview.Position().Step <= 1 {
		t.Fatal("preview source did not advance")
	}
	tx.stopPreview()
	if err := audio.Play(); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if err := audio.renderPeriod(nil, output); err != nil {
			t.Fatal(err)
		}
	}
	if player.Position() == paused {
		t.Fatal("score source was not restored")
	}
}

func TestStudioPreviewClosesStoppedOneStartDevice(t *testing.T) {
	_, filename := libraryStudio(t)
	player, preview := libraryReviewPlayer(t, filename), libraryReviewPlayer(t, filename)
	audio, device := simulatedCaptureAudio(128, preview)
	if err := audio.Play(); err != nil {
		t.Fatal(err)
	}
	tx := newStudioTransport(filename)
	tx.audioNull, tx.sampleRate = true, 48000
	tx.stream, tx.preview, tx.audio = player, preview, audio
	defer tx.close()
	tx.stopPreview()
	if tx.audio != nil || device.closes != 1 {
		t.Fatalf("stopped device retained: audio=%v closes=%d", tx.audio != nil, device.closes)
	}
	p, _, err := studioLibraryPreviewProject(filename, studioLibraryItem{Path: "std/synth", Name: "glassbass", Kind: "instrument"})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.startPreview(filename, p); err != nil {
		t.Fatalf("second preview could not reopen audio: %v", err)
	}
	if tx.audio == audio {
		t.Fatal("second preview reused stopped device")
	}
	tx.stopPreview()
	if err := tx.start(); err != nil {
		t.Fatalf("play after preview: %v", err)
	}
}

func TestStudioCaptureArmingCancelsPreviewBeforePreparingDevice(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(fmt.Sprint(prepare), func(t *testing.T) {
			_, filename := libraryStudio(t)
			preview := libraryReviewPlayer(t, filename)
			oldAudio, oldDevice := simulatedCaptureAudio(128, preview)
			if err := oldAudio.Play(); err != nil {
				t.Fatal(err)
			}
			newAudio, newDevice := simulatedCaptureAudio(128, nil)
			tx := &studioTransport{path: filename, sampleRate: 48000, preview: preview, audio: oldAudio, audioOptions: studioAudioOptions{InputEnabled: true, MonitorMode: "stereo", MonitorMuted: true}}
			tx.takeInputOpener = func(io.Reader, studioAudioOptions, string, int) (studioAudioDevice, error) { return newAudio, nil }
			defer tx.close()
			if prepare {
				if _, _, err := tx.prepareTakeInput(); err != nil {
					t.Fatal(err)
				}
				if tx.preview != nil || tx.audio != newAudio {
					t.Fatal("capture preparation retained preview")
				}
			}
			recorder, err := capture.NewRecorder(4, 128, 2, func(capture.RecordedBlock, [][]float32) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.armCapture(recorder); err != nil {
				t.Fatal(err)
			}
			if tx.preview != nil || oldDevice.closes != 1 || tx.audio != newAudio {
				t.Fatal("arming did not replace preview device")
			}
			tx.stopPreview()
			if !newAudio.Armed() || newDevice.closes != 0 {
				t.Fatal("preview cancellation destroyed armed capture")
			}
			if err := tx.startCapture(capture.Calibration{}); err != nil {
				t.Fatalf("record after preview: %v", err)
			}
		})
	}
}

func TestStudioLibraryPreviewsDrumLanePresets(t *testing.T) {
	_, filename := libraryStudio(t)
	root := filepath.Join(os.Getenv("CICADA_LIBRARY"), "lanes")
	if err := project.NewLibrary("lanes", root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.cicada"), []byte("preset kick { instrument=builtin.bd decay=250ms }\npreset hat { instrument=builtin.ch decay=50ms }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, lane string }{{"kick", "bd"}, {"hat", "ch"}} {
		p, source, err := studioLibraryPreviewProject(filename, studioLibraryItem{Path: "lanes", Name: test.name, Kind: "preset"})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(source, []byte("pattern phrase drums { "+test.lane+":")) {
			t.Fatalf("wrong lane phrase: %s", source)
		}
		if _, err := project.CompileEngine(p, 48000, 128); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStudioLibrarySavesPublicPresetWithPrivateInstrument(t *testing.T) {
	_, filename := libraryStudio(t)
	root := filepath.Join(os.Getenv("CICADA_LIBRARY"), "colors")
	if err := project.NewLibrary("colors", root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.cicada"), []byte("instrument _tone { param gain=0.5 voice mono { let wave=sine(pitch) out=wave*gain } }\npreset soft { instrument=_tone gain=0.3 level=-9dB }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := []byte("cicada 2\nimport \"colors\"\ninstrument saved-voice { voice mono { out=sine(pitch) } }\ntrack lead colors.soft { gain=0.2 }\npattern melody { c3 . g3 . }\nscene main { lead=melody }\nsong { main }\n")
	_, pins, err := studioLibraryPins(filename, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pins.Path, pins.After, 0600); err != nil {
		t.Fatal(err)
	}
	updated, err := studioSavedPresetSource(filename, source, "lead", "saved")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("instrument = saved-voice-2")) || !bytes.Contains(updated, []byte("let wave=sine(pitch)")) || bytes.Contains(updated, []byte("instrument = colors._tone")) {
		t.Fatalf("illegal or incomplete snapshot: %s", updated)
	}
	bound := []byte(strings.Replace(string(updated), "track lead colors.soft", "track lead saved", 1))
	if _, err := compileStudioSource(filename, bound); err != nil {
		t.Fatal(err)
	}
	studioLibraryPCMEqual(t, filename, source, bound)
}

func TestStudioLibraryInsertHonorsVendorLock(t *testing.T) {
	h, filename := libraryStudio(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(filename), ".cicada-vendor.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	r := studioCall(t, h, "/api/library/insert", studioEdit{Revision: studioRevision([]byte(studioLibraryScore)), Path: "std/synth", Item: "glassbass", Track: "lead"})
	if r.Code != 409 || !strings.Contains(r.Body.String(), "cannot lock library pins") {
		t.Fatalf("insert bypassed pin lock: %d %s", r.Code, r.Body.String())
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != studioLibraryScore {
		t.Fatal("locked insert changed score")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filename), "cicada.sum")); !os.IsNotExist(err) {
		t.Fatal("locked insert published pins")
	}
}
