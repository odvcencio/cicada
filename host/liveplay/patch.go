package liveplay

import (
	"math"
	"sync/atomic"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

const patchCapacity = 64

// Leave room for the largest note batch, all slot/scene launches, transport,
// and currently active overrides. Limit work even when the kernel is empty.
const patchBlockBudget = 2 * patchCapacity
const renderCommandReserve = 256 + 16 + 1 + 2

// PatchArena owns reusable batches. A successful Patch transfers ownership to
// Read; rejected batches return immediately. Builders must stop after transfer.
type PatchArena struct {
	patches  []Patch
	commands []cmd.Command
	free     chan *Patch
}

type Patch struct {
	revision uint64
	commands []cmd.Command
	targets  [patchCapacity]PreviewTarget
	n        int
	err      error
	arena    *PatchArena
	state    atomic.Uint32 // 0: free, 1: building, 2: queued/render-owned
}

func NewPatchArena(n int) *PatchArena {
	if n < 0 {
		n = 0
	}
	a := &PatchArena{patches: make([]Patch, n), commands: make([]cmd.Command, n*patchCapacity), free: make(chan *Patch, n)}
	for i := range a.patches {
		patch := &a.patches[i]
		patch.arena, patch.commands = a, a.commands[i*patchCapacity:(i+1)*patchCapacity]
		a.free <- patch
	}
	return a
}

func (a *PatchArena) Begin(revision uint64) *Patch {
	select {
	case patch := <-a.free:
		patch.revision, patch.n, patch.err = revision, 0, nil
		clear(patch.targets[:])
		patch.state.Store(1)
		return patch
	default:
		return nil
	}
}

func (a *PatchArena) Free() int { return len(a.free) }
func (patch *Patch) Err() error { return patch.err }

// Release returns a batch abandoned by its builder. Player.Patch releases
// rejected batches itself; a submitted batch belongs to the render thread.
func (patch *Patch) Release() {
	if patch != nil && patch.state.CompareAndSwap(1, 0) {
		patch.arena.free <- patch
	}
}

func (patch *Patch) recycle() {
	patch.state.Store(0)
	select {
	case patch.arena.free <- patch:
	default:
	}
}

func (patch *Patch) append(c cmd.Command, target PreviewTarget) {
	if patch.err != nil {
		return
	}
	if patch.state.Load() != 1 {
		patch.err = cmd.Error("patch is not owned by its builder")
		return
	}
	if patch.n == len(patch.commands) {
		patch.err = cmd.Error("patch command capacity exceeded")
		return
	}
	if err := c.Validate(16); err != nil {
		patch.err = err
		return
	}
	patch.commands[patch.n], patch.targets[patch.n] = c, target
	patch.n++
}

// SetParam accepts an index resolved at submission. Entity-based builders use
// SetResolvedParam to retain the incarnation captured before publication.
func (patch *Patch) SetParam(track uint8, id kernel.ParamID, value float32) {
	patch.append(cmd.Command{Op: cmd.OpSetParam, Track: track, Index: uint16(id), Arg0: math.Float32bits(value)}, PreviewTarget{})
}

func (patch *Patch) SetResolvedParam(target PreviewTarget, id kernel.ParamID, value float32) {
	if err := validatePatchParam(target.names, target.track, id, value); err != nil {
		patch.err = err
		return
	}
	patch.append(cmd.Command{Op: cmd.OpSetParam, Track: target.track, Index: uint16(id), Arg0: math.Float32bits(value)}, target)
}

func validatePatchParam(names *trackNameSnapshot, track uint8, id kernel.ParamID, value float32) error {
	spec, ok := kernel.Param(id)
	if !ok || !spec.Live {
		return cmd.Error("parameter is unknown or not live")
	}
	var validator *engine.PreviewParamValidator
	if spec.Scope == "global" {
		if track != 0xff {
			return cmd.Error("global parameter requires track 255")
		}
	} else {
		if names == nil || track >= names.count || !names.previewParams[track][id] {
			return cmd.Error("parameter is unsupported by the playing track")
		}
		validator = &names.previewValues[track]
	}
	return validatePreviewValue(spec, validator, value)
}

func (patch *Patch) Step(track, slot, index uint8, packed uint32) {
	patch.append(cmd.Command{Op: cmd.OpSetStep, Track: track, Index: uint16(index), Arg0: packed, Arg1: uint32(slot)}, PreviewTarget{})
}

func (patch *Patch) PatternLen(track, slot, length uint8) {
	patch.append(cmd.Command{Op: cmd.OpSetPatternLen, Track: track, Index: uint16(length), Arg1: uint32(slot)}, PreviewTarget{})
}

func (patch *Patch) PatternMeta(track, slot uint8, grid uint16, gate uint16, transpose int16) {
	patch.append(cmd.Command{Op: cmd.OpSetPatternMeta, Track: track, Index: grid, Arg0: uint32(gate) | uint32(uint16(transpose))<<16, Arg1: uint32(slot)}, PreviewTarget{})
}

func (patch *Patch) Chord(track, slot, index uint8, notes [4]uint8, count uint8) {
	c, err := cmd.ChordStepCommand(track, slot, index, notes, count)
	if err != nil {
		patch.err = err
		return
	}
	patch.append(c, PreviewTarget{})
}

// Patch publishes without waiting for audio. Success transfers batch ownership;
// all admission failures recycle it. No control path reads the playing engine.
func (p *Player) Patch(patch *Patch) error {
	if patch == nil || !patch.state.CompareAndSwap(1, 2) {
		return cmd.Error("patch is not owned by its builder")
	}
	p.patchMu.Lock()
	defer p.patchMu.Unlock()
	fail := func(err error) error { patch.recycle(); return err }
	if patch.n > min(patchBlockBudget, engine.CommandCapacity-renderCommandReserve) {
		return fail(cmd.Error("patch cannot fit beside render commands"))
	}
	select {
	case <-p.closed:
		return fail(cmd.Error("player is closed"))
	default:
	}
	if patch.err != nil {
		return fail(patch.err)
	}
	names := p.trackNames.Load()
	for i := 0; i < patch.n; i++ {
		c := patch.commands[i]
		target := patch.targets[i]
		if c.Track != 0xff {
			if target.names == nil {
				if names == nil || c.Track >= names.count {
					return fail(cmd.Error("patch track is out of range"))
				}
				target = PreviewTarget{player: p, names: names, track: c.Track}
				patch.targets[i] = target
			}
			if target.player != p {
				return fail(cmd.Error("patch target belongs to a different player"))
			}
		}
		if c.Op == cmd.OpSetParam {
			context := names
			if target.names != nil {
				context = target.names
			}
			if err := validatePatchParam(context, c.Track, kernel.ParamID(c.Index), math.Float32frombits(c.Arg0)); err != nil {
				return fail(err)
			}
		}
	}
	select {
	case p.patches <- patch:
		return nil
	default:
		return fail(cmd.Error("patch mailbox is full"))
	}
}

func (p *Player) LandedRevision() uint64 { return p.landedRevision.Load() }

func (p *Player) drainPatches() {
	reserve := renderCommandReserve
	active := 0
	if snapshot := p.overrides.Load(); snapshot != nil {
		for _, overrides := range snapshot.tracks {
			for id, override := range overrides.values {
				if override.active && override.version != overrides.state.clearedVersions[id] {
					active++
					if overrides.state.appliedVersions[id] != override.version {
						reserve++
					}
				}
			}
		}
	}
	// A patch may invalidate at most 64 applied previews, and cancellation
	// drains at most the captured queue length. Already applied previews do
	// not otherwise consume capacity, so a wide active gesture cannot starve edits.
	reserve += min(active, patchCapacity) + len(p.overrideClears)
	budget := min(patchBlockBudget, p.current.Engine.AvailableCommands()-reserve)
	remaining := len(p.patches)
	if p.deferredPatch != nil {
		remaining++
	}
	for ; remaining > 0; remaining-- {
		patch := p.deferredPatch
		if patch == nil {
			patch = <-p.patches
		}
		if patch.n > max(0, budget) {
			p.deferredPatch = patch
			return
		}
		p.deferredPatch = nil
		budget -= patch.n
		p.applyPatch(patch)
	}
}

func (p *Player) applyPatch(patch *Patch) {
	defer patch.recycle()
	names := p.trackNames.Load()
	for i := 0; i < patch.n; i++ {
		c := &patch.commands[i]
		if c.Track != 0xff {
			identity := patch.targets[i].names.overrideTrack(patch.targets[i].track)
			if !names.containsTrack(identity) {
				p.emit(Event{Kind: "patch-error", Name: "patch track incarnation is stale"})
				return
			}
			c.Track, _ = names.trackIndex(identity.id)
		}
		if c.Op == cmd.OpSetParam {
			if err := validatePatchParam(names, c.Track, kernel.ParamID(c.Index), math.Float32frombits(c.Arg0)); err != nil {
				p.emit(Event{Kind: "patch-error", Name: "patch parameter is incompatible with the playing voice"})
				return
			}
		}
		if err := c.Validate(names.count); err != nil {
			p.emit(Event{Kind: "patch-error", Name: "patch command is invalid"})
			return
		}
		if c.Op == cmd.OpSetStep || c.Op == cmd.OpSetChordStep || c.Op == cmd.OpSetPatternLen || c.Op == cmd.OpSetPatternMeta {
			c.Tick = (p.clock.TickAtSample(p.sample)/seq.TicksPerStep + 1) * seq.TicksPerStep
		}
	}
	if !p.current.Engine.PushCommittedBatch(patch.commands[:patch.n]) {
		p.emit(Event{Kind: "patch-error", Name: "patch could not be published"})
		return
	}
	for _, c := range patch.commands[:patch.n] {
		if c.Op == cmd.OpSetParam {
			id, value := kernel.ParamID(c.Index), math.Float32frombits(c.Arg0)
			names.defaults.Set(c.Track, id, value)
			p.blockParameters.Set(c.Track, id, value)
			p.recordCommitted(c.Track, id, value)
		}
	}
	// A semantic no-op can finish a gesture whose saved value was already
	// current. Retire through the same versioned method as a parameter patch.
	if snapshot := p.overrides.Load(); patch.n == 0 && snapshot != nil {
		for identity, overrides := range snapshot.tracks {
			track := uint8(0xff)
			if identity.id != "" {
				if !names.containsTrack(identity) {
					continue
				}
				track, _ = names.trackIndex(identity.id)
			}
			for id, override := range overrides.values {
				if value, ok := names.committedValue(track, kernel.ParamID(id)); override.active && ok && math.Float32bits(value) == math.Float32bits(override.value) {
					p.noteCommitted(track, kernel.ParamID(id), value)
				}
			}
		}
	}
	p.landedRevision.Store(patch.revision)
}

// The engine owns the scene traversal. Rebuild into scratch first so a
// matching earlier default cannot retire a preview of a different final value.
func (p *Player) rebuildCommitted(score Score, tick int64) {
	clear(p.reconstructed[:])
	score.Engine.ReconstructParameters(&score.trackNames.defaults, tick, func(setting engine.SceneSetting, _ bool) {
		if setting.Division == 0 {
			p.reconstructed.Set(setting.Track, setting.ID, setting.Value)
		}
	})
	names := score.trackNames
	for track := range names.committed {
		for id, bits := range p.reconstructed[track] {
			names.committed[track][id].Store(bits)
		}
	}
	if snapshot := p.overrides.Load(); snapshot != nil {
		for _, overrides := range snapshot.tracks {
			clear(overrides.state.appliedVersions[:])
		}
	}
}

func (p *Player) recordCommitted(track uint8, id kernel.ParamID, value float32) {
	names := p.trackNames.Load()
	names.storeCommitted(track, id, value)
	p.noteCommitted(track, id, value)
	if snapshot := p.overrides.Load(); snapshot != nil {
		if overrides := snapshot.tracks[names.overrideTrack(track)]; overrides != nil {
			overrides.state.appliedVersions[id] = 0 // Reapply any different active gesture.
		}
	}
}

func (p *Player) noteSceneCommitted(index int, blockTick int64) {
	p.current.Engine.VisitSceneParameters(uint16(index), func(parameter engine.SceneSetting, _ bool) {
		if parameter.Division != 0 {
			return
		}
		track := parameter.Track
		if track == 0xff {
			track = 16
		}
		// A same-tick patch follows scene launch in the kernel command order.
		// A later transition within this block supersedes the patch instead.
		if p.current.Engine.CurrentSceneTick() <= blockTick && p.blockParameters[track][parameter.ID]>>32 != 0 {
			return
		}
		p.recordCommitted(parameter.Track, parameter.ID, parameter.Value)
	})
}

func (p *Player) notePendingScenes(tick int64) {
	clear(p.reconstructed[:])
	p.current.Engine.VisitPendingSceneParameters(tick, func(setting engine.SceneSetting, _ bool) {
		if setting.Division == 0 {
			p.reconstructed.Set(setting.Track, setting.ID, setting.Value)
		}
	})
	p.reconstructed.Visit(func(setting engine.SceneSetting, _ bool) {
		track := setting.Track
		if track == 0xff {
			track = 16
		}
		if p.blockParameters[track][setting.ID]>>32 == 0 {
			p.recordCommitted(setting.Track, setting.ID, setting.Value)
		}
	})
}

// noteCommitted clears a matching preview after its committed value lands.
// Call it only on the render thread; the control snapshot stays immutable.
func (p *Player) noteCommitted(track uint8, id kernel.ParamID, value float32) {
	if id >= kernel.ParamCount {
		return
	}
	snapshot := p.overrides.Load()
	if snapshot == nil {
		return
	}
	identity := overrideTrack{}
	if track != 0xff {
		names := p.trackNames.Load()
		if names == nil || track >= names.count {
			return
		}
		identity = names.overrideTrack(track)
	}
	overrides := snapshot.tracks[identity]
	if overrides == nil {
		return
	}
	override := overrides.values[id]
	if override.active && math.Float32bits(value) == math.Float32bits(override.value) {
		overrides.state.retire(id, override.version)
	}
}

// retire runs only on the render thread. Its atomic mirror lets
// control callers observe retirement without mutating SetParam's snapshot.
func (state *overrideState) retire(id kernel.ParamID, version uint64) {
	state.clearedVersions[id] = version
	state.cancelledVersions[id] = 0
	state.retiredVersions[id].Store(version)
}
