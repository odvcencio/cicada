//go:build js && wasm

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/workstation/control"
)

func (u *liveUI) takeBaseRevision() string {
	if u.takeRevision != "" {
		return u.takeRevision
	}
	return u.props.Revision
}

func (u *liveUI) checkpointSnapshot() []control.Recording {
	copy := append([]control.Recording(nil), u.takes...)
	for i := range copy {
		copy[i].Notes = append([]control.Note(nil), copy[i].Notes...)
		for j := range copy[i].Notes {
			copy[i].Notes[j].Expressions = append([]control.NoteExpression(nil), copy[i].Notes[j].Expressions...)
			if input := copy[i].Notes[j].Input; input != nil {
				value := *input
				copy[i].Notes[j].Input = &value
			}
		}
	}
	for _, encoded := range u.active {
		i, n := encoded/1024, encoded%1024
		if i < len(copy) && n < len(copy[i].Notes) {
			copy[i].Notes[n].End(control.NoteTime{Tick: u.tick()})
			copy[i].Notes[n].EndEstimated = true
		}
	}
	return copy
}

// Recovery IO has its own worker. A slow disk or a failed checkpoint never
// queues ahead of note-on/off commands or runs on the native audio callback.
func (u *liveUI) checkpointNotes() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var attempted time.Time
	failedTake := ""
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
		case <-u.checkpointWake:
			if time.Since(attempted) < 500*time.Millisecond {
				continue
			}
		}
		if u.checkingTake {
			if err := u.checkRestoredTake(); err != nil {
				u.status.Set("Saved note recovery is unavailable. Your browser copy is retained.")
			}
			continue
		}
		if u.closed.Load() || u.finishing || len(u.takes) == 0 || !u.takeDirty && len(u.active) == 0 {
			continue
		}
		attempted = time.Now()
		id := u.takeID
		u.takeSequence++
		sequence := u.takeSequence
		data, err := json.Marshal(u.checkpointSnapshot())
		if err != nil {
			continue
		}
		u.takeDirty = false
		u.persistTakeStorage()
		if !u.props.PersistentNotes {
			continue
		}
		_, err = u.post(u.ctx, "note-checkpoint", map[string]string{"takeId": id, "sequence": strconv.FormatUint(sequence, 10), "revision": u.takeBaseRevision(), "recordings": string(data)})
		if u.closed.Load() || u.takeID != id || u.finishing {
			continue
		}
		if err != nil {
			var failure *liveActionFailure
			if errors.As(err, &failure) && failure.Status == 409 {
				u.checkingTake = true
				u.syncControls()
				continue
			}
			failedTake = id
			u.takeDirty = true
			u.status.Set(fmt.Sprintf("Note recovery could not save. Your browser copy is retained. %s", err))
		} else if failedTake == id {
			failedTake = ""
			u.status.Set("Note recovery resumed. Your performance is saved on this computer.")
		}
	}
}

// Resolve an interrupted response before trusting an old browser receipt.
// A committed/discarded performance must not reappear as an unfinished take.
func (u *liveUI) checkRestoredTake() error {
	id := u.takeID
	origin := js.Global().Get("location").Get("origin").String()
	request, err := http.NewRequestWithContext(u.ctx, http.MethodGet, origin+"/api/note-recovery/"+id, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		u.checkingTake = false
		u.syncControls()
		return nil
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("recovery unavailable")
	}
	var saved struct {
		State    string              `json:"state"`
		Sequence uint64              `json:"sequence"`
		Takes    []control.Recording `json:"takes"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&saved); err != nil {
		return err
	}
	if u.closed.Load() || u.takeID != id {
		return nil
	}
	u.checkingTake = false
	local, _ := json.Marshal(u.takes)
	stored, _ := json.Marshal(saved.Takes)
	finished := saved.State == "applied" || saved.State == "discarded" || saved.State == "ready"
	if (finished || saved.Sequence > u.takeSequence) && (!bytes.Equal(local, stored) || u.recording) {
		u.takeID = strings.ReplaceAll(js.Global().Get("crypto").Call("randomUUID").String(), "-", "")
		u.takeSequence = 0
		u.takeDirty = true
		u.persistTakeStorage()
		u.status.Set("This window has another unfinished copy. It is retained as a separate performance.")
	} else if finished {
		u.takes = nil
		u.takeID, u.takeRevision = "", ""
		u.takeDirty = false
		u.takeSequence = 0
		u.persistTakeStorage()
		u.status.Set("Your saved performance is already retained or handled. Saved note performances contains any unapplied copy.")
		if saved.State == "ready" {
			u.status.Set("Note take retained for review. Commit it to patterns, or discard it.")
		}
		workspaceRefreshRequested.Set(workspaceRefreshRequested.Get() + 1)
	} else {
		if saved.Sequence > u.takeSequence {
			u.takeSequence = saved.Sequence
		}
		u.persistTakeStorage()
	}
	u.syncControls()
	return nil
}
