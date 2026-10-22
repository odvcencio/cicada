package liveplay

import (
	"math"
	"runtime"
	"sync/atomic"
	"time"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// telemetryState hands the latest transport position and meter levels from the
// render thread to the watcher goroutine. The render thread only stores atomic
// words under a sequence counter; it never allocates, locks or encodes.
type telemetryState struct {
	seq        atomic.Uint64 // odd while the render thread writes
	sample     atomic.Int64
	tick       atomic.Int64
	wallNanos  atomic.Int64
	tempoMilli atomic.Int64
	playing    atomic.Bool
	trackCount atomic.Uint32
	tracks     [engine.MaxTracks][4]atomic.Uint32 // peak L, peak R, RMS L, RMS R as float32 bits
	compGR     atomic.Uint32                      // float32 bits, dB
	limiterGR  atomic.Uint32                      // float32 bits, dB
	over       atomic.Bool
	written    atomic.Bool // set once the render thread has stored a position
}

type telemetryTransport struct {
	sample, tick, wallNanos, tempoMilli int64
	playing                             bool
}

func (t *telemetryState) storeTransport(sample, tick, tempoMilli int64, playing bool) {
	observed := time.Now().UnixNano()
	t.seq.Add(1)
	t.sample.Store(sample)
	t.tick.Store(tick)
	t.wallNanos.Store(observed)
	t.tempoMilli.Store(tempoMilli)
	t.playing.Store(playing)
	t.written.Store(true)
	t.seq.Add(1)
}

func (t *telemetryState) storeMeters(f *MeterFrame) {
	t.seq.Add(1)
	t.trackCount.Store(uint32(f.TrackCount))
	for i := range t.tracks {
		track := &f.Tracks[i]
		t.tracks[i][0].Store(math.Float32bits(track.PeakL))
		t.tracks[i][1].Store(math.Float32bits(track.PeakR))
		t.tracks[i][2].Store(math.Float32bits(track.RMSL))
		t.tracks[i][3].Store(math.Float32bits(track.RMSR))
	}
	t.compGR.Store(math.Float32bits(max(0, f.CompGR)))
	t.limiterGR.Store(math.Float32bits(max(0, f.LimiterGR)))
	t.over.Store(f.PreLimiterOver())
	t.seq.Add(1)
}

// load returns a consistent snapshot, or ok=false when the render thread held
// the counter odd or changed it during every try. Callers skip the publish and
// keep the previous frame.
func (t *telemetryState) load() (transport telemetryTransport, meters MetersFrame, haveMeters, haveTransport, ok bool) {
	for try := 0; try < 8; try++ {
		before := t.seq.Load()
		if before&1 != 0 {
			runtime.Gosched()
			continue
		}
		haveTransport = t.written.Load()
		transport = telemetryTransport{sample: t.sample.Load(), tick: t.tick.Load(), wallNanos: t.wallNanos.Load(), tempoMilli: t.tempoMilli.Load(), playing: t.playing.Load()}
		meters = MetersFrame{TrackCount: uint8(t.trackCount.Load())}
		for i := range t.tracks {
			meters.Tracks[i] = TrackMeter{
				PeakL: math.Float32frombits(t.tracks[i][0].Load()), PeakR: math.Float32frombits(t.tracks[i][1].Load()),
				RMSL: math.Float32frombits(t.tracks[i][2].Load()), RMSR: math.Float32frombits(t.tracks[i][3].Load()),
			}
		}
		meters.Master = MasterMeter{CompGRDB: math.Float32frombits(t.compGR.Load()), LimiterGRDB: math.Float32frombits(t.limiterGR.Load()), PreLimiterOver: t.over.Load()}
		if t.seq.Load() == before {
			return transport, meters, meters.TrackCount != 0, haveTransport, true
		}
		runtime.Gosched()
	}
	return telemetryTransport{}, MetersFrame{}, false, false, false
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
	transport, meters, haveMeters, haveTransport, ok := p.telemetry.load()
	if !ok {
		return
	}
	if haveTransport {
		tick := transport.tick
		pub.PublishTransport(TransportFrame{
			SampleFrame: transport.sample, SampleRate: uint32(p.rate), WallNanos: transport.wallNanos,
			TempoMilli: uint32(transport.tempoMilli), Playing: transport.playing && !p.hostPaused.Load(),
			Bar: int32(tick/seq.TicksPerBar + 1), Beat: int32(tick%seq.TicksPerBar/seq.PPQ + 1), Tick: int32(tick % seq.PPQ),
		})
	}
	if haveMeters {
		loud := p.Loudness()
		meters.Master.MomentaryLUFS, meters.Master.ShortTermLUFS, meters.Master.TruePeakDBTP = float32(loud.MomentaryLUFS), float32(loud.ShortTermLUFS), float32(loud.TruePeakDBTP)
		pub.PublishMeters(meters)
	}
}
