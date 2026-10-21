package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/project"
)

const soundLeaseLifetime = 10 * time.Minute

type soundPreviewLease struct {
	id      string
	last    uint64
	expires time.Time
	closed  bool
}

type soundPreviewRequest struct {
	Action   string `json:"action"`
	Lease    string `json:"lease"`
	Sequence uint64 `json:"sequence"`
	Patch    string `json:"patch"`
	Tempo    int    `json:"tempo"`
	Root     int    `json:"root"`
	Scale    string `json:"scale"`
}

type soundPreviewResult struct {
	Lease      string `json:"lease,omitempty"`
	ExpiresMS  int64  `json:"expiresMs,omitempty"`
	DurationMS int64  `json:"durationMs,omitempty"`
	Active     bool   `json:"active"`
	Error      string `json:"error,omitempty"`
}

var soundScaleName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Use the same authored graph as AddPreset. The scratch score has no imports,
// effects or project assets; choosing a sound never edits the musician's score.
func soundPreviewProject(path string, request soundPreviewRequest) (*project.Project, []byte, time.Duration, error) {
	patch, ok := instrument.FindPatch(request.Patch)
	if !ok {
		return nil, nil, 0, fmt.Errorf("choose a sound from the instrument library")
	}
	if request.Tempo < 20000 || request.Tempo > 300000 || request.Root < 0 || request.Root > 11 || !soundScaleName.MatchString(request.Scale) {
		return nil, nil, 0, fmt.Errorf("preview needs a valid key and a tempo from 20 to 300 BPM")
	}
	declaration, err := patch.Source("preview_sound")
	if err != nil {
		return nil, nil, 0, err
	}
	roots := [...]string{"c", "c#", "d", "d#", "e", "f", "f#", "g", "g#", "a", "a#", "b"}
	phrase := "1 - - - 3 - - - 5 - - - 1 - - -"
	if patch.Mode == "poly" {
		phrase = "[1 3 5] - - - - - - - [5 7 3] - - - - - - -"
	}
	tempo := strconv.FormatFloat(float64(request.Tempo)/1000, 'f', 3, 64)
	source := []byte(fmt.Sprintf("cicada 2\ntempo %s\nkey %s %s\n%s\ntrack preview preview_sound {}\npattern phrase notes steps=16 { %s }\nscene audition { preview=phrase }\nsong { audition }\n", tempo, roots[request.Root], request.Scale, declaration, phrase))
	p, err := compileStudioSource(path, source)
	// All factory patches release within three seconds. Keep the entire phrase
	// and release, including the slowest supported tempo, on the chosen output.
	duration := time.Duration(240*int64(time.Second)*1000/int64(request.Tempo)) + 3*time.Second
	return p, source, duration, err
}

func (t *studioTransport) openSoundPreview() (soundPreviewResult, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return soundPreviewResult{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for i := range t.soundLeases {
		entry := &t.soundLeases[i]
		if entry.id == "" || entry.closed || !now.Before(entry.expires) {
			*entry = soundPreviewLease{id: hex.EncodeToString(bytes[:]), expires: now.Add(soundLeaseLifetime)}
			return soundPreviewResult{Lease: entry.id, ExpiresMS: soundLeaseLifetime.Milliseconds()}, nil
		}
	}
	return soundPreviewResult{}, fmt.Errorf("too many sound browser sessions; close another Studio window and try again")
}

func (t *studioTransport) soundLeaseLocked(id string) *soundPreviewLease {
	for i := range t.soundLeases {
		entry := &t.soundLeases[i]
		if id != "" && entry.id == id && !entry.closed && time.Now().Before(entry.expires) {
			return entry
		}
	}
	return nil
}

func (t *studioTransport) beginSoundPreview(request soundPreviewRequest) (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.soundLeaseLocked(request.Lease)
	if entry == nil {
		return 0, fmt.Errorf("sound preview expired; try Listen again")
	}
	if request.Sequence == 0 || request.Sequence <= entry.last {
		return 0, fmt.Errorf("sound preview was superseded")
	}
	entry.last = request.Sequence
	if t.playing || t.browserPlaying || t.audio != nil && t.audio.Armed() {
		return 0, fmt.Errorf("stop playback and recording before previewing a sound")
	}
	t.previewGeneration++
	t.pendingSoundLease = request.Lease
	return t.previewGeneration, nil
}

func (t *studioTransport) stopSoundPreview(request soundPreviewRequest) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.soundLeaseLocked(request.Lease)
	if entry == nil || request.Sequence == 0 {
		return
	}
	if request.Action != "close" && request.Sequence <= entry.last {
		return
	}
	entry.last = max(entry.last, request.Sequence)
	entry.closed = request.Action == "close"
	if t.pendingSoundLease == request.Lease {
		t.previewGeneration++
		t.pendingSoundLease = ""
	}
	if t.soundPreviewOwner == request.Lease {
		if t.pendingSoundLease != "" && t.pendingSoundLease != request.Lease {
			t.clearPreviewLocked()
		} else {
			t.stopPreviewLocked()
		}
	}
}

func (t *studioTransport) soundPreviewState(lease string) soundPreviewResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.preview == nil || t.soundPreviewOwner != lease || t.soundLeaseLocked(lease) == nil {
		return soundPreviewResult{}
	}
	if t.audio != nil {
		if err := t.audio.Err(); err != nil {
			t.stopPreviewLocked()
			return soundPreviewResult{Error: "Preview stopped: " + err.Error()}
		}
	}
	return soundPreviewResult{Active: true}
}

func (s *studio) soundPreview(w http.ResponseWriter, r *http.Request) {
	if !studioSameOrigin(r) {
		studioJSON(w, 403, map[string]string{"error": "cross-origin sound preview is not allowed"})
		return
	}
	var request soundPreviewRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		studioJSON(w, 400, map[string]string{"error": "invalid sound preview request"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		studioJSON(w, 400, map[string]string{"error": "expected one sound preview request"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch request.Action {
	case "open":
		result, err := s.transport.openSoundPreview()
		if err != nil {
			studioJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		studioJSON(w, 200, result)
	case "stop", "close":
		s.transport.stopSoundPreview(request)
		studioJSON(w, 200, soundPreviewResult{})
	case "state":
		studioJSON(w, 200, s.transport.soundPreviewState(request.Lease))
	case "play":
		generation, err := s.transport.beginSoundPreview(request)
		if err != nil {
			studioJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		defer func() {
			if err != nil {
				s.transport.mu.Lock()
				if s.transport.previewGeneration == generation && s.transport.pendingSoundLease == request.Lease {
					s.transport.pendingSoundLease = ""
				}
				s.transport.mu.Unlock()
			}
		}()
		s.mu.Lock()
		path := s.path
		s.mu.Unlock()
		var p *project.Project
		var duration time.Duration
		p, _, duration, err = soundPreviewProject(path, request)
		if err != nil {
			studioJSON(w, 422, map[string]string{"error": err.Error()})
			return
		}
		if err = r.Context().Err(); err == nil {
			err = s.transport.startPreviewRequestFor(path, p, generation, duration, true)
		}
		if err != nil {
			studioJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		studioJSON(w, 200, soundPreviewResult{Active: true, DurationMS: duration.Milliseconds()})
	default:
		studioJSON(w, 400, map[string]string{"error": "choose a sound preview action"})
	}
}
