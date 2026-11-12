package liveplay

import (
	"runtime"
	"sync/atomic"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// scoreMeterMap copies an engine's meter map before its score is published,
// so a swap on the render goroutine publishes it with one pointer store.
func scoreMeterMap(e *engine.Engine) *seq.MeterMap {
	m := e.MeterMap()
	return &m
}

// positionCell publishes the transport position together with the meter map
// that placed it, so a reader never decodes one score's bar and step on
// another score's map. Only the render goroutine writes (New writes once
// before it starts); any goroutine reads. A write is two stores between two
// increments of writes, and a reader retries while writes is odd or changed:
// no lock and no allocation on the render goroutine.
type positionCell struct {
	writes   atomic.Uint64
	position atomic.Uint64 // one-based bar << 8 | one-based step
	meterMap atomic.Pointer[seq.MeterMap]
}

func (c *positionCell) store(position uint64, meterMap *seq.MeterMap) {
	c.writes.Add(1)
	c.position.Store(position)
	c.meterMap.Store(meterMap)
	c.writes.Add(1)
}

func (c *positionCell) load() (uint64, *seq.MeterMap) {
	for {
		before := c.writes.Load()
		position, meterMap := c.position.Load(), c.meterMap.Load()
		if before&1 == 0 && c.writes.Load() == before {
			return position, meterMap
		}
		runtime.Gosched()
	}
}

func unpackPosition(packed uint64) Position {
	return Position{Bar: int64(packed >> 8), Step: int64(packed & 0xff)}
}

// songMeterMap is the published score's meter map. Any goroutine may call it.
// Code on the render goroutine reads p.current.meterMap instead: a swapped-in
// score's map is published with the next position, later in the same block.
func (p *Player) songMeterMap() seq.MeterMap {
	_, meterMap := p.published.load()
	return *meterMap
}

// MeterMap is the meter map of the score playing now, the one that places the
// bar and beat of each transport frame. Any goroutine may call it.
func (p *Player) MeterMap() seq.MeterMap { return p.songMeterMap() }

// PositionWithMap returns the position and the meter map that placed it,
// read together. Any goroutine may call it. Use it to turn the bar and step
// into a tick: MeterMap and Position read separately can straddle a swap.
func (p *Player) PositionWithMap() (Position, seq.MeterMap) {
	packed, meterMap := p.published.load()
	return unpackPosition(packed), *meterMap
}

// barNumber is the one-based bar that holds tick on the current score's map.
// Render goroutine only.
func (p *Player) barNumber(tick int64) int64 {
	bar, _ := p.current.meterMap.BarAt(tick)
	return bar + 1
}

// storePosition publishes the one-based bar that holds tick and the one-based
// step within that bar, with the current score's map. Render goroutine only.
func (p *Player) storePosition(tick int64) {
	bar, offset := p.current.meterMap.BarAt(tick)
	p.published.store(uint64((bar+1)<<8|offset/seq.TicksPerStep+1), p.current.meterMap)
}
