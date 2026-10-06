package main

import (
	"fmt"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

func (t *studioTransport) beginPreview() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.previewGeneration++
	return t.previewGeneration
}

func (t *studioTransport) startPreview(path string, p *project.Project) error {
	return t.startPreviewRequest(path, p, t.beginPreview())
}

// Preview swaps a prepared Player onto the existing native audio endpoint.
// All preparation and timer work happens outside the render callback.
func (t *studioTransport) startPreviewRequest(path string, p *project.Project, generation uint64) error {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	rate, err := t.ensureSampleRate()
	if err != nil {
		return err
	}
	score, err := compileLiveProjectAtRate(path, p, rate)
	if err != nil {
		return err
	}
	preview, err := liveplay.New(score, rate)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if generation != t.previewGeneration {
		preview.Close()
		return fmt.Errorf("preview was canceled")
	}
	if t.playing || t.browserPlaying || t.audio != nil && t.audio.Armed() {
		preview.Close()
		return fmt.Errorf("stop playback and recording before previewing")
	}
	t.clearPreviewLocked()
	if t.audio == nil {
		t.audio, err = openStudioAudio(preview, t.audioOptions, t.selectedAudioBackend(), rate)
		if err != nil {
			preview.Close()
			return err
		}
	} else {
		t.audio.SetSource(preview)
	}
	if err := t.audio.Play(); err != nil {
		t.audio.Pause()
		_ = t.audio.Close()
		t.audio = nil
		preview.Close()
		return err
	}
	t.preview = preview
	t.previewTimer = time.AfterFunc(2*time.Second, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.preview == preview {
			t.clearPreviewLocked()
		}
	})
	return nil
}

func (t *studioTransport) stopPreview() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopPreviewLocked()
}

func (t *studioTransport) stopPreviewLocked() {
	t.previewGeneration++
	t.clearPreviewLocked()
}

func (t *studioTransport) clearPreviewLocked() {
	if t.preview == nil {
		return
	}
	if t.previewTimer != nil {
		t.previewTimer.Stop()
		t.previewTimer = nil
	}
	if t.audio != nil {
		t.audio.Pause()
		if t.audio.StopClosesDevice() || t.stream == nil {
			_ = t.audio.Close()
			t.audio = nil
		} else {
			t.audio.SetSource(t.stream)
		}
	}
	t.preview.Close()
	t.preview = nil
}
