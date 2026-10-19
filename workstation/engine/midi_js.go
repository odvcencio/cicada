//go:build js && wasm

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"

	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/workstation/control"
)

func (u *liveUI) releaseMIDIPrefix(prefix string) {
	u.releaseMIDIPrefixAt(prefix, nil)
}

func (u *liveUI) releaseMIDIPrefixAt(prefix string, at *control.NoteTime) {
	u.midiNotes.DisconnectPrefix(strings.TrimPrefix(prefix, "midi:"))
	for id := range u.held {
		if strings.HasPrefix(id, prefix) {
			u.noteOffAt(id, at)
		}
	}
}

func (u *liveUI) enableNativeMIDI() {
	ctx, cancel := context.WithCancel(u.ctx)
	u.nativeMIDICancel = cancel
	u.nativeMIDIGeneration++
	generation := u.nativeMIDIGeneration
	u.query("[data-live-control=midi]").Set("textContent", "Disconnect MIDI")
	u.status.Set("Connecting MIDI inputs…")
	go func() {
		message := u.readNativeMIDI(ctx, generation)
		canceled := ctx.Err() != nil
		cancel()
		if u.closed.Load() || generation != u.nativeMIDIGeneration {
			return
		}
		u.nativeMIDICancel = nil
		u.releaseMIDIPrefix("midi:native:")
		u.query("[data-live-control=midi]").Set("textContent", "Enable MIDI")
		u.query("[data-live-control=midi]").Set("disabled", false)
		if canceled {
			message = "MIDI disconnected."
		}
		u.status.Set(message + " Keyboard and pointer notes remain available.")
	}()
}

func (u *liveUI) readNativeMIDI(ctx context.Context, generation uint64) string {
	origin := js.Global().Get("location").Get("origin").String()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/api/midi/input", bytes.NewBufferString("{}"))
	if err != nil {
		return "MIDI could not connect."
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", u.props.CSRF)
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return "MIDI connection ended."
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		var failure struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
			return failure.Error
		}
		return "MIDI could not connect. " + strings.TrimSpace(string(data))
	}
	decoder := json.NewDecoder(response.Body)
	for {
		var event midi.Event
		if err := decoder.Decode(&event); err != nil {
			return "MIDI connection ended. Connect again to resume."
		}
		if ctx.Err() != nil || u.closed.Load() || generation != u.nativeMIDIGeneration {
			return "MIDI disconnected."
		}
		if event.Position != nil {
			u.syncMIDIClock(event.Position.Epoch)
		}
		timing, current := control.NativeNoteTime(event, u.clockEpoch, u.tick())
		var at *control.NoteTime
		if current {
			at = &timing
		}
		switch event.Type {
		case "devices":
			for _, id := range event.Removed {
				u.releaseMIDIPrefixAt("midi:native:"+id+":", at)
			}
			var names []string
			for _, device := range event.Inputs {
				names = append(names, device.Name)
				for group := 0; group < 16; group++ {
					u.midiNames[fmt.Sprintf("native:%s:g%d", device.ID, group)] = device.Name
				}
			}
			if len(names) == 0 {
				u.status.Set("MIDI ready. Connect a controller; inputs appear automatically.")
			} else {
				u.status.Set("MIDI connected · " + strings.Join(names, ", "))
			}
		case "reset":
			if current {
				u.releaseMIDIPrefixAt("midi:native:"+event.Device+":", at)
			}
		case "message":
			if !current {
				continue
			}
			if event.Length < 2 || event.Length > 3 {
				return "MIDI input returned an invalid message."
			}
			values := []any{event.Data[0], event.Data[1], event.Data[2]}
			u.midiMessageAt(fmt.Sprintf("native:%s:g%d", event.Device, event.Group), js.ValueOf(values), at)
		case "error":
			return event.Message
		}
	}
}
