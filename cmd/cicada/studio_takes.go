package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
)

var errTakeRevision = errors.New("score changed during recording; take retained; reload and recover it against the current revision")

func (s *studio) openTakes() error {
	if s.takes != nil {
		return nil
	}
	store, err := takejournal.Open(s.path)
	if err != nil {
		return err
	}
	s.takes = store
	return store.Recover()
}
func (s *studio) recoverTakesOnOpen() error {
	if _, err := os.Stat(filepath.Join(takeRoot(s.path), takejournal.Namespace(s.path))); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err := s.openTakes(); err != nil {
		return err
	}
	for _, t := range s.takes.Takes() {
		if t.Stage != takejournal.Prepared && t.Stage != takejournal.Source {
			continue
		}
		if err := s.recoverTakeReceipts(t); err != nil {
			return err
		}
		source, err := os.ReadFile(s.path)
		if err != nil {
			return err
		}
		if studioRevision(source) == t.After {
			if err = s.takes.Mark(t.ID, takejournal.Committed); err != nil {
				return err
			}
			continue
		}
		if studioRevision(source) != t.Before {
			if err = s.takes.Mark(t.ID, takejournal.Conflict); err != nil {
				return err
			}
			continue
		}
		if err = s.commitTake(t.ID, t.Before, t.Candidate); err != nil && !errors.Is(err, errTakeRevision) {
			return err
		}
	}
	return nil
}

// commitTake publishes the asset before preparing source, then uses the same
// atomic revision exchange as other Studio edits. Reopening can distinguish an
// applied source from a pending candidate without replaying an undo.
func (s *studio) commitTake(id, expected string, candidate []byte) error {
	current, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if studioRevision(current) != expected {
		if e := s.takes.Mark(id, takejournal.Conflict); e != nil {
			return e
		}
		return errTakeRevision
	}
	t, err := s.takes.Get(id)
	if err != nil {
		return err
	}
	if candidate == nil {
		candidate, err = takejournal.SelectSource(current, t)
		if err != nil {
			return err
		}
	}
	p, err := compileStudioSource(s.path, candidate)
	if err != nil {
		return err
	}
	if err = s.takes.Prepare(id, current, candidate); err != nil {
		return err
	}
	info, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	if err = studioRecoveryConflict(s.path); err != nil {
		return err
	}
	committed, preserved, err := studioWriteTakeIfRevision(s.path, candidate, info.Mode().Perm(), expected, nil, s.takes.Checkpoint)
	if !committed {
		if e := s.takes.Mark(id, takejournal.Conflict); e != nil {
			return e
		}
		if err != nil {
			return fmt.Errorf("%w: %v (source recovery %s)", errTakeRevision, err, preserved)
		}
		return errTakeRevision
	}
	if err != nil {
		return err
	}
	if err = s.takes.Mark(id, takejournal.Source); err != nil {
		return err
	}
	s.lastGoodSource, s.lastGoodProject = bytes.Clone(candidate), p
	if s.history != nil {
		s.history.recordSourceWrite(current, candidate, "Select audio take", studioHistoryWriteNew, 0)
	}
	return s.takes.Mark(id, takejournal.Committed)
}
func (s *studio) takeState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var takes []takejournal.Take
	if s.takes != nil {
		takes = s.takes.Takes()
	}
	studioJSON(w, http.StatusOK, map[string]any{"takes": takes, "activeCapture": s.captureID})
}
func (s *studio) takeCommand(w http.ResponseWriter, r *http.Request) {
	edit, ok := studioRequestLimit(w, r, 128<<20)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	responseTake := s.captureID
	fail := func(err error) {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, errTakeRevision) {
			status = http.StatusConflict
		}
		studioJSON(w, status, map[string]any{"error": err.Error(), "take": responseTake, "retained": true})
	}
	if err := s.openTakes(); err != nil {
		fail(err)
		return
	}
	switch edit.Action {
	case "import":
		if s.captureID != "" {
			fail(errors.New("stop native capture before importing a browser take"))
			return
		}
		id, err := s.importBrowserTake(edit)
		responseTake = id
		if err != nil {
			fail(err)
			return
		}
	case "audition":
		if err := s.auditionTake(w, edit); err != nil {
			fail(err)
		}
		return
	case "arm":
		if s.captureID != "" {
			fail(errors.New("finish the current take before arming another"))
			return
		}
		source, err := os.ReadFile(s.path)
		if err != nil {
			fail(err)
			return
		}
		if studioRevision(source) != edit.Revision {
			fail(errTakeRevision)
			return
		}
		rate, channels, err := s.transport.prepareTakeInput()
		if err != nil {
			fail(err)
			return
		}
		id, err := s.takes.Begin(edit.Track, edit.Scene, edit.Revision, rate, channels)
		if err != nil {
			fail(err)
			return
		}
		responseTake = id
		recorder, err := capture.NewRecorder(64, 8192, channels, s.takes.Writer(id))
		if err != nil {
			fail(err)
			return
		}
		if err = s.transport.armCapture(recorder); err != nil {
			recorder.Close()
			fail(err)
			return
		}
		s.captureID, s.captureRecorder = id, recorder
	case "start":
		if s.captureID == "" {
			fail(errors.New("arm a take first"))
			return
		}
		if err := s.transport.startCapture(capture.Calibration{}); err != nil {
			fail(err)
			return
		}
	case "stop":
		if s.captureID == "" {
			fail(errors.New("no active audio take"))
			return
		}
		s.transport.stop()
		drainErr := s.transport.disarmCapture()
		snapshot := s.captureRecorder.Snapshot()
		id := s.captureID
		s.captureID = ""
		s.captureRecorder = nil
		if err := s.takes.Finalize(id, snapshot.Incomplete || drainErr != nil); err != nil {
			fail(err)
			return
		}
		if err := s.takes.Publish(id); err != nil {
			fail(err)
			return
		}
		t, _ := s.takes.Get(id)
		if err := s.commitTake(id, t.Expected, nil); err != nil {
			fail(err)
			return
		}
	case "recover", "select":
		responseTake = edit.TakeID
		if s.captureID != "" {
			fail(errors.New("stop capture before selecting a take"))
			return
		}
		if err := s.takes.Recover(); err != nil {
			fail(err)
			return
		}
		t, err := s.takes.Get(edit.TakeID)
		if err != nil {
			fail(err)
			return
		}
		if t.Frames == 0 {
			fail(errors.New("take has no durable PCM"))
			return
		}
		if err = s.commitTake(edit.TakeID, edit.Revision, nil); err != nil {
			fail(err)
			return
		}
	default:
		fail(errors.New("take action must be arm, start, stop, recover, select, import or audition"))
		return
	}
	source, err := os.ReadFile(s.path)
	if err != nil {
		fail(err)
		return
	}
	studioJSON(w, http.StatusOK, map[string]any{"revision": studioRevision(source), "source": string(source), "take": responseTake, "takes": s.takes.Takes(), "activeCapture": s.captureID})
}

func takeRoot(score string) string { dir, _ := takejournal.ProjectRoot(score); return dir }

// A crash may leave an exchanged or staged source without its revision receipt.
// Only bytes identified by this prepared transaction gain a receipt; a later
// external write still differs from that receipt and triggers the existing guard.
func (s *studio) recoverTakeReceipts(t takejournal.Take) error {
	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	prefix := strings.TrimSuffix(studioRecoveryPattern(s.path), "*")
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) || strings.HasSuffix(entry.Name(), ".revision") {
			continue
		}
		path := filepath.Join(filepath.Dir(s.path), entry.Name())
		if _, err := os.Stat(path + ".revision"); !os.IsNotExist(err) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rev := studioRevision(data)
		if rev != t.Before && rev != t.After {
			continue
		}
		if err = studioKeepRecovery(path, rev); err != nil {
			return err
		}
	}
	return takejournal.SyncDirectory(filepath.Dir(s.path))
}

// shutdown drains the device writer and preserves a short final batch even
// when the application closes before the explicit Stop command.
func (s *studio) shutdown() error {
	s.transport.close()
	if s.takes == nil {
		return nil
	}
	defer s.takes.Close()
	if s.captureRecorder != nil && s.captureID != "" {
		drainErr := s.captureRecorder.Close()
		snapshot := s.captureRecorder.Snapshot()
		if err := s.takes.Finalize(s.captureID, snapshot.Incomplete || drainErr != nil); err != nil {
			return err
		}
		if err := s.takes.Publish(s.captureID); err != nil {
			return err
		}
		t, err := s.takes.Get(s.captureID)
		if err != nil {
			return err
		}
		return s.commitTake(s.captureID, t.Expected, nil)
	}
	return nil
}
