package liveplay

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// scoreMeterMap copies an engine's meter map before its score is published,
// so a swap on the render goroutine publishes it with one pointer store.
func scoreMeterMap(e *engine.Engine) *seq.MeterMap {
	m := e.MeterMap()
	return &m
}

// songMeterMap is the playing score's meter map. Any goroutine may call it.
func (p *Player) songMeterMap() seq.MeterMap { return *p.meterMap.Load() }

// barNumber is the one-based bar that holds tick.
func (p *Player) barNumber(tick int64) int64 {
	bar, _ := p.songMeterMap().BarAt(tick)
	return bar + 1
}

// storePosition publishes the one-based bar that holds tick and the one-based
// step within that bar.
func (p *Player) storePosition(tick int64) {
	bar, offset := p.songMeterMap().BarAt(tick)
	p.position.Store(uint64((bar+1)<<8 | offset/seq.TicksPerStep + 1))
}
