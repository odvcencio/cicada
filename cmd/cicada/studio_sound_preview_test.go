package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/cmd"
)

func TestSoundPreviewFactoryPhrasesSoundAndRelease(t *testing.T) {
	for _, patch := range instrument.Patches() {
		t.Run(patch.ID, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preview.cicada")
			if err := os.WriteFile(path, []byte("cicada 2\n"), 0600); err != nil {
				t.Fatal(err)
			}
			p, source, duration, err := soundPreviewProject(path, soundPreviewRequest{Patch: patch.ID, Tempo: 130000, Root: 9, Scale: "minor"})
			if err != nil {
				t.Fatal(err)
			}
			if p.TempoMilli != 130000 || p.Key.Root != 9 || p.Key.Scale != "minor" || duration < 4800*time.Millisecond {
				t.Fatalf("audition lost musical context: %+v, %s", p.Key, duration)
			}
			score, err := compileLiveProjectLoopAtRate(path, p, 48000, false)
			if err != nil {
				t.Fatal(err)
			}
			e := score.Engine
			defer e.Close()
			if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
				t.Fatal("play refused")
			}
			var left, right [256]float32
			frames := int(math.Ceil(duration.Seconds() * 48000))
			peak, tail, energy := 0.0, 0.0, 0.0
			for frame := 0; frame < frames; {
				n := min(len(left), frames-frame)
				e.Render(left[:n], right[:n])
				for i := range n {
					for _, value := range []float32{left[i], right[i]} {
						v := math.Abs(float64(value))
						if math.IsNaN(v) || math.IsInf(v, 0) {
							t.Fatal("non-finite preview audio")
						}
						peak = max(peak, v)
						energy += v * v
						if frame+i >= frames-480 {
							tail = max(tail, v)
						}
					}
				}
				frame += n
			}
			if peak < .01 || peak > 1 || tail > .0001 {
				t.Fatalf("preview peak=%g tail=%g", peak, tail)
			}
			t.Logf("sound=%s peak=%.6f rms=%.6f final_10ms_peak=%.9f", patch.ID, peak, math.Sqrt(energy/float64(frames*2)), tail)
			if dir := os.Getenv("CICADA_SOUND_PROOF_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, patch.ID+".cicada"), source, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func soundTestLease(t *testing.T, s *studio) string {
	t.Helper()
	r := studioCall(t, s.routes(), "/api/sound-preview", soundPreviewRequest{Action: "open"})
	var response soundPreviewResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || response.Lease == "" {
		t.Fatalf("open: %d %s", r.Code, r.Body)
	}
	return response.Lease
}

func TestSoundPreviewLeavesSongAndOtherListenersIntact(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	tr := s.transport
	tr.audioNull = true
	t.Cleanup(tr.stop)
	if err := tr.start(); err != nil {
		t.Fatal(err)
	}
	tr.pause()
	stream, position, options := tr.stream, tr.stream.Position(), tr.audioOptions
	before, _ := os.ReadFile(s.path)
	a, b := soundTestLease(t, s), soundTestLease(t, s)
	play := func(lease, patch string, sequence uint64) {
		t.Helper()
		r := studioCall(t, s.routes(), "/api/sound-preview", soundPreviewRequest{Action: "play", Lease: lease, Sequence: sequence, Patch: patch, Tempo: 130000, Root: 9, Scale: "minor"})
		if r.Code != 200 {
			t.Fatalf("play: %d %s", r.Code, r.Body)
		}
	}
	play(a, "warm-pad", 1)
	if !tr.soundPreviewState(a).Active || tr.playing {
		t.Fatal("preview did not stay separate from song playback")
	}
	// Closing the old surface must not cancel another surface's pending build.
	prepared := soundPreviewRequest{Lease: b, Sequence: 1, Patch: "soft-bell", Tempo: 130000, Root: 9, Scale: "minor"}
	generation, err := tr.beginSoundPreview(prepared)
	if err != nil {
		t.Fatal(err)
	}
	tr.stopSoundPreview(soundPreviewRequest{Action: "close", Lease: a, Sequence: 2})
	p, _, duration, err := soundPreviewProject(s.path, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.startPreviewRequestFor(s.path, p, generation, duration, true); err != nil {
		t.Fatalf("old close canceled new preparation: %v", err)
	}
	tr.stopSoundPreview(soundPreviewRequest{Action: "close", Lease: a, Sequence: 100})
	if !tr.soundPreviewState(b).Active {
		t.Fatal("an old browser closed another listener's preview")
	}
	r := studioCall(t, s.routes(), "/api/sound-preview", soundPreviewRequest{Action: "play", Lease: a, Sequence: 101, Patch: "round-bass", Tempo: 130000, Root: 9, Scale: "minor"})
	if r.Code != 409 || !tr.soundPreviewState(b).Active {
		t.Fatal("closed lease revived or interrupted current sound")
	}
	r = studioCall(t, s.routes(), "/api/sound-preview", soundPreviewRequest{Action: "play", Lease: b, Sequence: 2, Patch: "unknown", Tempo: 130000, Root: 9, Scale: "minor"})
	if r.Code != 422 || !tr.soundPreviewState(b).Active {
		t.Fatal("invalid patch interrupted a valid preview")
	}
	play(b, "wide-strings", 3)
	tr.stopSoundPreview(soundPreviewRequest{Action: "stop", Lease: b, Sequence: 2})
	if !tr.soundPreviewState(b).Active {
		t.Fatal("late Stop canceled a newer sound")
	}
	tr.stopSoundPreview(soundPreviewRequest{Action: "stop", Lease: b, Sequence: 4})
	if tr.preview != nil || tr.audio != nil || tr.playing {
		t.Fatal("Stop did not release the preview output")
	}
	after, _ := os.ReadFile(s.path)
	if !bytes.Equal(before, after) || tr.stream != stream || !reflect.DeepEqual(position, stream.Position()) || tr.audioOptions != options {
		t.Fatal("listening changed the song, its position, or saved audio choices")
	}
	play(b, "poly-brass", 5)
	if err := tr.start(); err != nil {
		t.Fatal(err)
	}
	if tr.preview != nil || !tr.playing {
		t.Fatal("song playback did not take back its output")
	}
}

func TestSoundPreviewCanceledPreparationCannotStartLate(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	tr := s.transport
	tr.audioNull = true
	t.Cleanup(tr.stop)
	lease := soundTestLease(t, s)
	request := soundPreviewRequest{Lease: lease, Sequence: 1, Patch: "warm-pad", Tempo: 120000, Root: 0, Scale: "major"}
	p, _, duration, err := soundPreviewProject(s.path, request)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := tr.beginSoundPreview(request)
	if err != nil {
		t.Fatal(err)
	}
	tr.stopSoundPreview(soundPreviewRequest{Action: "close", Lease: lease, Sequence: 2})
	if err := tr.startPreviewRequestFor(s.path, p, generation, duration, true); err == nil || tr.preview != nil {
		t.Fatal("canceled preparation started audio")
	}
	other := soundTestLease(t, s)
	request.Lease, request.Sequence = other, 1
	generation, err = tr.beginSoundPreview(request)
	if err != nil {
		t.Fatal(err)
	}
	tr.stopPreview()
	if err := tr.startPreviewRequestFor(s.path, p, generation, duration, true); err == nil {
		t.Fatal("global Stop did not cancel pending preparation")
	}
	tr.stopSoundPreview(soundPreviewRequest{Action: "close", Lease: other, Sequence: 2})
	for i := 0; i < 32; i++ {
		if _, err := tr.openSoundPreview(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tr.openSoundPreview(); err == nil {
		t.Fatal("sound lease storage grew past its bound")
	}
}
