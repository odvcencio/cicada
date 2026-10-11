// Package browsercontrol turns validated Studio input into the existing kernel
// command ABI. A GoSX browser instance sends these bytes directly to its own
// AudioWorklet port; this package has no network or native device path.
package browsercontrol

import (
	"errors"
	"fmt"
	"math"

	"m31labs.dev/cicada/host/demopolicy"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

var ErrNotPlaying = errors.New("CICADA-AUDIO: start browser audio and transport first")
var ErrUnsupported = errors.New("CICADA-NOTE: unsupported browser live voice")
var ErrResetRequired = errors.New("CICADA-AUDIO: wait for a fresh worklet kernel image after Stop")

const MaxHeldInputs = 64

type route struct {
	index uint8
	kind  engine.VoiceKind
}

type heldNote struct {
	track          string
	note, velocity int
	lane           uint16
	order          uint64
}

// Controller is serialized by one browser event loop. Construct it again when
// a score image changes, after Panic on the old instance. Sender posts bytes
// as {t:'c', bytes:Uint8Array} to this instance's AudioWorklet MessagePort.
type Controller struct {
	policy     demopolicy.Policy
	tracks     map[string]route
	params     map[string]project.ResolvedParam
	validators [16]engine.PreviewParamValidator
	count      uint8
	send       func([]byte) error
	held       map[string]heldNote
	blocked    map[string]bool
	order      uint64
	playing    bool
	needsReset bool
}

// New consumes the actual validated project and its compiled engine config.
// It copies routing and registry rows so later caller mutations cannot widen
// command permissions. Experimental expressive voices are not graph voices.
func New(p *project.Project, cfg engine.Config, policy demopolicy.Policy, send func([]byte) error) (*Controller, error) {
	if p == nil || send == nil || cfg.Tracks < 1 || cfg.Tracks > 16 || len(p.Tracks) != cfg.Tracks {
		return nil, errors.New("CICADA-AUDIO: compiled project and local worklet sender required")
	}
	c := &Controller{policy: policy, tracks: make(map[string]route), params: make(map[string]project.ResolvedParam), count: uint8(cfg.Tracks), send: send, held: make(map[string]heldNote), blocked: make(map[string]bool)}
	validators, err := engine.PrepareParamValidators(&cfg)
	if err != nil {
		return nil, fmt.Errorf("CICADA-PARAM: prepare validation: %w", err)
	}
	c.validators = validators
	for index, track := range p.Tracks {
		if track.ID == "" {
			return nil, errors.New("CICADA-AUDIO: empty track ID")
		}
		if _, exists := c.tracks[track.ID]; exists {
			return nil, errors.New("CICADA-AUDIO: duplicate track ID")
		}
		c.tracks[track.ID] = route{index: uint8(index), kind: cfg.Track[index].Kind}
	}
	for _, address := range project.ParamAddresses(p) {
		resolved, err := project.ResolveParameterPath(p, address.Address)
		if err == nil && resolved.Descriptor.Live {
			c.params[address.Address] = resolved
		}
	}
	return c, nil
}

func (c *Controller) emit(records ...cmd.Command) error {
	data := make([]byte, len(records)*cmd.CommandSize)
	for i, record := range records {
		encoded, err := cmd.EncodeCommand(record, c.count)
		if err != nil {
			return err
		}
		copy(data[i*cmd.CommandSize:], encoded[:])
	}
	return c.send(data)
}

func (c *Controller) Play() error {
	return c.policy.Dispatch(demopolicy.Play, func() error {
		if c.needsReset {
			return ErrResetRequired
		}
		if c.playing {
			return nil
		}
		if err := c.emit(cmd.Command{Op: cmd.OpPlay, Track: 255}); err != nil {
			return err
		}
		c.playing = true
		return nil
	})
}

// Panic stops transport and every voice. Call on Stop, blur, hidden document,
// MIDI disconnect, context suspension, score replacement and disposal. Inputs
// held at panic remain blocked until Up, so a repeated key cannot restart them.
// If posting fails, the host must disconnect/suspend the worklet immediately.
// After Stop the canonical worklet leaves queued kernel commands unprocessed.
// Reinitialize its image and confirm readiness before allowing Play again.
func (c *Controller) Panic() error {
	return c.policy.Dispatch(demopolicy.Stop, func() error {
		for token := range c.held {
			c.blocked[token] = true
		}
		clear(c.held)
		c.playing = false
		c.needsReset = true
		return c.emit(cmd.Command{Op: cmd.OpStop, Track: 255})
	})
}

// KernelResetReady is a host lifecycle acknowledgement, never an agent/UI
// command. Call only after a newly initialized kernel image becomes active
// while stopped (matching worklet t:'t' or a new node's t:'r'). An ordinary
// t:'s' Stop acknowledgement does not establish this boundary.
func (c *Controller) KernelResetReady() { c.needsReset = false }

func gmLane(note int) (uint16, bool) {
	// Match the existing Studio General MIDI input map, including aliases.
	switch note {
	case 36:
		return 0, true
	case 37:
		return 5, true
	case 38:
		return 1, true
	case 39:
		return 4, true
	case 41, 43:
		return 6, true
	case 42:
		return 2, true
	case 45, 47:
		return 7, true
	case 46:
		return 3, true
	case 48, 50:
		return 8, true
	case 49, 57:
		return 10, true
	case 56:
		return 9, true
	}
	return 0, false
}

func (c *Controller) noteCommand(note heldNote, on bool) cmd.Command {
	r := c.tracks[note.track]
	op := cmd.OpNoteOff
	arg := uint32(0)
	if on {
		op = cmd.OpNoteOn
		arg = uint32(note.note) | uint32(note.velocity)<<8
	}
	return cmd.Command{Op: op, Track: r.index, Index: note.lane, Arg0: arg}
}

// Down takes an input identity (keyboard code, pointer ID or device/channel/
// note tuple). Monophonic acid/graph tracks use last-held-note priority. Drum
// lanes remain independent. No sustain/bend/aftertouch or polyphony is claimed.
func (c *Controller) Down(token, track string, note, velocity int, repeat bool) error {
	return c.policy.Dispatch(demopolicy.Note, func() error {
		if token == "" || len(token) > 128 || note < 0 || note > 127 || velocity < 0 || velocity > 127 {
			return errors.New("CICADA-NOTE: invalid input identity, note or velocity")
		}
		if repeat || c.blocked[token] {
			return nil
		}
		if _, exists := c.held[token]; exists {
			return nil
		}
		if !c.playing {
			return ErrNotPlaying
		}
		r, exists := c.tracks[track]
		if !exists {
			return ErrUnsupported
		}
		n := heldNote{track: track, note: note, velocity: velocity}
		switch r.kind {
		case engine.VoiceAcid, engine.VoiceGraph:
		case engine.VoiceDrums:
			lane, ok := gmLane(note)
			if !ok {
				return ErrUnsupported
			}
			n.lane = lane
		default:
			return ErrUnsupported
		}
		if len(c.held)+len(c.blocked) >= MaxHeldInputs {
			return errors.New("CICADA-LIMIT: too many held inputs; release them first")
		}
		if err := c.emit(c.noteCommand(n, true)); err != nil {
			return err
		}
		c.order++
		n.order = c.order
		c.held[token] = n
		return nil
	})
}

func (c *Controller) Up(token string) error {
	return c.policy.Dispatch(demopolicy.Note, func() error {
		delete(c.blocked, token)
		note, exists := c.held[token]
		if !exists {
			return nil
		}
		var latest heldNote
		for otherToken, other := range c.held {
			if otherToken != token && other.track == note.track && (c.tracks[note.track].kind != engine.VoiceDrums || other.lane == note.lane) && other.order > latest.order {
				latest = other
			}
		}
		if latest.order > note.order || c.tracks[note.track].kind == engine.VoiceDrums && latest.order != 0 {
			delete(c.held, token)
			return nil
		}
		next, on := note, false
		if latest.order != 0 {
			next, on = latest, true
		}
		if err := c.emit(c.noteCommand(next, on)); err != nil {
			return err
		}
		delete(c.held, token)
		return nil
	})
}

// SetParam resolves only current compiled registry addresses and encodes float32
// values in their declared unit. nil means off only for descriptors allowing it.
func (c *Controller) SetParam(address string, value *float64) error {
	return c.policy.Dispatch(demopolicy.Parameter, func() error {
		param, exists := c.params[address]
		if !exists {
			return fmt.Errorf("CICADA-PARAM: unknown or unsupported live address %s", address)
		}
		var v float32
		if value == nil {
			if !param.Descriptor.Off {
				return errors.New("CICADA-PARAM: parameter does not accept off")
			}
			v = float32(math.Inf(-1))
		} else {
			v = float32(*value)
			if math.IsNaN(*value) || math.IsInf(*value, 0) || math.IsInf(float64(v), 0) {
				return errors.New("CICADA-PARAM: finite float32 value required")
			}
		}
		var global engine.PreviewParamValidator
		validator := &global
		if param.Track != 0xff {
			validator = &c.validators[param.Track]
		}
		if err := validator.Validate(param.ID, v); err != nil {
			return fmt.Errorf("CICADA-PARAM: %s: %w", address, err)
		}
		// Encode only after prepared validation; rejected controls send nothing.
		return c.emit(cmd.Command{Op: cmd.OpSetParam, Track: param.Track, Index: uint16(param.ID), Arg0: math.Float32bits(v)})
	})
}

func (c *Controller) SetMute(track string, on bool) error { return c.setSwitch(track+".mute", on) }
func (c *Controller) SetSolo(track string, on bool) error { return c.setSwitch(track+".solo", on) }

func (c *Controller) setSwitch(address string, on bool) error {
	value := float64(0)
	if on {
		value = 1
	}
	return c.SetParam(address, &value)
}
