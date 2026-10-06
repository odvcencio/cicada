package director

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/cmd"
)

// Send must enqueue the record or return an error. It must not call Engine.Push
// concurrently with Render. The game and audio thread own the queue between them.
type Send func(cmd.Command) error

type Client struct {
	surface Surface
	send    Send
	state   string
	layers  uint32
}

func New(surface Surface, send Send) (*Client, error) {
	if err := surface.Validate(); err != nil {
		return nil, err
	}
	if send == nil {
		return nil, fmt.Errorf("director requires a command sender")
	}
	surface.Macros = append([]Macro(nil), surface.Macros...)
	surface.States = append([]State(nil), surface.States...)
	surface.Stingers = append([]Stinger(nil), surface.Stingers...)
	surface.Transitions = append([]Transition(nil), surface.Transitions...)
	surface.Setup = append([]cmd.Command(nil), surface.Setup...)
	return &Client{surface: surface, send: send, layers: (1 << surface.Tracks) - 1}, nil
}

// Setup installs score macros, layers and phrase length before Play.
func (c *Client) Setup() error {
	for _, v := range c.surface.Setup {
		if err := c.send(v); err != nil {
			return err
		}
	}
	return nil
}

// SetState uses the last landed state to select a transition. Tick=0 means now;
// positive ticks are absolute transport ticks (960 per quarter note).
func (c *Client) SetState(name string, tick int64) error {
	for _, v := range c.surface.States {
		if v.Name == name {
			qname := c.surface.Land
			if qname == "" {
				qname = "bar"
			}
			frames := uint32(0)
			for _, t := range c.surface.Transitions {
				if t.From == c.state && t.To == name {
					qname, frames = t.Quantize, t.CrossfadeFrames
					break
				}
			}
			q, err := Quantize(qname, c.surface.PhraseBars)
			if err != nil {
				return err
			}
			return c.command(cmd.Command{Op: cmd.OpSetState, Track: 255, Index: v.ID, Arg0: uint32(v.Scene) | q<<16, Arg1: frames, Tick: tick})
		}
	}
	return fmt.Errorf("unknown director state %q", name)
}

func (c *Client) SetMacro(name string, value float32, tick int64) error {
	for _, v := range c.surface.Macros {
		if v.Name == name {
			return c.command(cmd.Command{Op: cmd.OpSetMacro, Track: 255, Index: v.ID, Arg0: math.Float32bits(value), Arg1: v.SmoothFrames, Tick: tick})
		}
	}
	return fmt.Errorf("unknown director macro %q", name)
}
func (c *Client) TriggerStinger(name string, tick int64) error {
	for _, v := range c.surface.Stingers {
		if v.Name == name {
			q, err := Quantize(v.Quantize, c.surface.PhraseBars)
			if err != nil {
				return err
			}
			return c.command(cmd.Command{Op: cmd.OpTriggerStinger, Track: v.Track, Index: v.Slot, Arg0: q, Arg1: v.CrossfadeFrames, Tick: tick})
		}
	}
	return fmt.Errorf("unknown director stinger %q", name)
}
func (c *Client) command(v cmd.Command) error {
	if err := v.Validate(c.surface.Tracks); err != nil {
		return err
	}
	return c.send(v)
}

// Handle updates the observed state only after the kernel reports a landing.
// Games should forward every drained message, including faults and late events.
func (c *Client) Handle(m cmd.Message) {
	switch m.Kind {
	case cmd.StateChanged:
		for _, v := range c.surface.States {
			if v.ID == m.A && uint32(v.Scene) == m.B {
				c.state = v.Name
				break
			}
		}
	case cmd.LayerChanged:
		c.layers = m.B
	}
}
func (c *Client) State() string     { return c.state }
func (c *Client) LayerMask() uint32 { return c.layers }
