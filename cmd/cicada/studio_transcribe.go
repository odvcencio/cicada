package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/transcription"
)

//go:embed studio-transcribe.js
var studioTranscribeJS []byte

const studioTranscribeMaxBytes = 32 << 20

func (s *studio) transcribeScript(w http.ResponseWriter, r *http.Request) {
	serveCaptureScript(w, studioTranscribeJS)
}

func transcriptionLocalRequest(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if !studioLoopbackHost(r.Host) || err != nil || ip == nil || !ip.IsLoopback() {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "transcription requires a local connection"})
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin transcription requests are not allowed"})
			return false
		}
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		studioJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin transcription requests are not allowed"})
		return false
	}
	return true
}

// Transcription is an offline host operation. Temporary uploads are removed
// even when multipart parsing fails; analysis never writes the current score.
func (s *studio) transcribe(w http.ResponseWriter, r *http.Request) {
	if !transcriptionLocalRequest(w, r) {
		return
	}
	s.mu.Lock()
	if s.transcribing {
		s.mu.Unlock()
		studioJSON(w, http.StatusTooManyRequests, map[string]string{"error": "a transcription is already running; try again when it finishes"})
		return
	}
	s.transcribing = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.transcribing = false; s.mu.Unlock() }()
	r.Body = http.MaxBytesReader(w, r.Body, studioTranscribeMaxBytes+(1<<20))
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	options := transcription.DefaultOptions()
	var data []byte
	var err error
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch mediaType {
	case "multipart/form-data":
		if err = r.ParseMultipartForm(1 << 20); err != nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose one WAV file of at most 32 MiB"})
			return
		}
		if value := r.FormValue("tempo"); value != "" {
			options.Tempo, err = strconv.ParseFloat(value, 64)
			if err != nil {
				studioJSON(w, http.StatusBadRequest, map[string]string{"error": "tempo must be a number or left blank for automatic detection"})
				return
			}
		}
		if value := r.FormValue("grid"); value != "" {
			options.Grid, err = strconv.Atoi(value)
			if err != nil {
				studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose a quarter, eighth or sixteenth note grid"})
				return
			}
		}
		options.Key = r.FormValue("key")
		files := r.MultipartForm.File["wav"]
		if len(files) != 1 || files[0].Size > studioTranscribeMaxBytes {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "choose one WAV file of at most 32 MiB"})
			return
		}
		file, openErr := files[0].Open()
		if openErr != nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot open recording"})
			return
		}
		data, err = io.ReadAll(io.LimitReader(file, studioTranscribeMaxBytes+1))
		_ = file.Close()
	case "application/json":
		var request struct {
			TakeID  string                `json:"takeId"`
			Options transcription.Options `json:"options"`
		}
		request.Options = options
		decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid take transcription request"})
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			studioJSON(w, http.StatusBadRequest, map[string]string{"error": "expected one take transcription request"})
			return
		}
		options = request.Options
		data, err = s.transcriptionTakeWAV(request.TakeID)
	default:
		studioJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "upload a WAV file or choose a completed take"})
		return
	}
	if err != nil || len(data) > studioTranscribeMaxBytes {
		message := "cannot read recording"
		if err != nil {
			message = err.Error()
		}
		studioJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		return
	}
	if r.Context().Err() != nil {
		return
	}
	result, err := transcription.DecodeWAV(data, options)
	if err == nil && r.Context().Err() == nil {
		result, err = transcription.Complete(result, options)
	}
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		studioJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	studioJSON(w, http.StatusOK, result)
}

func (s *studio) transcriptionTakeWAV(id string) ([]byte, error) {
	if id == "" || s.takes == nil {
		return nil, fmt.Errorf("choose a completed recording")
	}
	take, err := s.takes.Get(id)
	if err != nil {
		return nil, fmt.Errorf("recording is unavailable")
	}
	if take.Incomplete || take.Asset.Path == "" || take.Rate < 8000 || take.Frames > uint64(take.Rate)*60 {
		return nil, fmt.Errorf("choose a complete recording of at most 60 seconds")
	}
	region, err := sampleasset.LoadRegion(takeRoot(s.path), take.Asset, take.StartFrame(), 0, 60, false)
	if err != nil {
		return nil, fmt.Errorf("cannot load recording")
	}
	mono := append([]float32(nil), region.Left[region.Start:region.End]...)
	if len(region.Right) > 0 {
		for i := range mono {
			mono[i] = (mono[i] + region.Right[region.Start+i]) * 0.5
		}
	}
	return recording.EncodeWAV(mono, region.SampleRate), nil
}
