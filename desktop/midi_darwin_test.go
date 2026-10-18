package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"
)

func TestMIDIDecoderRunningStatusExpressionAndSysex(t *testing.T) {
	var decoder midiDecoder
	var got [][]int
	emit := func(data []int) { got = append(got, data) }
	decoder.read([]byte{0x90, 60}, emit)
	decoder.read([]byte{100, 0xf8, 61, 80, 0xe2, 0, 96, 0xd2, 70, 0xa2, 61, 90}, emit)
	decoder.read([]byte{0xf0, 0x7d, 10, 11}, emit)
	decoder.read([]byte{12, 0xf8, 0xf7, 60, 90, 0x82, 61, 0}, emit)
	want := [][]int{{0x90, 60, 100}, {0xf8}, {0x90, 61, 80}, {0xe2, 0, 96}, {0xd2, 70}, {0xa2, 61, 90}, {0xf8}, {0x82, 61, 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded messages %v, want %v", got, want)
	}
}

func TestMIDINativeCallbackIsBoundedAndNonblocking(t *testing.T) {
	m := newNativeMIDI(func(string, any) error { t.Fatal("native callback entered UI transport"); return nil })
	var bytes [32]byte
	binary.LittleEndian.PutUint32(bytes[:4], 1)
	binary.LittleEndian.PutUint16(bytes[12:14], 3)
	copy(bytes[14:], []byte{0x90, 60, 100})
	m.receive(unsafe.Pointer(&bytes[0]), 42)
	packet := <-m.packets
	if packet.source != 42 || packet.length != 3 || packet.data[0] != 0x90 || packet.data[2] != 100 {
		t.Fatalf("lost native MIDI packet: %+v", packet)
	}
	for range cap(m.packets) {
		m.packets <- nativeMIDIPacket{}
	}
	start := time.Now()
	m.receive(unsafe.Pointer(&bytes[0]), 42)
	if !m.overflow.Load() || time.Since(start) > time.Second {
		t.Fatal("full input queue blocked CoreMIDI callback or failed to request note release")
	}
	m.closed.Store(true)
	m.overflow.Store(false)
	m.receive(unsafe.Pointer(&bytes[0]), 42)
	if m.overflow.Load() {
		t.Fatal("closed receiver still accepted input")
	}
}

func TestMIDINativeOversizedPacketReleasesNotesWhileIdle(t *testing.T) {
	reset := make(chan struct{}, 1)
	m := newNativeMIDI(func(topic string, _ any) error {
		if topic == "cicada.midi.reset" {
			reset <- struct{}{}
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.run(ctx)
	var bytes [4]byte
	binary.LittleEndian.PutUint32(bytes[:], 129)
	m.receive(unsafe.Pointer(&bytes[0]), 42)
	select {
	case <-reset:
	case <-time.After(time.Second):
		t.Fatal("an oversized final packet left held notes active until more input arrived")
	}
}

// Creates and disposes only private virtual endpoints. It never opens audio,
// records a microphone, or connects the receiver to another MIDI source.
func TestNativeMIDIVirtualSource(t *testing.T) {
	if os.Getenv("CICADA_TEST_NATIVE_MIDI") != "1" {
		t.Skip("set CICADA_TEST_NATIVE_MIDI=1 for private CoreMIDI virtual-port integration")
	}
	a, err := loadCoreMIDI()
	if err != nil {
		t.Fatal(err)
	}
	name := a.stringCreate(0, fmt.Sprintf("Cicada private MIDI test %d", os.Getpid()), midiUTF8)
	defer a.release(name)
	var client, source uint32
	if err := midiStatus("test create client", a.clientCreate(name, 0, 0, &client)); err != nil {
		t.Fatal(err)
	}
	defer a.clientDispose(client)
	if err := midiStatus("test create source", a.sourceCreate(client, name, &source)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if source != 0 {
			a.endpointDispose(source)
		}
	}()
	messages := make(chan nativeMIDIMessage, 32)
	states := make(chan []nativeMIDIInput, 8)
	resets := make(chan struct{}, 8)
	m := newNativeMIDI(func(topic string, value any) error {
		switch topic {
		case "cicada.midi.messages":
			for _, message := range value.(map[string]any)["messages"].([]nativeMIDIMessage) {
				messages <- message
			}
		case "cicada.midi.state":
			states <- value.(map[string]any)["inputs"].([]nativeMIDIInput)
		case "cicada.midi.reset":
			resets <- struct{}{}
		}
		return nil
	})
	testSource := source
	m.acceptSource = func(endpoint uint32) bool { return endpoint == testSource }
	defer m.close()
	inputs, err := m.open()
	if err != nil || len(inputs) != 1 {
		t.Fatalf("private endpoint unavailable: inputs=%v err=%v", inputs, err)
	}
	want := [][]int{{0x92, 60, 100}, {0xb2, 74, 88}, {0xe2, 0, 96}, {0xd2, 70}, {0x82, 60, 0}, {0xb2, 123, 0}}
	var buffer [1024]byte
	base := uintptr(unsafe.Pointer(&buffer[0]))
	packet := a.packetListInit(base)
	for i, message := range want {
		data := make([]byte, len(message))
		for i, b := range message {
			data[i] = byte(b)
		}
		packet = a.packetListAdd(base, uintptr(len(buffer)), packet, uint64(i+1), uintptr(len(data)), &data[0])
		if packet == 0 {
			t.Fatal("could not build private test packet list")
		}
	}
	if err := midiStatus("test receive", a.received(source, base)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range want {
		select {
		case message := <-messages:
			if message.ID != inputs[0].ID || !reflect.DeepEqual(message.Data, expected) {
				t.Fatalf("received %v, want %v", message, expected)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("private native MIDI input delivery timed out")
		}
	}
	select {
	case <-resets:
	case <-time.After(time.Second):
		t.Fatal("controller all-notes-off did not release held notes")
	}
	if err := midiStatus("test unplug", a.endpointDispose(source)); err != nil {
		t.Fatal(err)
	}
	source = 0
	deadline := time.After(3 * time.Second)
	for {
		select {
		case state := <-states:
			if len(state) == 0 {
				m.close()
				m.close()
				m.mu.Lock()
				defer m.mu.Unlock()
				if m.client != 0 || m.port != 0 {
					t.Fatal("MIDI resources survived shutdown")
				}
				return
			}
		case <-deadline:
			t.Fatal("private MIDI unplug did not update native inputs")
		}
	}
}

// TestNativeMIDIAppSource is a bounded sender for the packaged-app smoke test.
// Its marker and .stop file live in a caller-created private temporary folder.
// The matching native-midi-workflow.js listens only to this virtual endpoint.
func TestNativeMIDIAppSource(t *testing.T) {
	ready := os.Getenv("CICADA_TEST_MIDI_APP_SOURCE")
	if ready == "" {
		t.Skip("set CICADA_TEST_MIDI_APP_SOURCE to a private ready marker for the WKWebView MIDI smoke test")
	}
	a, err := loadCoreMIDI()
	if err != nil {
		t.Fatal(err)
	}
	nameText := fmt.Sprintf("Cicada private app MIDI %d", os.Getpid())
	name := a.stringCreate(0, nameText, midiUTF8)
	defer a.release(name)
	var client, source uint32
	if err := midiStatus("test create sender", a.clientCreate(name, 0, 0, &client)); err != nil {
		t.Fatal(err)
	}
	defer a.clientDispose(client)
	if err := midiStatus("test create source", a.sourceCreate(client, name, &source)); err != nil {
		t.Fatal(err)
	}
	defer a.endpointDispose(source)
	marker, err := os.OpenFile(ready, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = json.NewEncoder(marker).Encode(map[string]any{"name": nameText, "pid": os.Getpid()})
	closeErr := marker.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write private MIDI marker: %v %v", err, closeErr)
	}
	t.Log("private virtual MIDI sender ready")
	var buffer [1024]byte
	base := uintptr(unsafe.Pointer(&buffer[0]))
	packet := a.packetListInit(base)
	for i, data := range [][]byte{{0x92, 60, 100}, {0xb2, 74, 88}, {0xe2, 0, 96}, {0xd2, 70}, {0x82, 60, 0}, {0xb2, 123, 0}} {
		packet = a.packetListAdd(base, uintptr(len(buffer)), packet, uint64(i+1), uintptr(len(data)), &data[0])
		if packet == 0 {
			t.Fatal("could not build private app-test MIDI packet list")
		}
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(60 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-deadline.C:
			t.Log("private MIDI sender closed at bounded deadline")
			return
		case <-ticker.C:
			if _, err := os.Stat(ready + ".stop"); err == nil {
				t.Log("private MIDI sender closed on smoke completion")
				return
			}
			if err := midiStatus("test send", a.received(source, base)); err != nil {
				t.Fatal(err)
			}
		}
	}
}
