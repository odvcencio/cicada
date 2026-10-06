package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type studioLiveLease struct {
	Sequence uint64
	Closed   bool
	Seen     time.Time
	Notes    map[string]audioClientMessage
}
type studioLiveControls struct {
	mu     sync.Mutex
	leases map[string]*studioLiveLease
}

func (c *studioLiveControls) heldByAnother(owner, key string) bool {
	for id, lease := range c.leases {
		if id != owner && !lease.Closed {
			if _, held := lease.Notes[key]; held {
				return true
			}
		}
	}
	return false
}

type studioLiveRequest struct {
	Owner    string               `json:"owner"`
	Sequence uint64               `json:"sequence"`
	Release  bool                 `json:"release"`
	Messages []audioClientMessage `json:"messages"`
}

// GoSX owns browser sessions and CSRF. This private command endpoint shares
// the validated live-control path used by the portable audio host.
func (s *studio) liveControl(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var request studioLiveRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || len(request.Owner) < 16 || len(request.Owner) > 128 || request.Sequence == 0 || (!request.Release && len(request.Messages) == 0) || len(request.Messages) > 64 {
		studioJSON(w, 400, map[string]any{"error": "live control needs 1 to 64 messages"})
		return
	}
	s.liveControls.mu.Lock()
	defer s.liveControls.mu.Unlock()
	if s.liveControls.leases == nil {
		s.liveControls.leases = map[string]*studioLiveLease{}
	}
	for owner, lease := range s.liveControls.leases {
		if lease.Closed && time.Since(lease.Seen) > 30*time.Minute {
			delete(s.liveControls.leases, owner)
		}
	}
	lease := s.liveControls.leases[request.Owner]
	if lease == nil {
		active := 0
		for _, retained := range s.liveControls.leases {
			if !retained.Closed {
				active++
			}
		}
		// Closed tombstones reject delayed fetches but do not consume the
		// simultaneous input budget as the user navigates between panels.
		if active >= 128 || len(s.liveControls.leases) >= 4096 {
			studioJSON(w, 429, map[string]any{"error": "live input lease limit reached"})
			return
		}
		lease = &studioLiveLease{Notes: map[string]audioClientMessage{}}
		s.liveControls.leases[request.Owner] = lease
	}
	if lease.Closed || request.Sequence <= lease.Sequence {
		studioJSON(w, 409, map[string]any{"error": "live input mount has ended or command is stale"})
		return
	}
	lease.Sequence = request.Sequence
	lease.Seen = time.Now()
	if request.Release {
		lease.Closed = true
		for key, note := range lease.Notes {
			if s.liveControls.heldByAnother(request.Owner, key) {
				continue
			}
			off, velocity := false, 0
			note.On, note.Velocity = &off, &velocity
			_ = s.applyAudioMessage(note)
		}
		lease.Notes = nil
		studioJSON(w, 200, map[string]any{"ok": true})
		return
	}
	for _, message := range request.Messages {
		key := ""
		if message.Type == "note" {
			if err := validateAudioNote(message); err != nil {
				studioJSON(w, 422, map[string]any{"error": err.Error()})
				return
			}
			key = fmt.Sprintf("%s:%d", message.Track, *message.Note)
			if !*message.On {
				if _, owned := lease.Notes[key]; !owned {
					continue
				}
				if s.liveControls.heldByAnother(request.Owner, key) {
					delete(lease.Notes, key)
					continue
				}
			}
			if *message.On && len(lease.Notes) >= 64 {
				if _, ok := lease.Notes[key]; !ok {
					studioJSON(w, 429, map[string]any{"error": "release a note before playing another"})
					return
				}
			}
			if *message.On {
				_, held := lease.Notes[key]
				if held || s.liveControls.heldByAnother(request.Owner, key) {
					// One sounding voice represents all leases holding this pitch.
					// Only the final owner sends its matching note-off.
					lease.Notes[key] = message
					continue
				}
			}
		}
		if err := s.applyAudioMessage(message); err != nil {
			studioJSON(w, 422, map[string]any{"error": err.Error()})
			return
		}
		if key != "" {
			if *message.On {
				lease.Notes[key] = message
			} else {
				delete(lease.Notes, key)
			}
		}
	}
	studioJSON(w, 200, map[string]any{"ok": true})
}
