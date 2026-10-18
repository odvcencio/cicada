package main

import (
	"context"
	_ "embed"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"m31labs.dev/gosx/desktop"
	"m31labs.dev/gosx/desktop/bridge"
)

//go:embed testdata/midi-bootstrap.js
var nativeMIDIBootstrap string

const midiUTF8 = 0x08000100

type coreMIDIAPI struct {
	clientCreate     func(uintptr, uintptr, uintptr, *uint32) int32
	clientDispose    func(uint32) int32
	inputPortCreate  func(uint32, uintptr, uintptr, uintptr, *uint32) int32
	portDispose      func(uint32) int32
	connectSource    func(uint32, uint32, uintptr) int32
	disconnectSource func(uint32, uint32) int32
	sourceCount      func() uintptr
	source           func(uintptr) uint32
	integerProperty  func(uint32, uintptr, *int32) int32
	stringProperty   func(uint32, uintptr, *uintptr) int32
	stringCreate     func(uintptr, string, uint32) uintptr
	stringCString    func(uintptr, *byte, int64, uint32) bool
	release          func(uintptr)
	sourceCreate     func(uint32, uintptr, *uint32) int32
	endpointDispose  func(uint32) int32
	received         func(uint32, unsafe.Pointer) int32
	packetListInit   func(unsafe.Pointer) unsafe.Pointer
	packetListAdd    func(unsafe.Pointer, uintptr, unsafe.Pointer, uint64, uintptr, *byte) unsafe.Pointer
}

var coreMIDILibrary struct {
	sync.Once
	api *coreMIDIAPI
	err error
}

func loadCoreMIDI() (*coreMIDIAPI, error) {
	coreMIDILibrary.Do(func() {
		// Framework handles live for this process, as do purego's C callbacks.
		midi, err := purego.Dlopen("/System/Library/Frameworks/CoreMIDI.framework/CoreMIDI", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			coreMIDILibrary.err = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			coreMIDILibrary.err = err
			return
		}
		a := &coreMIDIAPI{}
		for _, binding := range []struct {
			fn   any
			name string
		}{
			{&a.clientCreate, "MIDIClientCreate"}, {&a.clientDispose, "MIDIClientDispose"},
			{&a.inputPortCreate, "MIDIInputPortCreate"}, {&a.portDispose, "MIDIPortDispose"},
			{&a.connectSource, "MIDIPortConnectSource"}, {&a.disconnectSource, "MIDIPortDisconnectSource"},
			{&a.sourceCount, "MIDIGetNumberOfSources"}, {&a.source, "MIDIGetSource"},
			{&a.integerProperty, "MIDIObjectGetIntegerProperty"}, {&a.stringProperty, "MIDIObjectGetStringProperty"},
			{&a.sourceCreate, "MIDISourceCreate"}, {&a.endpointDispose, "MIDIEndpointDispose"},
			{&a.received, "MIDIReceived"}, {&a.packetListInit, "MIDIPacketListInit"}, {&a.packetListAdd, "MIDIPacketListAdd"},
		} {
			symbol, err := purego.Dlsym(midi, binding.name)
			if err != nil {
				coreMIDILibrary.err = err
				return
			}
			purego.RegisterFunc(binding.fn, symbol)
		}
		purego.RegisterLibFunc(&a.stringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&a.stringCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&a.release, cf, "CFRelease")
		coreMIDILibrary.api = a
	})
	return coreMIDILibrary.api, coreMIDILibrary.err
}

type nativeMIDIInput struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
}

type nativeMIDIMessage struct {
	ID   string `json:"id"`
	Data []int  `json:"data"`
}

type nativeMIDIPacket struct {
	source uint32
	length uint16
	data   [256]byte
}

type nativeMIDI struct {
	mu           sync.Mutex
	api          *coreMIDIAPI
	client, port uint32
	readCallback uintptr
	inputs       map[uint32]nativeMIDIInput
	acceptSource func(uint32) bool // Native tests select only their own virtual source.
	packets      chan nativeMIDIPacket
	alerts       chan struct{}
	overflow     atomic.Bool
	closed       atomic.Bool
	cancel       context.CancelFunc
	emit         func(string, any) error
}

// attachNativeMIDI installs input-only CoreMIDI after an explicit Enable MIDI.
// Existing browser MIDI stays in charge where requestMIDIAccess is available.
func attachNativeMIDI(app *desktop.App) (func(), error) {
	m := newNativeMIDI(app.Bridge().Emit)
	if err := app.Bridge().Register("cicada.midi.open", func(ctx *bridge.Context) error {
		var request struct {
			Sysex bool `json:"sysex"`
		}
		if err := ctx.Decode(&request); err != nil {
			return err
		}
		if request.Sysex {
			return fmt.Errorf("system-exclusive MIDI access is unavailable")
		}
		inputs, err := m.open()
		if err != nil {
			return err
		}
		return ctx.Respond(map[string]any{"inputs": inputs})
	}); err != nil {
		return nil, err
	}
	if err := app.PrependBootstrapScript(nativeMIDIBootstrap); err != nil {
		app.Bridge().Unregister("cicada.midi.open")
		return nil, err
	}
	return func() {
		app.Bridge().Unregister("cicada.midi.open")
		m.close()
	}, nil
}

func newNativeMIDI(emit func(string, any) error) *nativeMIDI {
	return &nativeMIDI{packets: make(chan nativeMIDIPacket, 512), alerts: make(chan struct{}, 1), inputs: make(map[uint32]nativeMIDIInput), emit: emit}
}

func midiStatus(operation string, status int32) error {
	if status != 0 {
		return fmt.Errorf("CoreMIDI %s failed (%d)", operation, status)
	}
	return nil
}

func (m *nativeMIDI) open() ([]nativeMIDIInput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed.Load() {
		return nil, fmt.Errorf("MIDI input has closed")
	}
	if m.client == 0 {
		a, err := loadCoreMIDI()
		if err != nil {
			return nil, err
		}
		m.api = a
		name := a.stringCreate(0, "Cicada Studio MIDI input", midiUTF8)
		defer a.release(name)
		if err := midiStatus("create client", a.clientCreate(name, 0, 0, &m.client)); err != nil {
			return nil, err
		}
		if m.readCallback == 0 {
			m.readCallback = purego.NewCallback(func(packets unsafe.Pointer, _ uintptr, source uintptr) { m.receive(packets, uint32(source)) })
		}
		if err := midiStatus("create input port", a.inputPortCreate(m.client, name, m.readCallback, 0, &m.port)); err != nil {
			a.clientDispose(m.client)
			m.client = 0
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		m.cancel = cancel
		go m.run(ctx)
	}
	return m.refreshLocked(), nil
}

func (a *coreMIDIAPI) property(source uint32, key string) string {
	name := a.stringCreate(0, key, midiUTF8)
	defer a.release(name)
	var value uintptr
	if a.stringProperty(source, name, &value) != 0 || value == 0 {
		return ""
	}
	defer a.release(value)
	var buffer [1024]byte
	if !a.stringCString(value, &buffer[0], int64(len(buffer)), midiUTF8) {
		return ""
	}
	for i, b := range buffer {
		if b == 0 {
			return string(buffer[:i])
		}
	}
	return ""
}

func (m *nativeMIDI) refreshLocked() []nativeMIDIInput {
	found := make(map[uint32]bool)
	if m.closed.Load() || m.client == 0 {
		return nil
	}
	a := m.api
	uidName := a.stringCreate(0, "uniqueID", midiUTF8)
	defer a.release(uidName)
	for i, count := uintptr(0), min(a.sourceCount(), 128); i < count; i++ {
		source := a.source(i)
		if source == 0 || m.acceptSource != nil && !m.acceptSource(source) {
			continue
		}
		found[source] = true
		if _, connected := m.inputs[source]; connected {
			continue
		}
		var uid int32
		if a.integerProperty(source, uidName, &uid) != 0 || uid == 0 {
			uid = int32(source)
		}
		name := a.property(source, "displayName")
		if name == "" {
			name = a.property(source, "name")
		}
		if name == "" {
			name = "MIDI input"
		}
		if a.connectSource(m.port, source, uintptr(source)) != 0 {
			continue
		}
		m.inputs[source] = nativeMIDIInput{ID: "coremidi:" + strconv.FormatInt(int64(uid), 10), Name: name, Manufacturer: a.property(source, "manufacturer")}
	}
	for source := range m.inputs {
		if !found[source] {
			a.disconnectSource(m.port, source)
			delete(m.inputs, source)
		}
	}
	inputs := make([]nativeMIDIInput, 0, len(m.inputs))
	for _, input := range m.inputs {
		inputs = append(inputs, input)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].ID < inputs[j].ID })
	return inputs
}

// CoreMIDI owns this high-priority callback. It copies at most 16 KiB into
// fixed-size nonblocking slots; parsing, JSON and UI delivery stay in run.
func (m *nativeMIDI) receive(list unsafe.Pointer, source uint32) {
	if m.closed.Load() || list == nil {
		return
	}
	count := *(*uint32)(list)
	offset, budget := 4, 16<<10 // SDK MIDIPacketList uses #pragma pack(4).
	if count > 128 {
		m.markOverflow()
		return
	}
	for range count {
		length := int(*(*uint16)(unsafe.Add(list, offset+8)))
		if length > budget {
			m.markOverflow()
			return
		}
		budget -= length
		data := unsafe.Slice((*byte)(unsafe.Add(list, offset+10)), length)
		for len(data) > 0 {
			chunk := nativeMIDIPacket{source: source, length: uint16(min(len(data), 256))}
			copy(chunk.data[:], data[:chunk.length])
			select {
			case m.packets <- chunk:
			default:
				m.markOverflow()
				return
			}
			data = data[chunk.length:]
		}
		offset += 10 + length
		if runtime.GOARCH == "arm64" {
			offset = (offset + 3) &^ 3
		}
	}
}

func (m *nativeMIDI) markOverflow() {
	m.overflow.Store(true)
	select {
	case m.alerts <- struct{}{}:
	default:
	}
}

func (m *nativeMIDI) run(ctx context.Context) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	decoders := make(map[uint32]*midiDecoder)
	lastInputs := ""
	for {
		if m.overflow.Swap(false) {
			for range len(m.packets) {
				<-m.packets
			}
			clear(decoders)
			if !m.closed.Load() {
				_ = m.emit("cicada.midi.reset", nil)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-m.alerts:
			continue
		case <-ticker.C:
			m.mu.Lock()
			inputs := m.refreshLocked()
			for source := range decoders {
				if _, ok := m.inputs[source]; !ok {
					delete(decoders, source)
				}
			}
			m.mu.Unlock()
			identity := fmt.Sprint(inputs)
			if identity != lastInputs && !m.closed.Load() {
				lastInputs = identity
				_ = m.emit("cicada.midi.state", map[string]any{"inputs": inputs})
			}
		case packet := <-m.packets:
			m.mu.Lock()
			input, connected := m.inputs[packet.source]
			m.mu.Unlock()
			if !connected || m.closed.Load() {
				continue
			}
			decoder := decoders[packet.source]
			if decoder == nil {
				decoder = &midiDecoder{}
				decoders[packet.source] = decoder
			}
			messages := make([]nativeMIDIMessage, 0, 16)
			releaseNotes := false
			decoder.read(packet.data[:packet.length], func(data []int) {
				messages = append(messages, nativeMIDIMessage{ID: input.ID, Data: data})
				releaseNotes = releaseNotes || len(data) == 1 && data[0] == 0xff || len(data) == 3 && data[0]&0xf0 == 0xb0 && (data[1] == 120 || data[1] == 123)
			})
			if len(messages) > 0 {
				_ = m.emit("cicada.midi.messages", map[string]any{"messages": messages})
			}
			if releaseNotes {
				_ = m.emit("cicada.midi.reset", nil)
			}
		}
	}
}

func (m *nativeMIDI) close() {
	if m.closed.Swap(true) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.port != 0 {
		m.api.portDispose(m.port)
		m.port = 0
	}
	if m.client != 0 {
		m.api.clientDispose(m.client)
		m.client = 0
	}
	clear(m.inputs)
}

// MIDI 1.0 running status and real-time interleaving can span native packets.
// System-exclusive bytes never leave the host, even between packet boundaries.
type midiDecoder struct {
	status      byte
	count, need int
	data        [3]int
	sysex       bool
}

func (d *midiDecoder) read(data []byte, emit func([]int)) {
	for _, b := range data {
		if b >= 0xf8 {
			emit([]int{int(b)})
			if b == 0xff {
				*d = midiDecoder{}
			}
			continue
		}
		if b&0x80 != 0 {
			d.status, d.count, d.need = 0, 0, 0
			if b == 0xf0 {
				d.sysex = true
				continue
			}
			if b == 0xf7 {
				d.sysex = false
				continue
			}
			d.sysex = false
			switch {
			case b < 0xf0:
				d.need = 3
				if b&0xf0 == 0xc0 || b&0xf0 == 0xd0 {
					d.need = 2
				}
			case b == 0xf1 || b == 0xf3:
				d.need = 2
			case b == 0xf2:
				d.need = 3
			case b == 0xf6:
				emit([]int{int(b)})
				continue
			default:
				continue
			}
			d.status, d.data[0], d.count = b, int(b), 1
			continue
		}
		if d.sysex || d.status == 0 {
			continue
		}
		d.data[d.count] = int(b)
		d.count++
		if d.count == d.need {
			message := make([]int, d.need)
			copy(message, d.data[:d.need])
			emit(message)
			if d.status < 0xf0 {
				d.count = 1
			} else {
				d.status, d.count = 0, 0
			}
		}
	}
}
