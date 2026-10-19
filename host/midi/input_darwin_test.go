//go:build darwin && (amd64 || arm64)

package midi

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

func TestCoreMIDIInputHotplugAcrossProcesses(t *testing.T) {
	if os.Getenv("CICADA_COREMIDI_HELPER") == "1" {
		runCoreMIDISourceProcess(t)
		return
	}
	if os.Getenv("CICADA_COREMIDI_TEST") != "1" {
		t.Skip("set CICADA_COREMIDI_TEST=1 for cross-process hotplug qualification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	// Open the reader before starting the separate source process. A test
	// that creates its source first cannot catch missing hotplug notifications.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, executable, "-test.run=^TestCoreMIDIInputHotplugAcrossProcesses$")
	child.Env = append(os.Environ(), "CICADA_COREMIDI_HELPER=1")
	in, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = child.Wait() }()
	reader := bufio.NewReader(out)
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("source start: %q %v %s", line, err, stderr.String())
	}
	wait := func(match func(Event) bool) Event {
		t.Helper()
		for {
			select {
			case event := <-stream.Events():
				if match(event) {
					return event
				}
			case err := <-stream.Errors():
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatalf("cross-process MIDI event timed out; parent source count=%d; helper=%s", core.count(), stderr.String())
			}
		}
	}
	var id string
	wait(func(event Event) bool {
		for _, d := range event.Inputs {
			if d.Name == "Cicada cross-process MIDI test" {
				id = d.ID
				return true
			}
		}
		return false
	})
	if _, err := fmt.Fprintln(in, "send"); err != nil {
		t.Fatal(err)
	}
	for _, want := range [][3]uint8{{0x90, 60, 100}, {0xb0, 21, 96}, {0x80, 60, 0}} {
		event := wait(func(e Event) bool { return e.Type == "message" && e.Device == id })
		if event.Data != want {
			t.Fatalf("cross-process data=%v, want %v", event.Data, want)
		}
	}
	if _, err := fmt.Fprintln(in, "drop"); err != nil {
		t.Fatal(err)
	}
	wait(func(event Event) bool { return slices.Contains(event.Removed, id) })
	if err := child.Wait(); err != nil {
		t.Fatalf("source exit: %v %s", err, stderr.String())
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		reopened, err := Open(ctx)
		if err != nil {
			t.Fatalf("MIDI reconnect failed: %v", err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("cross-process hotplug, note/CC delivery, unplug, and three reconnects passed")
}

func runCoreMIDISourceProcess(t *testing.T) {
	if err := core.initialize(); err != nil {
		t.Fatal(err)
	}
	var create func(uint32, uintptr, uint32, *uint32) int32
	var dispose func(uint32) int32
	var init func(unsafe.Pointer, uint32) unsafe.Pointer
	var add func(unsafe.Pointer, uintptr, unsafe.Pointer, uint64, uintptr, *uint32) unsafe.Pointer
	var send func(uint32, unsafe.Pointer) int32
	purego.RegisterLibFunc(&create, core.library, "MIDISourceCreateWithProtocol")
	purego.RegisterLibFunc(&dispose, core.library, "MIDIEndpointDispose")
	purego.RegisterLibFunc(&init, core.library, "MIDIEventListInit")
	purego.RegisterLibFunc(&add, core.library, "MIDIEventListAdd")
	purego.RegisterLibFunc(&send, core.library, "MIDIReceivedEventList")
	name := core.stringCreate(0, "Cicada cross-process MIDI test", 0x08000100)
	defer core.release(name)
	var source uint32
	if status := create(core.client, name, 1, &source); status != 0 {
		t.Fatalf("create source: %d", status)
	}
	defer func() {
		if source != 0 {
			dispose(source)
		}
	}()
	fmt.Println("ready")
	var command string
	if _, err := fmt.Fscan(os.Stdin, &command); err != nil || command != "send" {
		t.Fatal("missing send command", err)
	}
	words := []uint32{0x20903c64, 0x20b01560, 0x20803c00}
	packet := make([]byte, 256)
	first := init(unsafe.Pointer(&packet[0]), 1)
	if add(unsafe.Pointer(&packet[0]), uintptr(len(packet)), first, 0, uintptr(len(words)), &words[0]) == nil {
		t.Fatal("packet overflow")
	}
	if status := send(source, unsafe.Pointer(&packet[0])); status != 0 {
		t.Fatalf("send source: %d", status)
	}
	runtime.KeepAlive(packet)
	runtime.KeepAlive(words)
	if _, err := fmt.Fscan(os.Stdin, &command); err != nil || command != "drop" {
		t.Fatal("missing drop command", err)
	}
	if status := dispose(source); status != 0 {
		t.Fatalf("dispose source: %d", status)
	}
	source = 0
}

// This sends MIDI only through a temporary virtual source. It opens no audio
// device, plays no sound, and never sends to a physical controller or output.
func TestCoreMIDIInputReceivesVirtualSourceAndDisconnect(t *testing.T) {
	if os.Getenv("CICADA_COREMIDI_TEST") != "1" {
		t.Skip("set CICADA_COREMIDI_TEST=1 for native virtual-controller qualification")
	}
	if err := core.initialize(); err != nil {
		t.Fatal(err)
	}
	var sourceCreate func(uint32, uintptr, uint32, *uint32) int32
	var clientDispose func(uint32) int32
	var endpointDispose func(uint32) int32
	var eventInit func(unsafe.Pointer, uint32) unsafe.Pointer
	var eventAdd func(unsafe.Pointer, uintptr, unsafe.Pointer, uint64, uintptr, *uint32) unsafe.Pointer
	var received func(uint32, unsafe.Pointer) int32
	purego.RegisterLibFunc(&sourceCreate, core.library, "MIDISourceCreateWithProtocol")
	purego.RegisterLibFunc(&clientDispose, core.library, "MIDIClientDispose")
	purego.RegisterLibFunc(&endpointDispose, core.library, "MIDIEndpointDispose")
	purego.RegisterLibFunc(&eventInit, core.library, "MIDIEventListInit")
	purego.RegisterLibFunc(&eventAdd, core.library, "MIDIEventListAdd")
	purego.RegisterLibFunc(&received, core.library, "MIDIReceivedEventList")
	name := core.stringCreate(0, "Cicada virtual MIDI qualification", 0x08000100)
	defer core.release(name)
	var client, source uint32
	if status := core.clientCreate(name, &client, nil); status != 0 {
		t.Fatalf("virtual client: %d", status)
	}
	defer clientDispose(client)
	if status := sourceCreate(client, name, 1, &source); status != 0 {
		t.Fatalf("virtual source: %d", status)
	}
	defer func() {
		if source != 0 {
			endpointDispose(source)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	wait := func(match func(Event) bool) Event {
		t.Helper()
		for {
			select {
			case e := <-stream.Events():
				if match(e) {
					return e
				}
			case err := <-stream.Errors():
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("virtual MIDI input timed out")
			}
		}
	}
	var id string
	wait(func(e Event) bool {
		for _, d := range e.Inputs {
			if d.Name == "Cicada virtual MIDI qualification" {
				id = d.ID
				return true
			}
		}
		return false
	})
	words := []uint32{0x23903c64, 0x23b01560, 0x23803c00}
	packet := make([]byte, 512)
	first := eventInit(unsafe.Pointer(&packet[0]), 1)
	if next := eventAdd(unsafe.Pointer(&packet[0]), uintptr(len(packet)), first, 0, uintptr(len(words)), &words[0]); next == nil {
		t.Fatal("virtual event list overflow")
	}
	if status := received(source, unsafe.Pointer(&packet[0])); status != 0 {
		t.Fatalf("send virtual MIDI: %d", status)
	}
	runtime.KeepAlive(packet)
	runtime.KeepAlive(words)
	for _, want := range [][3]uint8{{0x90, 60, 100}, {0xb0, 21, 96}, {0x80, 60, 0}} {
		e := wait(func(e Event) bool { return e.Type == "message" && e.Device == id })
		if e.Group != 3 || e.Data != want || e.Length != 3 {
			t.Fatalf("virtual message: %+v, want %v", e, want)
		}
	}
	if status := endpointDispose(source); status != 0 {
		t.Fatalf("disconnect source: %d", status)
	}
	source = 0
	wait(func(e Event) bool { return e.Type == "devices" && slices.Contains(e.Removed, id) })
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	connections.RLock()
	remaining := len(connections.values)
	connections.RUnlock()
	if remaining != 0 {
		t.Fatalf("%d native connections survived close", remaining)
	}
	t.Log("native note, CC, note-off, source removal, and port cleanup passed")
}
