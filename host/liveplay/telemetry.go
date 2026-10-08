package liveplay

import (
	"encoding/binary"
	"fmt"
	"math"
	"sync"
)

// FrameVersion is the first byte of every telemetry frame.
const FrameVersion = 1

// FrameKind is the second byte of every telemetry frame.
type FrameKind uint8

const (
	FrameTransport FrameKind = 1
	FrameMeters    FrameKind = 2
	FrameHealth    FrameKind = 3
)

const (
	TransportFrameSize = 64
	MetersFrameSize    = 2 + 1 + 16*16 + 12
	HealthFrameSize    = 18
)

var le = binary.LittleEndian

// TransportFrame is the playback position. Bar and Beat are 1-based. Tick is
// the 0-based tick within the beat (0 to 959). LoopStart, LoopEnd and
// InsertMarker are absolute ticks.
type TransportFrame struct {
	SampleFrame                      int64
	SampleRate                       uint32
	WallNanos                        int64
	TempoMilli                       uint32
	Playing, Recording               bool
	LoopStart, LoopEnd, InsertMarker int64
	Bar, Beat, Tick                  int32
}

// TrackMeter holds per-track stereo peak and RMS levels.
type TrackMeter struct{ PeakL, PeakR, RMSL, RMSR float32 }

// MasterMeter holds master loudness readings.
type MasterMeter struct{ MomentaryLUFS, ShortTermLUFS, TruePeakDBTP float32 }

// MetersFrame carries up to 16 track meters and the master meter.
type MetersFrame struct {
	TrackCount uint8
	Tracks     [16]TrackMeter
	Master     MasterMeter
}

// HealthFrame reports engine load and device health.
type HealthFrame struct {
	CPULoad      float32
	Dropouts     uint32
	LatencyNanos int64
}

func header(dst []byte, kind FrameKind) {
	dst[0], dst[1] = FrameVersion, byte(kind)
}

func checkHeader(src []byte, kind FrameKind, size int) error {
	if len(src) < size {
		return fmt.Errorf("telemetry frame has %d bytes, want %d", len(src), size)
	}
	if src[0] != FrameVersion {
		return fmt.Errorf("telemetry frame version %d, want %d", src[0], FrameVersion)
	}
	if src[1] != byte(kind) {
		return fmt.Errorf("telemetry frame kind %d, want %d", src[1], kind)
	}
	return nil
}

func putBool(b *byte, v bool) {
	if v {
		*b = 1
	} else {
		*b = 0
	}
}

func putF32(dst []byte, v float32) { le.PutUint32(dst, math.Float32bits(v)) }
func getF32(src []byte) float32    { return math.Float32frombits(le.Uint32(src)) }

// Encode writes the frame into dst and returns its size, or 0 if dst is short.
func (f TransportFrame) Encode(dst []byte) int {
	if len(dst) < TransportFrameSize {
		return 0
	}
	header(dst, FrameTransport)
	le.PutUint64(dst[2:], uint64(f.SampleFrame))
	le.PutUint32(dst[10:], f.SampleRate)
	le.PutUint64(dst[14:], uint64(f.WallNanos))
	le.PutUint32(dst[22:], f.TempoMilli)
	putBool(&dst[26], f.Playing)
	putBool(&dst[27], f.Recording)
	le.PutUint64(dst[28:], uint64(f.LoopStart))
	le.PutUint64(dst[36:], uint64(f.LoopEnd))
	le.PutUint64(dst[44:], uint64(f.InsertMarker))
	le.PutUint32(dst[52:], uint32(f.Bar))
	le.PutUint32(dst[56:], uint32(f.Beat))
	le.PutUint32(dst[60:], uint32(f.Tick))
	return TransportFrameSize
}

// DecodeTransportFrame parses a transport frame.
func DecodeTransportFrame(src []byte) (TransportFrame, error) {
	if err := checkHeader(src, FrameTransport, TransportFrameSize); err != nil {
		return TransportFrame{}, err
	}
	return TransportFrame{
		SampleFrame: int64(le.Uint64(src[2:])), SampleRate: le.Uint32(src[10:]),
		WallNanos: int64(le.Uint64(src[14:])), TempoMilli: le.Uint32(src[22:]),
		Playing: src[26] != 0, Recording: src[27] != 0,
		LoopStart: int64(le.Uint64(src[28:])), LoopEnd: int64(le.Uint64(src[36:])), InsertMarker: int64(le.Uint64(src[44:])),
		Bar: int32(le.Uint32(src[52:])), Beat: int32(le.Uint32(src[56:])), Tick: int32(le.Uint32(src[60:])),
	}, nil
}

// Encode writes the frame into dst and returns its size, or 0 if dst is short.
func (f MetersFrame) Encode(dst []byte) int {
	if len(dst) < MetersFrameSize {
		return 0
	}
	header(dst, FrameMeters)
	dst[2] = f.TrackCount
	for i, t := range f.Tracks {
		o := 3 + i*16
		putF32(dst[o:], t.PeakL)
		putF32(dst[o+4:], t.PeakR)
		putF32(dst[o+8:], t.RMSL)
		putF32(dst[o+12:], t.RMSR)
	}
	o := 3 + 16*16
	putF32(dst[o:], f.Master.MomentaryLUFS)
	putF32(dst[o+4:], f.Master.ShortTermLUFS)
	putF32(dst[o+8:], f.Master.TruePeakDBTP)
	return MetersFrameSize
}

// DecodeMetersFrame parses a meters frame.
func DecodeMetersFrame(src []byte) (MetersFrame, error) {
	if err := checkHeader(src, FrameMeters, MetersFrameSize); err != nil {
		return MetersFrame{}, err
	}
	if src[2] > 16 {
		return MetersFrame{}, fmt.Errorf("telemetry meters frame has %d tracks, max 16", src[2])
	}
	f := MetersFrame{TrackCount: src[2]}
	for i := range f.Tracks {
		o := 3 + i*16
		f.Tracks[i] = TrackMeter{PeakL: getF32(src[o:]), PeakR: getF32(src[o+4:]), RMSL: getF32(src[o+8:]), RMSR: getF32(src[o+12:])}
	}
	o := 3 + 16*16
	f.Master = MasterMeter{MomentaryLUFS: getF32(src[o:]), ShortTermLUFS: getF32(src[o+4:]), TruePeakDBTP: getF32(src[o+8:])}
	return f, nil
}

// Encode writes the frame into dst and returns its size, or 0 if dst is short.
func (f HealthFrame) Encode(dst []byte) int {
	if len(dst) < HealthFrameSize {
		return 0
	}
	header(dst, FrameHealth)
	putF32(dst[2:], f.CPULoad)
	le.PutUint32(dst[6:], f.Dropouts)
	le.PutUint64(dst[10:], uint64(f.LatencyNanos))
	return HealthFrameSize
}

// DecodeHealthFrame parses a health frame.
func DecodeHealthFrame(src []byte) (HealthFrame, error) {
	if err := checkHeader(src, FrameHealth, HealthFrameSize); err != nil {
		return HealthFrame{}, err
	}
	return HealthFrame{CPULoad: getF32(src[2:]), Dropouts: le.Uint32(src[6:]), LatencyNanos: int64(le.Uint64(src[10:]))}, nil
}

// Subscriber receives the kind of each newly published frame. A slow
// subscriber loses intermediate notifications, never the latest frame.
type Subscriber struct{ C chan FrameKind }

// Publisher stores the latest frame of each kind and notifies subscribers.
// It runs on the watcher goroutine, never on the audio thread.
type Publisher struct {
	mu     sync.Mutex
	latest [4][]byte
	subs   map[*Subscriber]struct{}
}

// NewPublisher returns an empty publisher.
func NewPublisher() *Publisher { return &Publisher{subs: map[*Subscriber]struct{}{}} }

// Subscribe registers a subscriber whose channel holds buffer notifications.
func (p *Publisher) Subscribe(buffer int) *Subscriber {
	s := &Subscriber{C: make(chan FrameKind, max(buffer, 1))}
	p.mu.Lock()
	p.subs[s] = struct{}{}
	p.mu.Unlock()
	return s
}

// Unsubscribe removes a subscriber.
func (p *Publisher) Unsubscribe(s *Subscriber) {
	p.mu.Lock()
	delete(p.subs, s)
	p.mu.Unlock()
}

func (p *Publisher) publish(kind FrameKind, frame []byte) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.latest[kind] = frame
	for s := range p.subs {
		select {
		case s.C <- kind:
		default:
		}
	}
	p.mu.Unlock()
}

// PublishTransport stores and announces a transport frame.
func (p *Publisher) PublishTransport(f TransportFrame) {
	b := make([]byte, TransportFrameSize)
	f.Encode(b)
	p.publish(FrameTransport, b)
}

// PublishMeters stores and announces a meters frame.
func (p *Publisher) PublishMeters(f MetersFrame) {
	b := make([]byte, MetersFrameSize)
	f.Encode(b)
	p.publish(FrameMeters, b)
}

// PublishHealth stores and announces a health frame.
func (p *Publisher) PublishHealth(f HealthFrame) {
	b := make([]byte, HealthFrameSize)
	f.Encode(b)
	p.publish(FrameHealth, b)
}

// Latest returns copies of the newest frame of each kind, indexed by
// FrameKind. A kind that was never published is nil.
func (p *Publisher) Latest() [4][]byte {
	if p == nil {
		return [4][]byte{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var out [4][]byte
	for i, b := range p.latest {
		if b != nil {
			out[i] = append([]byte(nil), b...)
		}
	}
	return out
}
