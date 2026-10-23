package midi

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestUMPInputKeepsGroupsAndSkipsWholeUnsupportedMessages(t *testing.T) {
	var events []Event
	err := decodeUMP([]uint32{
		0x23903c64,             // group 3, note on
		0x30120102, 0x2090407f, // SysEx payload resembles a note; do not play it
		0x40a03c00, 0xffffffff, // high-resolution expression is not MIDI 1 input
		0x25d07f00, // group 5, channel pressure (two bytes)
		0x12ff0000, // group 2, reset
		0x10f80000, // timing clock is not a note
		0x23803c00, // group 3, note off
	}, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Type: "message", Group: 3, Data: [3]uint8{0x90, 60, 100}, Length: 3},
		{Type: "message", Group: 5, Data: [3]uint8{0xd0, 127, 0}, Length: 2},
		{Type: "reset", Group: 2},
		{Type: "message", Group: 3, Data: [3]uint8{0x80, 60, 0}, Length: 3},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("messages = %#v", events)
	}
}

func TestUMPInputRejectsIncompleteAndInvalidPackets(t *testing.T) {
	for _, words := range [][]uint32{{0x30000000}, {0x50000000, 0, 0}, {0x60903c64}, {0x207f3c64}, {0x20908000}, {0x209000ff}} {
		if err := decodeUMP(words, func(Event) { t.Fatal("invalid packet emitted input") }); err == nil {
			t.Fatalf("accepted %x", words)
		}
	}
}

func TestNativeMIDICanceledOpenDoesNotCreateAClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := Open(ctx)
	if stream != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open: %v %v", stream, err)
	}
}

func TestMIDIInputStateRejectsLateMessagesAcrossUnplugAndReconnect(t *testing.T) {
	var state InputState
	message := Event{Type: "message", Device: "controller", Epoch: 1, Data: [3]uint8{144, 60, 100}, Length: 3}
	if state.Accept(message) {
		t.Fatal("input played before its connection was ready")
	}
	state.Accept(Event{Type: "devices", Inputs: []Device{{ID: "controller", Name: "Keyboard", Epoch: 1}}})
	if !state.Accept(message) {
		t.Fatal("active controller input was rejected")
	}
	state.Accept(Event{Type: "devices", Removed: []string{"controller"}})
	if state.Accept(message) {
		t.Fatal("queued note resurrected an unplugged controller")
	}
	state.Accept(Event{Type: "devices", Inputs: []Device{{ID: "controller", Name: "Keyboard", Epoch: 2}}})
	if state.Accept(message) {
		t.Fatal("old connection played after reconnect")
	}
	if state.Accept(Event{Type: "reset", Device: "controller", Epoch: 1}) {
		t.Fatal("old reset interrupted the new connection")
	}
	message.Epoch = 2
	if !state.Accept(message) {
		t.Fatal("reconnected controller input was rejected")
	}
}

func FuzzUMPInput(f *testing.F) {
	f.Add([]byte{0x20, 0x90, 60, 100, 0x20, 0x80, 60, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			return
		}
		words := make([]uint32, len(data)/4)
		for i := range words {
			b := data[i*4:]
			words[i] = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		}
		_ = decodeUMP(words, func(e Event) {
			if e.Group > 15 || e.Type == "message" && (e.Data[0] < 128 || e.Data[0] >= 240 || e.Data[1] > 127 || e.Data[2] > 127 || e.Length < 2 || e.Length > 3) {
				t.Fatalf("invalid event: %+v", e)
			}
		})
	})
}
