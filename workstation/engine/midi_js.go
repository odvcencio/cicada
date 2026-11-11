//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"syscall/js"
	"time"

	"m31labs.dev/cicada/host/midi"
	"m31labs.dev/cicada/workstation/control"
)

func (u *liveUI) loadPortableMIDIPorts() {
	response, err := nativeRequest{Method: methodGet, Path: "/api/midi/ports", Timeout: 5 * time.Second, Limit: 65537}.do(u.ctx)
	if err != nil {
		if !u.closed.Load() {
			u.status.Set("Could not read local MIDI port settings.")
		}
		return
	}
	var result struct {
		Ports    map[string]string `json:"ports"`
		Revision string            `json:"configRevision"`
		Error    string            `json:"error"`
	}
	if err := decodeLimited(response.Body, 65537, &result); err != nil || response.Status != statusOK {
		if !u.closed.Load() {
			if result.Error != "" {
				u.status.Set(result.Error)
			} else {
				u.status.Set("Could not read local MIDI port settings.")
			}
		}
		return
	}
	if u.closed.Load() {
		return
	}
	u.localMIDIPorts = result.Ports
	clear(u.midiPickups)
	u.localMIDIRevision = result.Revision
	u.renderMIDIPortDevices()
	var missing []string
	for _, port := range u.props.PortableMIDI.Ports {
		if result.Ports[port] == "" {
			missing = append(missing, port)
		}
	}
	if len(missing) > 0 {
		u.status.Set("Set local MIDI ports in cicada.local: " + strings.Join(missing, ", "))
	}
	u.renderMappings()
}

func (u *liveUI) renderMIDIPortDevices() {
	if u.props.PortableMIDI == nil || len(u.props.PortableMIDI.Ports) == 0 {
		return
	}
	selectEl := u.query("[data-live-device]")
	if selectEl.IsNull() {
		return
	}
	port := u.selected("[data-live-port]")
	bound := u.localMIDIPorts[port]
	names := map[string]bool{}
	for id := range u.webMIDIPorts {
		if name := u.midiNames[id]; name != "" {
			names[name] = true
		}
	}
	for _, name := range u.nativeMIDINames {
		if name != "" {
			names[name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	selectEl.Call("replaceChildren")
	add := func(value, label string) {
		option := js.Global().Get("document").Call("createElement", "option")
		option.Set("value", value)
		option.Set("textContent", label)
		selectEl.Call("append", option)
	}
	add("", "Choose a connected device")
	if bound != "" && !names[bound] {
		add(bound, bound+" · disconnected")
	}
	for _, name := range ordered {
		add(name, name)
	}
	if bound != "" {
		selectEl.Set("value", bound)
	}
}

func (u *liveUI) releaseMIDIPrefix(prefix string) {
	u.releaseMIDIPrefixAt(prefix, nil)
}

func (u *liveUI) releaseMIDIPrefixAt(prefix string, at *control.NoteTime) {
	clear(u.midiPickups)
	u.midiNotes.DisconnectPrefix(strings.TrimPrefix(prefix, "midi:"))
	for id, group := range u.monitorGroups {
		if strings.HasPrefix("midi:"+group.Device+":", prefix) {
			delete(u.monitorGroups, id)
		}
	}
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
		u.nativeMIDINames = nil
		u.renderMIDIPortDevices()
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
	// The input stream stays open while the controller plays, so it is read
	// as it arrives and has no timeout; cancelling ctx closes it.
	status, body, err := nativeRequest{Method: methodPost, Path: "/api/midi/input", Body: []byte("{}"), Headers: map[string]string{
		"Content-Type": "application/json", "X-CSRF-Token": u.props.CSRF,
	}}.stream(ctx)
	if err != nil {
		return "MIDI connection ended."
	}
	defer body.Close()
	if status != statusOK {
		return midiInputRefusal(body)
	}
	decoder := json.NewDecoder(body)
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
			u.nativeMIDINames = names
			u.renderMIDIPortDevices()
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
