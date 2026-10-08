package liveplay

import (
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/kernel/seq"
)

// telemetryState hands the latest transport position and meter levels from the
// render thread to the watcher goroutine. The render thread only stores atomic
// words under a sequence counter; it never allocates, locks or encodes.
type telemetryState struct {
	seq        atomic.Uint64 // odd while the render thread writes
	sample     atomic.Int64
	tick       atomic.Int64
	playing    atomic.Bool
	trackCount atomic.Uint32
	tracks     [16][2]atomic.Uint32 // peak, RMS as float32 bits
	written    atomic.Bool          // set once the render thread has stored a position
}

func (t *telemetryState) storeTransport(sample, tick int64, playing bool) {
	t.seq.Add(1)
	t.sample.Store(sample)
	t.tick.Store(tick)
	t.playing.Store(playing)
	t.written.Store(true)
	t.seq.Add(1)
}

func (t *telemetryState) storeMeters(f *MeterFrame) {
	t.seq.Add(1)
	t.trackCount.Store(uint32(f.TrackCount))
	for i := range t.tracks {
		t.tracks[i][0].Store(math.Float32bits(f.Tracks[i].Peak))
		t.tracks[i][1].Store(math.Float32bits(f.Tracks[i].RMS))
	}
	t.seq.Add(1)
}

// load returns a consistent snapshot, or ok=false when the render thread held
// the counter odd or changed it during every try. Callers skip the publish and
// keep the previous frame.
func (t *telemetryState) load() (sample, tick int64, playing bool, meters MetersFrame, haveMeters, haveTransport, ok bool) {
	for try := 0; try < 8; try++ {
		before := t.seq.Load()
		if before&1 != 0 {
			runtime.Gosched()
			continue
		}
		haveTransport = t.written.Load()
		sample, tick, playing = t.sample.Load(), t.tick.Load(), t.playing.Load()
		meters = MetersFrame{TrackCount: uint8(t.trackCount.Load())}
		for i := range t.tracks {
			p, r := math.Float32frombits(t.tracks[i][0].Load()), math.Float32frombits(t.tracks[i][1].Load())
			meters.Tracks[i] = TrackMeter{PeakL: p, PeakR: p, RMSL: r, RMSR: r}
		}
		if t.seq.Load() == before {
			return sample, tick, playing, meters, meters.TrackCount != 0, haveTransport, true
		}
		runtime.Gosched()
	}
	return 0, 0, false, MetersFrame{}, false, false, false
}

// SetPublisher attaches the publisher that PublishTelemetry feeds. Once it
// returns, no earlier PublishTelemetry call is still publishing, so passing
// nil retires the player's frames.
func (p *Player) SetPublisher(pub *Publisher) {
	p.publishMu.Lock()
	p.publisher = pub
	p.publishMu.Unlock()
}

// SetTransportPlaying records the host's play/pause state. Rendering stops
// while the host is paused, so the render thread cannot report it. A new
// player counts as playing.
func (p *Player) SetTransportPlaying(playing bool) { p.hostPaused.Store(!playing) }

// PublishTelemetry encodes the newest transport and meter state and publishes
// it. Call it from the watcher goroutine, never from the audio thread. It
// skips a frame it cannot read consistently, and publishes no transport frame
// before the first block renders.
func (p *Player) PublishTelemetry() {
	p.publishMu.Lock()
	defer p.publishMu.Unlock()
	pub := p.publisher
	if pub == nil {
		return
	}
	sample, tick, playing, meters, haveMeters, haveTransport, ok := p.telemetry.load()
	if !ok {
		return
	}
	if haveTransport {
		pub.PublishTransport(TransportFrame{
			SampleFrame: sample, SampleRate: uint32(p.rate), WallNanos: time.Now().UnixNano(),
			TempoMilli: uint32(p.tempoMilli.Load()), Playing: playing && !p.hostPaused.Load(),
			Bar: int32(tick/seq.TicksPerBar + 1), Beat: int32(tick%seq.TicksPerBar/seq.PPQ + 1), Tick: int32(tick % seq.PPQ),
		})
	}
	if haveMeters {
		loud := p.Loudness()
		meters.Master = MasterMeter{MomentaryLUFS: float32(loud.MomentaryLUFS), ShortTermLUFS: float32(loud.ShortTermLUFS), TruePeakDBTP: float32(loud.TruePeakDBTP)}
		pub.PublishMeters(meters)
	}
}
