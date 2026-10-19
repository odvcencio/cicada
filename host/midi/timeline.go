package midi

import (
	"math"
	"time"

	"m31labs.dev/cicada/kernel/seq"
)

// Timeline correlates recent native transport observations with the CoreMIDI
// uptime clock. Callers serialize access; no audio callback uses this history.
type Timeline struct {
	epoch       uint64
	started     int64
	frames      [64]clockFrame
	count, next int
	cachedAt    int64
	cached      Position
}

type clockFrame struct{ at, tick, tempo int64 }

func (t *Timeline) Epoch() uint64 { return t.epoch }

func (t *Timeline) Reset(at int64) {
	t.epoch++
	if t.epoch == 0 {
		t.epoch = 1
	}
	t.started, t.count, t.next, t.cachedAt = at, 0, 0, 0
}

// Observe returns false when playback moved backwards. The host starts a new
// epoch before admitting more input, including when a playback range wraps.
func (t *Timeline) Observe(at, tick, tempo int64) bool {
	if at <= 0 || tick < 0 || tempo < 20000 || tempo > 300000 {
		return true
	}
	if t.count > 0 {
		last := t.frames[(t.next+len(t.frames)-1)%len(t.frames)]
		if at <= last.at {
			return true
		}
		if tick < last.tick {
			return false
		}
	}
	t.frames[t.next] = clockFrame{at, tick, tempo}
	t.next = (t.next + 1) % len(t.frames)
	t.count = min(t.count+1, len(t.frames))
	return true
}

// PositionAt uses the event time, never its HTTP or UI delivery time. Cache a
// packet's position so all UMP messages sharing its timestamp stay together
// even if another transport observation arrives while the packet is drained.
func (t *Timeline) PositionAt(at int64, playing bool) Position {
	p := Position{Epoch: t.epoch, Playing: playing}
	if at <= 0 {
		return p
	}
	if at < t.started {
		p.Stale = true
		return p
	}
	if at == t.cachedAt {
		p = t.cached
		p.Playing = playing
		return p
	}
	for i := 0; i < t.count; i++ {
		f := t.frames[(t.next+len(t.frames)-1-i)%len(t.frames)]
		if f.at > at {
			continue
		}
		elapsed := at - f.at
		if elapsed > int64(250*time.Millisecond) {
			break
		}
		advance := float64(elapsed) * float64(f.tempo) * float64(seq.PPQ) / float64(time.Minute*1000)
		if advance > float64(math.MaxInt64-f.tick) {
			break
		}
		p.Tick, p.Valid = f.tick+int64(math.Round(advance)), true
		break
	}
	t.cachedAt, t.cached = at, p
	return p
}

// NoteTiming retains source timing independently of pattern quantization.
// EndHostNanos is zero after a forced release without a native input timestamp.
type NoteTiming struct {
	HostNanos    int64  `json:"hostNanos"`
	EndHostNanos int64  `json:"endHostNanos"`
	ClockEpoch   uint64 `json:"clockEpoch"`
	Source       string `json:"source"`
	EndSource    string `json:"endSource,omitempty"`
	Estimated    bool   `json:"estimated,omitempty"`
}
