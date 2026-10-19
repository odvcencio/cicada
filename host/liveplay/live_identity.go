package liveplay

import (
	"io"
	"math"

	"m31labs.dev/cicada/kernel/cmd"
)

type ownedNoteCommands struct{ gate, controls cmd.Command }

func (p *Player) allocateLiveIdentity() uint16 {
	for {
		p.nextLiveIdentity++
		if p.nextLiveIdentity == 0 || p.nextLiveIdentity == 65535 {
			p.nextLiveIdentity = 1
		}
		used := false
		for i := range p.heldNotes {
			if p.heldOrder[i] != 0 && p.heldNotes[i].KernelID == p.nextLiveIdentity {
				used = true
				break
			}
		}
		if !used {
			return p.nextLiveIdentity
		}
	}
}

func liveExpressionCommand(track uint8, note *noteInput) cmd.Command {
	return cmd.Command{Op: cmd.OpNoteExpression, Track: track, Index: note.KernelID, Arg0: math.Float32bits(note.PitchCents), Arg1: math.Float32bits(note.Pressure), Pad: math.Float32bits(note.Timbre)}
}

// Reduce gates and controls by the engine identity, never by pitch. A press
// followed by release within one read must not reopen when the kernel sorts
// same-tick releases before presses. Expression bursts keep their final value.
func (p *Player) addOwnedFinal(c cmd.Command) {
	i := 0
	for ; i < p.ownedFinalCount; i++ {
		previous := p.ownedFinal[i].gate
		if previous.Op == 0 {
			previous = p.ownedFinal[i].controls
		}
		if previous.Track == c.Track && previous.Index == c.Index {
			break
		}
	}
	if i == p.ownedFinalCount {
		if i == len(p.ownedFinal) {
			p.fault = io.ErrShortBuffer
			return
		}
		p.ownedFinal[i] = ownedNoteCommands{}
		p.ownedFinalCount++
	}
	if c.Op == cmd.OpNoteExpression {
		p.ownedFinal[i].controls = c
	} else {
		p.ownedFinal[i].gate = c
	}
}

func (p *Player) flushOwnedFinal() {
	for i := 0; i < p.ownedFinalCount; i++ {
		entry := &p.ownedFinal[i]
		if entry.gate.Op != 0 && !p.current.Engine.Push(entry.gate) {
			p.fault = io.ErrShortBuffer
		}
		if entry.gate.Op != cmd.OpNoteOff && entry.controls.Op != 0 && !p.current.Engine.Push(entry.controls) {
			p.fault = io.ErrShortBuffer
		}
	}
}
