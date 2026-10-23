package liveplay

import (
	"fmt"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/musictime"
)

// PlaybackRange uses exact, half-open musical ticks. It does not change the
// score or mixer. A host can offer a filtered score to audition one placement.
type PlaybackRange struct {
	StartTick int64 `json:"startTick"`
	EndTick   int64 `json:"endTick"`
	Loop      bool  `json:"loop"`
}

type playbackRangeRequest struct{ value *PlaybackRange }

var clearPlaybackRangeRequest playbackRangeRequest

func (r PlaybackRange) Validate() error {
	if r.StartTick < 0 || r.EndTick <= r.StartTick || r.EndTick > (int64(^uint32(0))+1)*seq.TicksPerBar {
		return fmt.Errorf("listening range must have a nonnegative start and a later end within transport bounds")
	}
	return nil
}

// PlayRange starts at the range's first tick on the next complete stereo frame.
// Requests and prepared score offers are consumed by the audio reader without
// locks or allocations; a newest request supersedes an older one.
func (p *Player) PlayRange(r PlaybackRange) error {
	if err := r.Validate(); err != nil {
		return err
	}
	p.CancelStart()
	p.rangeFinished.Store(false)
	p.rangeCycles.Store(0)
	p.rangeState.Store(&r)
	p.rangeRequest.Store(&playbackRangeRequest{value: &r})
	return nil
}

// ClearRange lets the current song continue from its current tick.
func (p *Player) ClearRange() {
	p.rangeState.Store(nil)
	p.rangeRequest.Store(&clearPlaybackRangeRequest)
}

func (p *Player) PlaybackRange() *PlaybackRange { return p.rangeState.Load() }
func (p *Player) RangeFinished() bool           { return p.rangeFinished.Load() }
func (p *Player) RangeCycles() uint64           { return p.rangeCycles.Load() }

func (p *Player) setPlaybackRange(r *PlaybackRange) {
	p.playbackRange = r
	p.rangeState.Store(r)
	p.rangeFinished.Store(false)
}

func (p *Player) beginRequestedRange() {
	if p.read%8 != 0 {
		return
	}
	if request := p.rangeRequest.Swap(nil); request != nil {
		p.setPlaybackRange(request.value)
		if request.value != nil {
			p.seekListeningRange(request.value.StartTick)
		}
	}
}

func (p *Player) seekListeningRange(tick int64) {
	p.read, p.buffered = 0, 0
	p.launches.Store(nil)
	p.slotLaunches.Swap(nil)
	if p.previous != nil {
		p.retireEngine(p.previous)
	}
	p.previous, p.fadeRemaining = nil, 0
	// A short loop may never cross a bar. Take the latest valid edit at its
	// restart as well as at ordinary bar boundaries.
	select {
	case next := <-p.offers:
		if next.Range != nil {
			tick = next.Range.StartTick
		}
		p.retireEngine(p.current.Engine)
		p.discardSupersededPatches(next.publication)
		// Same order as a song-entry seek: rebuild the replacement's committed
		// values at the restart tick before its previews are reapplied.
		p.rebuildCommitted(next, tick)
		p.applyOverrides(next.Engine, next, true)
		p.current = next
		p.streamEngine.Store(next.Engine)
		p.trackCount.Store(uint32(next.Engine.TrackCount()))
		p.trackNames.Store(next.trackNames)
		p.resetMeters()
		if next.Range != nil {
			p.setPlaybackRange(next.Range)
		}
		p.emit(Event{Bar: tick/seq.TicksPerBar + 1, Name: next.Name, Kind: "edit", Revision: next.Revision})
	default:
	}
	p.heldNotes, p.heldOrder = [128]noteInput{}, [128]uint64{}
	commands := [2]cmd.Command{{Op: cmd.OpSeek, Track: 0xff, Arg0: uint32(tick / seq.TicksPerBar), Arg1: uint32(tick % seq.TicksPerBar)}, {Op: cmd.OpPlay, Track: 0xff}}
	if !p.current.Engine.PushBatch(commands[:]) {
		p.fault = fmt.Errorf("live engine rejected listening start at tick %d", tick)
		return
	}
	// The seek reconstructs saved defaults and scene settings in the engine.
	// Mirror it in the committed snapshot, drop the override cache, and let
	// the reconstruction precede any patch drained in the next block.
	p.rebuildCommitted(p.current, tick)
	p.transportQueued = true
	p.initialParameters = false
	p.bar = tick / seq.TicksPerBar
	p.clock = seq.Clock{SampleRate: int64(p.rate), BPMMilli: p.current.Engine.TempoAtTick(tick), AnchorSample: p.sample, AnchorTick: tick}
	p.tempoMilli.Store(p.clock.BPMMilli)
	p.nextBarSample = p.clock.SampleAtTick((p.bar + 1) * seq.TicksPerBar)
	p.position.Store(uint64((tick/seq.TicksPerBar+1)<<8 | (tick%seq.TicksPerBar)/seq.TicksPerStep + 1))
	p.jumpFadeRemaining = p.rate / 200
}

// Split the final render block at the range end. Once-through audition fills
// subsequent reads with silence until the host pauses its device; no samples
// beyond the selected end are rendered. Looping reanchors to the exact start.
func (p *Player) finishPlaybackRange() bool {
	r := p.playbackRange
	if r == nil || p.sample < p.clock.SampleAtTick(r.EndTick) {
		return false
	}
	if r.Loop {
		p.seekListeningRange(r.StartTick)
		p.rangeCycles.Add(1)
		return false
	}
	p.rangeFinished.Store(true)
	p.hostPaused.Store(true)
	p.position.Store(uint64((r.EndTick/seq.TicksPerBar+1)<<8 | (r.EndTick%seq.TicksPerBar)/seq.TicksPerStep + 1))
	p.telemetry.storeTransport(p.sample, r.EndTick, p.clock.BPMMilli, false, musictime.ObservedTempoStep{})
	clear(p.pcm[:])
	p.buffered, p.read = len(p.pcm), 0
	return true
}
