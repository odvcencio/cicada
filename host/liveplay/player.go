// Package liveplay streams the native engine to an audio device and lands
// validated score replacements on exact musical bar boundaries.
package liveplay

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync/atomic"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

const blockFrames = 256

type Score struct {
	Engine     *engine.Engine
	SampleRate int
	BPMMilli   int64
	Name       string
	SceneIDs   []string // engine scene indices in source order
}

type Event struct {
	Bar  int64 // one-based bar that has just begun
	Name string
	Kind string // edit, scene, or scene-error
}

// Position is the most recently rendered musical location. Step is one-based
// within a 16-step bar; the audio device may still be playing buffered frames.
type Position struct {
	Bar  int64 `json:"bar"`
	Step int64 `json:"step"`
}

// Player implements io.Reader for interleaved stereo float32 little-endian PCM.
// Read is owned by the audio device; Offer may be called from a file watcher.
type Player struct {
	current       Score
	previous      *engine.Engine
	clock         seq.Clock
	rate          int
	sample        int64
	bar           int64
	nextBarSample int64
	fadeTotal     int
	fadeRemaining int
	offers        chan Score
	launches      chan string
	events        chan Event
	left, right   [blockFrames]float32
	oldL, oldR    [blockFrames]float32
	pcm           [blockFrames * 8]byte
	buffered      int
	read          int
	fault         error
	position      atomic.Uint64
}

func New(initial Score, rate int) (*Player, error) {
	if initial.Engine == nil || initial.SampleRate != rate {
		return nil, fmt.Errorf("live score has no engine at %d Hz", rate)
	}
	clock, err := seq.NewClock(rate, initial.BPMMilli)
	if err != nil {
		return nil, err
	}
	if !initial.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		return nil, fmt.Errorf("live engine rejected play")
	}
	p := &Player{
		current: initial, clock: clock, rate: rate,
		nextBarSample: clock.SampleAtTick(seq.TicksPerBar),
		offers:        make(chan Score, 1), launches: make(chan string, 1), events: make(chan Event, 16),
	}
	p.position.Store(1<<8 | 1)
	return p, nil
}

// Offer replaces any edit that has not landed yet with the newest valid score.
func (p *Player) Offer(score Score) error {
	if score.Engine == nil || score.SampleRate != p.rate {
		return fmt.Errorf("live score has no engine at %d Hz", p.rate)
	}
	if _, err := seq.NewClock(p.rate, score.BPMMilli); err != nil {
		return err
	}
	for {
		select {
		case p.offers <- score:
			return nil
		default:
			select {
			case <-p.offers:
			default:
			}
		}
	}
}

func (p *Player) Events() <-chan Event { return p.events }

// LaunchScene keeps the newest requested scene. The audio reader resolves its
// name against the score active at the landing bar, so edits cannot stale an index.
func (p *Player) LaunchScene(name string) error {
	if name == "" {
		return fmt.Errorf("scene name is required")
	}
	for {
		select {
		case p.launches <- name:
			return nil
		default:
			select {
			case <-p.launches:
			default:
			}
		}
	}
}

// CancelScene discards a request that has not reached the audio reader.
func (p *Player) CancelScene() {
	select {
	case <-p.launches:
	default:
	}
}

// Position can be read safely by a UI thread while Read renders audio.
func (p *Player) Position() Position {
	packed := p.position.Load()
	return Position{Bar: int64(packed >> 8), Step: int64(packed & 0xff)}
}

func (p *Player) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	written := 0
	for written < len(out) {
		if p.read == p.buffered {
			if p.fault != nil {
				if written > 0 {
					return written, nil
				}
				return 0, p.fault
			}
			p.renderBlock()
			if p.fault != nil {
				continue
			}
		}
		n := copy(out[written:], p.pcm[p.read:p.buffered])
		written += n
		p.read += n
	}
	return written, nil
}

func (p *Player) renderBlock() {
	if p.sample == p.nextBarSample {
		p.bar++
		swapped := false
		select {
		case next := <-p.offers:
			if p.bar > int64(^uint32(0)) || !next.Engine.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(p.bar)}) || !next.Engine.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				p.fault = fmt.Errorf("live engine rejected bar %d", p.bar)
				return
			}
			p.previous = p.current.Engine
			p.fadeTotal = p.rate / 200 // five milliseconds
			p.fadeRemaining = p.fadeTotal
			p.current = next
			swapped = true
			p.clock = seq.Clock{
				SampleRate: int64(p.rate), BPMMilli: next.BPMMilli,
				AnchorSample: p.sample, AnchorTick: p.bar * seq.TicksPerBar,
			}
			select {
			case p.events <- Event{Bar: p.bar + 1, Name: next.Name, Kind: "edit"}:
			default:
			}
		default:
		}
		// A freshly swapped engine also receives Seek and Play at this bar.
		// Let those commands settle before a scene launch on the next bar.
		if !swapped {
			select {
			case name := <-p.launches:
				index := -1
				for i, candidate := range p.current.SceneIDs {
					if candidate == name {
						index = i
						break
					}
				}
				if index < 0 || index > int(^uint16(0)) {
					p.emit(Event{Bar: p.bar + 1, Name: name, Kind: "scene-error"})
				} else if !p.current.Engine.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: uint16(index), Arg0: 0}) {
					p.fault = fmt.Errorf("live engine rejected scene %q", name)
					return
				} else {
					p.emit(Event{Bar: p.bar + 1, Name: name, Kind: "scene"})
				}
			default:
			}
		}
		p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
	}
	frames := int(min(int64(blockFrames), p.nextBarSample-p.sample))
	if frames <= 0 {
		p.fault = fmt.Errorf("live transport did not advance at bar %d", p.bar+1)
		return
	}
	tick := p.clock.TickAtSample(p.sample)
	p.position.Store(uint64((tick/seq.TicksPerBar+1)<<8 | (tick%seq.TicksPerBar)/seq.TicksPerStep + 1))
	p.current.Engine.Render(p.left[:frames], p.right[:frames])
	var message cmd.Message
	for p.current.Engine.Poll(&message) {
		if message.Kind == cmd.Fault {
			p.fault = fmt.Errorf("live engine fault %d", message.A)
			return
		}
	}
	if p.fadeRemaining > 0 {
		fadeFrames := min(frames, p.fadeRemaining)
		p.previous.Render(p.oldL[:fadeFrames], p.oldR[:fadeFrames])
		for i := 0; i < fadeFrames; i++ {
			newGain := float32(p.fadeTotal-p.fadeRemaining+i+1) / float32(p.fadeTotal)
			oldGain := 1 - newGain
			p.left[i] = p.oldL[i]*oldGain + p.left[i]*newGain
			p.right[i] = p.oldR[i]*oldGain + p.right[i]*newGain
		}
		p.fadeRemaining -= fadeFrames
		if p.fadeRemaining == 0 {
			p.previous = nil
		}
	}
	for i := 0; i < frames; i++ {
		binary.LittleEndian.PutUint32(p.pcm[i*8:], math.Float32bits(p.left[i]))
		binary.LittleEndian.PutUint32(p.pcm[i*8+4:], math.Float32bits(p.right[i]))
	}
	p.sample += int64(frames)
	p.buffered, p.read = frames*8, 0
}

func (p *Player) emit(event Event) {
	select {
	case p.events <- event:
	default:
	}
}

var _ io.Reader = (*Player)(nil)
