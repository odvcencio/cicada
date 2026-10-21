package liveplay

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel/cmd"
)

type liveControlGroup struct {
	Owner, Track string
	Controls     cmd.Command
	Dirty        bool
}

// BindNoteGroupOwner joins a held, owned note to a separate shared layer.
// The binding never changes the note's own controls or its gate.
func (p *Player) BindNoteGroupOwner(track string, noteID uint16, owner, group string) error {
	if noteID == 0 || noteID == 65535 || owner == "" || len(owner) > 1024 {
		return fmt.Errorf("invalid live group note owner")
	}
	if err := p.validateLiveGroup(track, group); err != nil {
		return err
	}
	return p.queueOwnedNote(noteInput{ID: owner, Track: track, NoteID: noteID, Group: group, BindGroup: true}, p.noteEpoch.Load())
}

// GroupExpressionOwner keeps shared controls addressable while voices release.
// Only an existing binding can admit controls; a snapshot cannot create one.
func (p *Player) GroupExpressionOwner(track, owner string, pitch, pressure, timbre float32) error {
	if err := p.validateLiveGroup(track, owner); err != nil {
		return err
	}
	c := cmd.Command{Op: cmd.OpSetLiveGroup, Index: 1, Arg0: math.Float32bits(pitch), Arg1: math.Float32bits(pressure), Pad: math.Float32bits(timbre)}
	if err := c.Validate(1); err != nil {
		return err
	}
	return p.queueOwnedNote(noteInput{Track: track, Group: owner, PitchCents: pitch, Pressure: pressure, Timbre: timbre}, p.noteEpoch.Load())
}

func (p *Player) validateLiveGroup(track, owner string) error {
	if owner == "" || len(owner) > 1024 {
		return fmt.Errorf("invalid live group owner")
	}
	_, kind, found := findTrack(p.trackNames.Load(), track)
	if !found || kind != "acid" && kind != "graph" && kind != "poly" {
		return fmt.Errorf("track %q does not support shared expression", track)
	}
	return nil
}

func (p *Player) pruneLiveGroups() {
	for i := range p.liveGroups[:p.liveGroupLimit] {
		group := &p.liveGroups[i]
		group.Dirty = false
		if group.Controls.Op != 0 && !p.current.Engine.LiveGroupActive(group.Controls.Track, uint16(i+1)) {
			*group = liveControlGroup{}
		}
	}
	for p.liveGroupLimit > 0 && p.liveGroups[p.liveGroupLimit-1].Controls.Op == 0 {
		p.liveGroupLimit--
	}
}

func (p *Player) queueLiveGroup(track uint8, input noteInput) {
	var held *noteInput
	if input.BindGroup {
		for i := range p.heldNotes {
			note := &p.heldNotes[i]
			if p.heldOrder[i] != 0 && note.ID == input.ID && note.Track == input.Track && note.NoteID == input.NoteID {
				held = note
				break
			}
		}
		if held == nil {
			return
		}
	}
	index, free := -1, -1
	for i := range p.liveGroups[:p.liveGroupLimit] {
		group := &p.liveGroups[i]
		if group.Owner == input.Group && group.Track == input.Track {
			index = i
			break
		}
		if free < 0 && group.Controls.Op == 0 {
			free = i
		}
	}
	if index < 0 {
		if free < 0 && p.liveGroupLimit < len(p.liveGroups) {
			free = p.liveGroupLimit
		}
		if held == nil || free < 0 {
			return
		}
		index = free
		p.liveGroupLimit = max(p.liveGroupLimit, index+1)
		p.liveGroups[index] = liveControlGroup{Owner: input.Group, Track: input.Track, Controls: cmd.Command{Op: cmd.OpSetLiveGroup, Track: track, Index: uint16(index + 1), Pad: math.Float32bits(.5)}}
	}
	group := &p.liveGroups[index]
	group.Dirty = true
	if held != nil {
		p.addOwnedFinal(cmd.Command{Op: cmd.OpBindNoteGroup, Track: track, Index: held.KernelID, Arg0: uint32(index + 1)})
	} else {
		group.Controls.Arg0, group.Controls.Arg1, group.Controls.Pad = math.Float32bits(input.PitchCents), math.Float32bits(input.Pressure), math.Float32bits(input.Timbre)
	}
}
