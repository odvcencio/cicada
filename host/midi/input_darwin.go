//go:build darwin && (amd64 || arm64)

package midi

import (
	"cmp"
	"context"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

type coreAPI struct {
	once                        sync.Once
	err                         error
	library                     uintptr
	clientCreate                func(uintptr, *uint32, unsafe.Pointer) int32
	client                      uint32
	portCreate                  func(uint32, uintptr, uint32, *uint32, unsafe.Pointer) int32
	portDispose                 func(uint32) int32
	connect                     func(uint32, uint32, uintptr) int32
	disconnect                  func(uint32, uint32) int32
	count                       func() uintptr
	source                      func(uintptr) uint32
	stringProperty              func(uint32, uintptr, *uintptr) int32
	integerProperty             func(uint32, uintptr, *int32) int32
	stringCreate                func(uintptr, string, uint32) uintptr
	stringRead                  func(uintptr, *byte, int64, uint32) bool
	release                     func(uintptr)
	runLoop                     func()
	displayName, name, uniqueID uintptr
	// The input and notification blocks live for the process. Connection
	// changes never allocate another Go callback or native block.
	block []byte
}

var core coreAPI
var connections = struct {
	sync.RWMutex
	next   atomic.Uint64
	values map[uintptr]coreConnection
}{values: map[uintptr]coreConnection{}}

type coreConnection struct {
	stream *coreStream
	device Device
	token  uintptr
}

func (a *coreAPI) initialize() error {
	a.once.Do(func() {
		ready := make(chan struct{})
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			a.initializeOnThread()
			close(ready)
			if a.err != nil {
				return
			}
			// CoreMIDI's process object cache receives changes through the
			// creating thread's run loop, including with dispatch callbacks.
			// Own one control thread for the process; never use the audio thread.
			for {
				a.runLoop()
				time.Sleep(10 * time.Millisecond)
			}
		}()
		<-ready
	})
	return a.err
}

func (a *coreAPI) initializeOnThread() {
	a.library, a.err = purego.Dlopen("/System/Library/Frameworks/CoreMIDI.framework/CoreMIDI", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if a.err != nil {
		return
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		a.err = err
		return
	}
	// Missing symbols are reported as an unavailable backend, never a panic
	// in response to a user pressing Enable MIDI.
	bind := func(target any, library uintptr, name string) {
		if a.err != nil {
			return
		}
		address, err := purego.Dlsym(library, name)
		if err != nil {
			a.err = err
			return
		}
		purego.RegisterFunc(target, address)
	}
	bind(&a.clientCreate, a.library, "MIDIClientCreateWithBlock")
	bind(&a.portCreate, a.library, "MIDIInputPortCreateWithProtocol")
	bind(&a.portDispose, a.library, "MIDIPortDispose")
	bind(&a.connect, a.library, "MIDIPortConnectSource")
	bind(&a.disconnect, a.library, "MIDIPortDisconnectSource")
	bind(&a.count, a.library, "MIDIGetNumberOfSources")
	bind(&a.source, a.library, "MIDIGetSource")
	bind(&a.stringProperty, a.library, "MIDIObjectGetStringProperty")
	bind(&a.integerProperty, a.library, "MIDIObjectGetIntegerProperty")
	bind(&a.stringCreate, cf, "CFStringCreateWithCString")
	bind(&a.stringRead, cf, "CFStringGetCString")
	bind(&a.release, cf, "CFRelease")
	bind(&a.runLoop, cf, "CFRunLoopRun")
	if a.err != nil {
		return
	}
	class, err := purego.Dlsym(purego.RTLD_DEFAULT, "_NSConcreteGlobalBlock")
	if err != nil {
		a.err = err
		return
	}
	a.block, a.err = unix.Mmap(-1, 0, 256, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if a.err != nil {
		return
	}
	callback := purego.NewCallback(receiveCoreMIDI)
	// Block ABI: isa, flags/reserved, invoke, descriptor. The descriptor
	// and signature also reside in native memory. It captures no Go data.
	*(*uintptr)(unsafe.Pointer(&a.block[0])) = class
	*(*uint32)(unsafe.Pointer(&a.block[8])) = 1<<28 | 1<<30
	*(*uintptr)(unsafe.Pointer(&a.block[16])) = callback
	*(*uintptr)(unsafe.Pointer(&a.block[24])) = uintptr(unsafe.Pointer(&a.block[32]))
	*(*uintptr)(unsafe.Pointer(&a.block[40])) = 32
	*(*uintptr)(unsafe.Pointer(&a.block[48])) = uintptr(unsafe.Pointer(&a.block[64]))
	copy(a.block[64:], "v24@?0^v8^v16\x00")
	// A non-nil notification block selects CoreMIDI's dispatch delivery.
	// Its internal object cache must observe hotplug even though streams
	// enumerate on their own control goroutines.
	*(*uintptr)(unsafe.Pointer(&a.block[128])) = class
	*(*uint32)(unsafe.Pointer(&a.block[136])) = 1<<28 | 1<<30
	*(*uintptr)(unsafe.Pointer(&a.block[144])) = purego.NewCallback(func(unsafe.Pointer, unsafe.Pointer) {})
	*(*uintptr)(unsafe.Pointer(&a.block[152])) = uintptr(unsafe.Pointer(&a.block[160]))
	*(*uintptr)(unsafe.Pointer(&a.block[168])) = 32
	*(*uintptr)(unsafe.Pointer(&a.block[176])) = uintptr(unsafe.Pointer(&a.block[192]))
	copy(a.block[192:], "v16@?0^v8\x00")
	a.displayName = a.stringCreate(0, "displayName", 0x08000100)
	a.name = a.stringCreate(0, "name", 0x08000100)
	a.uniqueID = a.stringCreate(0, "uniqueID", 0x08000100)
	if a.displayName == 0 || a.name == 0 || a.uniqueID == 0 {
		a.err = fmt.Errorf("CoreMIDI property names unavailable")
		return
	}
	name := a.stringCreate(0, "Cicada Studio MIDI", 0x08000100)
	if name == 0 {
		a.err = fmt.Errorf("CoreMIDI client name unavailable")
		return
	}
	defer a.release(name)
	// Keep one process client alive. Apple's API warns that disposing the
	// last client can prevent a later input connection from reopening.
	if status := a.clientCreate(name, &a.client, unsafe.Pointer(&a.block[128])); status != 0 {
		a.err = fmt.Errorf("CoreMIDI client: %d", status)
	}
}

type coreStream struct {
	port       uint32
	events     chan Event
	errors     chan error
	stop, done chan struct{}
	closeOnce  sync.Once
	closed     atomic.Bool
	closeErr   error                     // Written before done closes.
	sources    map[uint32]coreConnection // owned by run
}

func openNative(ctx context.Context) (Stream, error) {
	if err := core.initialize(); err != nil {
		return nil, fmt.Errorf("CoreMIDI unavailable: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &coreStream{events: make(chan Event, 512), errors: make(chan error, 1), stop: make(chan struct{}), done: make(chan struct{}), sources: map[uint32]coreConnection{}}
	name := core.stringCreate(0, "Cicada Studio MIDI input", 0x08000100)
	if name == 0 {
		return nil, fmt.Errorf("CoreMIDI client name unavailable")
	}
	defer core.release(name)
	if status := core.portCreate(core.client, name, 1, &s.port, unsafe.Pointer(&core.block[0])); status != 0 {
		return nil, fmt.Errorf("CoreMIDI input port: %d", status)
	}
	if err := s.refresh(true); err != nil {
		s.dispose()
		return nil, err
	}
	go s.run(ctx)
	return s, nil
}

func (s *coreStream) Events() <-chan Event { return s.events }
func (s *coreStream) Errors() <-chan error { return s.errors }
func (s *coreStream) Close() error {
	s.closeOnce.Do(func() { close(s.stop) })
	<-s.done
	return s.closeErr
}
func (s *coreStream) fail(err error) {
	select {
	case s.errors <- err:
	default:
	}
}
func (s *coreStream) emit(event Event) {
	if s.closed.Load() {
		return
	}
	select {
	case s.events <- event:
	default:
		s.fail(ErrOverflow)
	}
}
func (s *coreStream) run(ctx context.Context) {
	defer close(s.done)
	defer s.dispose()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			if err := s.refresh(false); err != nil {
				s.fail(err)
				return
			}
		}
	}
}
func (s *coreStream) dispose() {
	s.closed.Store(true)
	connections.Lock()
	for _, c := range s.sources {
		delete(connections.values, c.token)
	}
	connections.Unlock()
	if status := core.portDispose(s.port); status != 0 {
		s.closeErr = fmt.Errorf("close CoreMIDI input port: %d", status)
	}
}

func sourceName(source uint32) string {
	for _, property := range []uintptr{core.displayName, core.name} {
		var value uintptr
		if core.stringProperty(source, property, &value) != 0 || value == 0 {
			continue
		}
		var buffer [1024]byte
		ok := core.stringRead(value, &buffer[0], int64(len(buffer)), 0x08000100)
		core.release(value)
		if ok {
			for i, b := range buffer {
				if b == 0 {
					if i > 0 {
						return string(buffer[:i])
					}
					break
				}
			}
		}
	}
	return "Unnamed MIDI input"
}

func (s *coreStream) refresh(initial bool) error {
	count := core.count()
	if count > 128 {
		return fmt.Errorf("CoreMIDI exposes more than 128 input sources")
	}
	current := make(map[uint32]Device, int(count))
	for i := uintptr(0); i < count; i++ {
		source := core.source(i)
		if source == 0 {
			continue
		}
		var unique int32
		id := "coremidi:endpoint:" + strconv.FormatUint(uint64(source), 10)
		status := core.integerProperty(source, core.uniqueID, &unique)
		if status != 0 && status != -10835 { // Unknown property is distinct from a vanished endpoint.
			continue
		}
		if status == 0 && unique != 0 {
			id = "coremidi:" + strconv.FormatInt(int64(unique), 10)
		}
		current[source] = Device{ID: id, Name: sourceName(source)}
	}
	changed := initial
	var removed []string
	for source, old := range s.sources {
		if device, ok := current[source]; ok && device.ID == old.device.ID && device.Name == old.device.Name {
			continue
		}
		connections.Lock()
		delete(connections.values, old.token)
		connections.Unlock()
		core.disconnect(s.port, source)
		delete(s.sources, source)
		removed = append(removed, old.device.ID)
		changed = true
	}
	for source, device := range current {
		if _, ok := s.sources[source]; ok {
			continue
		}
		connection := coreConnection{stream: s, device: device, token: uintptr(connections.next.Add(1))}
		connection.device.Epoch = uint64(connection.token)
		connections.Lock()
		connections.values[connection.token] = connection
		connections.Unlock()
		if status := core.connect(s.port, source, connection.token); status != 0 {
			connections.Lock()
			delete(connections.values, connection.token)
			connections.Unlock()
			// A source can disappear between enumeration and connection.
			// Do not lose every other controller because one was unplugged.
			if status == -50 || status == -10834 || status == -10842 {
				continue
			}
			return fmt.Errorf("connect MIDI input %s: %d", device.Name, status)
		}
		s.sources[source] = connection
		changed = true
	}
	if changed {
		inputs := make([]Device, 0, len(s.sources))
		for _, c := range s.sources {
			inputs = append(inputs, c.device)
		}
		slices.SortFunc(inputs, func(a, b Device) int {
			if a.Name != b.Name {
				return cmp.Compare(a.Name, b.Name)
			}
			return cmp.Compare(a.ID, b.ID)
		})
		slices.Sort(removed)
		s.emit(Event{Type: "devices", Inputs: inputs, Removed: removed})
	}
	return nil
}

func receiveCoreMIDI(_ unsafe.Pointer, list unsafe.Pointer, token uintptr) {
	connections.RLock()
	connection, ok := connections.values[token]
	connections.RUnlock()
	if !ok || list == nil || connection.stream.closed.Load() {
		return
	}
	if protocol := *(*uint32)(list); protocol != 1 {
		connection.stream.fail(fmt.Errorf("CoreMIDI returned protocol %d instead of MIDI 1", protocol))
		return
	}
	packets := *(*uint32)(unsafe.Add(list, 4))
	if packets > 4096 {
		connection.stream.fail(fmt.Errorf("CoreMIDI packet list exceeds limit"))
		return
	}
	packet := unsafe.Add(list, 8)
	remaining := uint32(65536)
	for i := uint32(0); i < packets; i++ {
		stamp := *(*uint64)(packet)
		count := *(*uint32)(unsafe.Add(packet, 8))
		if count > remaining {
			connection.stream.fail(fmt.Errorf("CoreMIDI packet words exceed limit"))
			return
		}
		remaining -= count
		words := unsafe.Slice((*uint32)(unsafe.Add(packet, 12)), int(count))
		if err := decodeUMP(words, func(event Event) {
			event.Device = connection.device.ID
			event.Epoch = connection.device.Epoch
			event.Timestamp = stamp
			connection.stream.emit(event)
		}); err != nil {
			connection.stream.fail(err)
			return
		}
		packet = unsafe.Add(packet, 12+uintptr(count)*4)
	}
}
