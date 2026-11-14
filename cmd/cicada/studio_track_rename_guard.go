package main

import (
	"errors"
	"strings"

	"m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
)

var errTrackRenameWhilePlaying = errors.New("Stop playback and finish recording before renaming a track.")

// Caller holds s.mu, which serializes native transport starts and take starts
// with the commit. Stop discards the stream, preventing another live note from
// being queued against the name while the rename is committed.
func (s *studio) trackRenameGuard(env edit.Envelope) error {
	if env.DryRun {
		return nil
	}
	for _, intent := range env.Intents {
		rename, ok := intent.(*edit.Rename)
		if !ok || !strings.HasPrefix(string(rename.Entity), "track:") {
			continue
		}
		if s.trackIdentityBusy() {
			return errTrackRenameWhilePlaying
		}
	}
	return nil
}

// trackIdentityBusy is called under s.mu, which serializes native stream and
// capture starts with score commits.
func (s *studio) trackIdentityBusy() bool {
	s.transport.mu.Lock()
	busy := s.transport.stream != nil || s.transport.browserPlaying || s.transport.audio != nil && s.transport.audio.Armed()
	s.transport.mu.Unlock()
	return busy || s.captureID != "" || s.captureInstrument
}

func sameTrackIdentities(a, b *project.Project) bool {
	if a == nil || b == nil || len(a.Tracks) != len(b.Tracks) {
		return false
	}
	for i := range a.Tracks {
		if a.Tracks[i].ID != b.Tracks[i].ID {
			return false
		}
	}
	return true
}
