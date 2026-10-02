package main

import (
	"fmt"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/project"
)

// Preview swaps a prepared Player onto the existing native audio endpoint.
// All preparation and timer work happens outside the render callback.
func (t *studioTransport) startPreview(path string, p *project.Project) error {
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
	if t.playing || t.browserPlaying || t.audio != nil && t.audio.Armed() {
		preview.Close()
		return fmt.Errorf("stop playback and recording before previewing")
	}
	t.stopPreviewLocked()
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
			t.stopPreviewLocked()
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
	if t.preview == nil {
		return
	}
	if t.previewTimer != nil {
		t.previewTimer.Stop()
		t.previewTimer = nil
	}
	if t.audio != nil {
		t.audio.Pause()
		if t.stream != nil {
			t.audio.SetSource(t.stream)
		} else {
			_ = t.audio.Close()
			t.audio = nil
		}
	}
	t.preview.Close()
	t.preview = nil
}
