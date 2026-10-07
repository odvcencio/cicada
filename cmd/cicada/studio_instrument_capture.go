package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/recording"
)

// Native microphone instruments use the same durable journal and Tymbal duplex
// clock as audio takes, with silent monitoring and no pre-existing audio track.
func (s *studio) instrumentCapture(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequest(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func(err error) { studioJSON(w, 422, map[string]any{"error": err.Error()}) }
	switch edit.Action {
	case "start":
		if s.captureID != "" {
			fail(fmt.Errorf("finish the active recording first"))
			return
		}
		if s.transport.snapshot().Playing {
			fail(fmt.Errorf("pause playback before recording an instrument"))
			return
		}
		if s.recordedRevision() != edit.Revision {
			studioJSON(w, 409, map[string]any{"error": "score changed; reload before recording"})
			return
		}
		if err := s.openTakes(); err != nil {
			fail(err)
			return
		}
		rate, channels, err := s.transport.prepareTakeInput()
		if err != nil {
			fail(err)
			return
		}
		id, err := s.takes.Begin("recorded", "main", edit.Revision, rate, channels)
		if err != nil {
			fail(err)
			return
		}
		writer := s.takes.Writer(id)
		recorder, err := capture.NewRecorder(64, 8192, channels, func(block capture.RecordedBlock, pcm [][]float32) error {
			if block.RawFrame+uint64(block.Timing.Frames) > recording.MaxFrames {
				return fmt.Errorf("instrument recording reached the frame limit")
			}
			return writer(block, pcm)
		})
		if err != nil {
			fail(err)
			return
		}
		if err = s.transport.armCapture(recorder); err != nil {
			recorder.Close()
			fail(err)
			return
		}
		if err = recorder.Begin(capture.CountIn{}, capture.Calibration{}); err != nil {
			s.transport.disarmCapture()
			fail(err)
			return
		}
		s.transport.mu.Lock()
		s.transport.audio.SetSource(studioSilentSource{})
		s.transport.audio.SetMonitor(studioAudioMonitor{Muted: true, Mode: "stereo"})
		err = s.transport.audio.Play()
		s.transport.mu.Unlock()
		if err != nil {
			s.transport.disarmCapture()
			fail(err)
			return
		}
		s.captureID, s.captureRecorder, s.captureInstrument = id, recorder, true
		studioJSON(w, 200, map[string]any{"recording": true})
	case "stop":
		if s.captureID == "" || !s.captureInstrument {
			fail(fmt.Errorf("record an instrument first"))
			return
		}
		s.transport.mu.Lock()
		s.transport.audio.Pause()
		if s.transport.stream != nil {
			s.transport.audio.SetSource(s.transport.stream)
		}
		s.transport.mu.Unlock()
		drain := s.transport.disarmCapture()
		snapshot := s.captureRecorder.Snapshot()
		id := s.captureID
		s.captureID, s.captureRecorder, s.captureInstrument = "", nil, false
		if err := s.finalizeCaptureTake(id, snapshot, drain); err != nil {
			fail(err)
			return
		}
		take, err := s.takes.Get(id)
		if err != nil {
			fail(err)
			return
		}
		if take.Incomplete {
			fail(fmt.Errorf("recording has missing audio; the raw take was retained"))
			return
		}
		if err = s.takes.Publish(id); err != nil {
			fail(err)
			return
		}
		base, err := studioProjectRoot(s.path)
		if err != nil {
			fail(err)
			return
		}
		f, err := os.Open(filepath.Join(base, take.Asset.Path))
		if err != nil {
			fail(err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, recording.MaxInputBytes+1))
		f.Close()
		if err != nil {
			fail(err)
			return
		}
		audio, err := recording.DecodeWAV(data)
		if err != nil {
			fail(err)
			return
		}
		options := recording.DefaultOptions()
		hits, err := recording.Analyze([]recording.Audio{audio}, options)
		if err != nil {
			fail(err)
			return
		}
		name := edit.NewName
		if name == "" {
			name = "recorded"
		}
		pack, err := recording.Build(name, hits, options.Layers)
		if err != nil {
			fail(err)
			return
		}
		if s.recordedInstruments == nil {
			s.recordedInstruments = map[string]*recording.Pack{}
		}
		s.recordedInstruments[pack.Pin] = pack
		s.recordingPreview = pack.Pin
		studioJSON(w, 200, map[string]any{"sha256": pack.Pin, "hits": hits, "kept": len(hits)})
	default:
		fail(fmt.Errorf("choose start or stop"))
	}
}

func (s *studio) instrumentState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pack := s.recordedInstruments[s.recordingPreview]
	sampled, modeled := "", ""
	source, err := os.ReadFile(s.path)
	if err == nil {
		if p, err := compileStudioSource(s.path, source); err == nil {
			for _, sampler := range p.Samplers {
				if sampler.SHA256 == s.recordedSampled {
					sampled = s.recordedSampled
				}
				if sampler.SHA256 == s.recordedModeled {
					modeled = s.recordedModeled
				}
			}
		}
	}
	studioJSON(w, 200, map[string]any{"preview": pack, "recording": s.captureInstrument, "sampled": sampled, "modeled": modeled})
}
func (s *studio) instrumentHit(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pack := s.recordedInstruments[r.URL.Query().Get("sha256")]
	var index int
	if _, err := fmt.Sscanf(r.URL.Query().Get("hit"), "%d", &index); err != nil || pack == nil || index < 1 || index > len(pack.Hits) {
		http.NotFound(w, r)
		return
	}
	hit := pack.Hits[index-1]
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(recording.EncodeWAV(hit.PCM, hit.Rate))
}

type studioSilentSource struct{}

func (studioSilentSource) Read(data []byte) (int, error) { clear(data); return len(data), nil }
