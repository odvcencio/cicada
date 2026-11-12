package project

import "m31labs.dev/cicada/kernel/seq"

// MeterMap places the song's bar and beat lines. A score cannot declare a
// meter yet, so every song is 4/4: the zero map. Bar counts, positions and
// tempo steps go through it so a declared meter changes them in one place.
func (p *Project) MeterMap() seq.MeterMap { return seq.MeterMap{} }

// SongEndTick is where the song section ends: the bar line after its last
// entry. Song entries count bars of the base meter.
func (p *Project) SongEndTick() int64 {
	var bars int64
	for _, entry := range p.Song {
		bars += int64(entry.Bars)
	}
	return p.MeterMap().BarStart(bars)
}
