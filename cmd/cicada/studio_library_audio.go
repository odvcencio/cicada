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
	t.soundPreviewOwner = ""
	t.pendingSoundLease = ""
	return t.previewGeneration
}

func (t *studioTransport) startPreview(path string, p *project.Project) error {
	return t.startPreviewRequest(path, p, t.beginPreview())
}

// Preview swaps a prepared Player onto the existing native audio endpoint.
// All preparation and timer work happens outside the render callback.
func (t *studioTransport) startPreviewRequest(path string, p *project.Project, generation uint64) error {
	return t.startPreviewRequestFor(path, p, generation, 2*time.Second, false)
}

func (t *studioTransport) startPreviewRequestFor(path string, p *project.Project, generation uint64, duration time.Duration, outputOnly bool) error {
	t.pollMu.Lock()
	defer t.pollMu.Unlock()
	t.mu.Lock()
	options, backend := t.audioOptions, t.selectedAudioBackend()
	t.mu.Unlock()
	var rate int
	var err error
	if outputOnly {
		options.InputEnabled, options.MonitorMuted = false, true
		rate, err = studioAudioSampleRate(backend, options)
	} else {
		rate, err = t.ensureSampleRate()
	}
	if err != nil {
		return err
	}
	score, err := compileLiveProjectLoopAtRate(path, p, rate, !outputOnly)
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
	if outputOnly && t.audio != nil {
		// Browsing sounds must not open or monitor the configured microphone.
		// Playback reopens its normal routing when the musician resumes the song.
		t.audio.Pause()
		_ = t.audio.Close()
		t.audio = nil
	}
	if t.audio == nil {
		t.audio, err = openStudioAudio(preview, options, backend, rate)
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
	t.previewOutputOnly = outputOnly
	if outputOnly {
		t.soundPreviewOwner = t.pendingSoundLease
		t.pendingSoundLease = ""
	}
	t.previewTimer = time.AfterFunc(duration, func() {
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
	t.soundPreviewOwner = ""
	t.pendingSoundLease = ""
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
		if t.previewOutputOnly || t.audio.StopClosesDevice() || t.stream == nil {
			_ = t.audio.Close()
			t.audio = nil
		} else {
			t.audio.SetSource(t.stream)
		}
	}
	t.preview.Close()
	t.preview = nil
	t.previewOutputOnly = false
	t.soundPreviewOwner = ""
}
