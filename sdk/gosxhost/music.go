package gosxhost

import (
	"errors"
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/sdk/director"
)

// Sender posts a batch of 24-byte command records to the kernel. With GoSX it
// is (*kernel.Node).Send. It must report an error if it cannot enqueue the
// whole batch.
type Sender func(batch []byte) error

// IntensityMacro is the score macro SetIntensity drives. A score declares it
// in its live block: macro intensity = 0.3 smooth 400ms.
const IntensityMacro = "intensity"

// batchRecords bounds one post. The kernel command region holds 512 records;
// half keeps a batch clear of commands already queued in the same block.
const batchRecords = 256

// Music drives a score's scenes and intensity. Scene changes and stingers land
// on the musical boundary the score names (beat, bar or phrase), not when the
// call is made, so a game can call them from any frame.
//
// A Music value is not safe for concurrent use. Call its methods and
// HandleMessages from one goroutine, which the GoSX host guarantees for its
// message callback.
type Music struct {
	client *director.Client
	send   Sender
	tracks uint8
	batch  []byte
	err    error

	scene   string
	bar     uint32
	faults  []uint16
	started bool
	// layers is the last track mask the kernel reported.
	layers uint32
}

// NewMusic builds a controller for pack. The first command is sent by Start.
func NewMusic(pack *Pack, send Sender) (*Music, error) {
	if pack == nil || send == nil {
		return nil, errors.New("gosxhost: NewMusic needs a pack and a sender")
	}
	m := &Music{send: send, tracks: pack.Surface.Tracks}
	client, err := director.New(pack.Surface, m.enqueue)
	if err != nil {
		return nil, err
	}
	m.client = client
	return m, nil
}

func (m *Music) enqueue(c cmd.Command) error {
	rec, err := cmd.EncodeCommand(c, m.tracks)
	if err != nil {
		return err
	}
	m.batch = append(m.batch, rec[:]...)
	return nil
}

// flush posts the queued records in bounded batches.
func (m *Music) flush() error {
	batch := m.batch
	m.batch = m.batch[:0]
	for len(batch) > 0 {
		n := min(len(batch), batchRecords*cmd.CommandSize)
		if err := m.send(batch[:n]); err != nil {
			return err
		}
		batch = batch[n:]
	}
	return nil
}

func (m *Music) do(fn func() error) error {
	m.batch = m.batch[:0]
	if err := fn(); err != nil {
		m.batch = m.batch[:0]
		return err
	}
	return m.flush()
}

// Start installs the score's macros and layers, requests the opening scene,
// and starts the transport. Call it after the host reports the kernel ready.
func (m *Music) Start(scene string) error {
	return m.do(func() error {
		if err := m.client.Setup(); err != nil {
			return err
		}
		if err := m.client.SetState(scene, 0); err != nil {
			return err
		}
		if err := m.enqueue(cmd.Command{Op: cmd.OpPlay, Track: 255}); err != nil {
			return err
		}
		m.started = true
		return nil
	})
}

// Stop halts the transport. Start begins again from the top of the score.
func (m *Music) Stop() error {
	return m.do(func() error {
		m.started = false
		return m.enqueue(cmd.Command{Op: cmd.OpStop, Track: 255})
	})
}

// SetScene asks for a game state by name, for example "explore" or "combat".
// The change lands on the boundary the score's transition rule names, with the
// crossfade it names. Until it lands, Scene still reports the old scene.
func (m *Music) SetScene(name string) error {
	return m.do(func() error { return m.client.SetState(name, 0) })
}

// SetIntensity sets the score's intensity macro to v in [0, 1]. The score
// smooths the change over the time its macro declares and may add or drop
// layers at thresholds.
func (m *Music) SetIntensity(v float64) error {
	if math.IsNaN(v) || v < 0 || v > 1 {
		return fmt.Errorf("gosxhost: intensity %v is outside [0, 1]", v)
	}
	return m.do(func() error { return m.client.SetMacro(IntensityMacro, float32(v), 0) })
}

// SetMacro sets any named score macro to v in [0, 1].
func (m *Music) SetMacro(name string, v float64) error {
	if math.IsNaN(v) || v < 0 || v > 1 {
		return fmt.Errorf("gosxhost: macro %q value %v is outside [0, 1]", name, v)
	}
	return m.do(func() error { return m.client.SetMacro(name, float32(v), 0) })
}

// Stinger plays a one-shot cue the score declares, quantized as the score says.
func (m *Music) Stinger(name string) error {
	return m.do(func() error { return m.client.TriggerStinger(name, 0) })
}

// HandleMessages consumes a batch of 16-byte kernel message records, as the
// GoSX host delivers them. It updates Scene, Bar and Faults. Forward every
// batch, including faults.
func (m *Music) HandleMessages(records []byte) error {
	if len(records)%cmd.MessageSize != 0 {
		return fmt.Errorf("gosxhost: %d bytes is not a whole number of messages", len(records))
	}
	for at := 0; at < len(records); at += cmd.MessageSize {
		msg, err := cmd.DecodeMessage(records[at : at+cmd.MessageSize])
		if err != nil {
			return err
		}
		m.client.Handle(msg)
		switch msg.Kind {
		case cmd.Bar:
			// B is the one-based bar the kernel's meter map placed this line at.
			if msg.B > 0 {
				m.bar = msg.B - 1
			}
		case cmd.Fault:
			m.faults = append(m.faults, msg.A)
		case cmd.LayerChanged:
			m.layers = msg.B
		}
	}
	m.scene = m.client.State()
	return nil
}

// Scene is the scene the kernel last reported landing. It is empty before the
// first landing.
func (m *Music) Scene() string { return m.scene }

// Bar is the last bar line the kernel reported, counted from the start.
func (m *Music) Bar() uint32 { return m.bar }

// Layers is the track mask the kernel last reported.
func (m *Music) Layers() uint32 { return m.layers }

// Faults lists the fault codes the kernel reported.
func (m *Music) Faults() []uint16 { return append([]uint16(nil), m.faults...) }

// Started reports whether Start has run and Stop has not.
func (m *Music) Started() bool { return m.started }
