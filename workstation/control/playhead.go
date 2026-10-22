package control

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"time"

	"m31labs.dev/cicada/kernel/seq"
)

// PlaybackState consumes the small native transport projection. The binary
// layout is TransportFrame v1 in host/liveplay/telemetry.go; keep native audio
// dependencies out of the browser module.
type PlaybackState struct {
	MIDIClockEpoch uint64        `json:"midiClockEpoch"`
	FrameAgeNanos  *int64        `json:"frameAgeNanos,omitempty"`
	RoundTrip      time.Duration `json:"-"` // Measured by the client; not a host clock.
	Playing        bool          `json:"playing"`
	Bar            int64         `json:"bar"`
	Step           int64         `json:"step"`
	Frames         struct {
		Transport string `json:"transport"`
	} `json:"frames"`
	Range     *PlaybackRange `json:"listening"`
	Streaming *struct {
		ArenaBytes    int64  `json:"arenaBytes"`
		MissingFrames uint64 `json:"missingFrames"`
		ReadErrors    uint64 `json:"readErrors"`
		Error         string `json:"error"`
	} `json:"streaming"`
}

type PlaybackRange struct {
	Start  int64  `json:"startTick"`
	End    int64  `json:"endTick"`
	Loop   bool   `json:"loop"`
	Cycles uint64 `json:"cycles"`
	Name   string `json:"name"`
	Mode   string `json:"mode"`
}

type Playhead struct {
	Tick, Tempo int64
	Playing     bool
	Range       *PlaybackRange
	Age         time.Duration // Render age at receipt, including estimated response transit.
}

func (s PlaybackState) Playhead() Playhead {
	p := Playhead{Tick: max(0, s.Bar-1)*seq.TicksPerBar + max(0, s.Step-1)*seq.TicksPerStep, Playing: s.Playing, Range: s.Range}
	if s.FrameAgeNanos != nil {
		p.Age = time.Duration(*s.FrameAgeNanos)
		if p.Age >= 0 {
			// Half the measured round trip estimates return transit without
			// assuming the browser and server have synchronized wall clocks.
			p.Age = min(p.Age, time.Hour) + max(0, min(s.RoundTrip/2, time.Hour))
		}
	}
	b, err := base64.StdEncoding.DecodeString(s.Frames.Transport)
	if err != nil || len(b) != 64 || b[0] != 1 || b[1] != 1 {
		return p
	}
	bar := int64(int32(binary.LittleEndian.Uint32(b[52:])))
	beat := int64(int32(binary.LittleEndian.Uint32(b[56:])))
	tick := int64(int32(binary.LittleEndian.Uint32(b[60:])))
	tempo := int64(binary.LittleEndian.Uint32(b[22:]))
	if bar < 1 || beat < 1 || beat > 4 || tick < 0 || tick >= seq.PPQ || tempo < 20000 || tempo > 300000 {
		return p
	}
	p.Tick, p.Tempo = (bar-1)*seq.TicksPerBar+(beat-1)*seq.PPQ+tick, tempo
	return p
}

// Extrapolate only a short interval, so a lost connection never runs the
// cursor ahead of the audio. The native playing flag overrides stale frames.
func (p Playhead) At(elapsed time.Duration) float64 {
	return p.at(elapsed, 350*time.Millisecond)
}

// ClockFresh distinguishes an old render from a responsive HTTP connection.
// A stopped position remains useful without a running audio clock.
func (p Playhead) ClockFresh(elapsed, limit time.Duration) bool {
	return !p.Playing || p.Age >= 0 && p.Age < limit && max(0, elapsed) < limit-p.Age
}

func (p Playhead) at(elapsed, limit time.Duration) float64 {
	tick := float64(p.Tick)
	if p.Playing && p.Age >= 0 {
		age := min(p.Age, limit)
		total := age + min(max(0, elapsed), limit-age)
		tick += total.Seconds() * float64(p.Tempo) * float64(seq.PPQ) / 60000
	}
	if r := p.Range; r != nil && r.End > r.Start {
		if r.Loop && tick >= float64(r.End) {
			tick = float64(r.Start) + math.Mod(tick-float64(r.Start), float64(r.End-r.Start))
		} else {
			tick = min(tick, float64(r.End))
		}
	}
	return tick
}

// NoteTick uses the audio clock's sub-step position and current tempo. Older
// hosts without transport frames fall back to the score tempo. Stop advancing
// after 250 ms without a fresh sample, preserving the recording clock's bound.
func (p Playhead) NoteTick(elapsed time.Duration, fallbackBPM float64) int64 {
	if p.Tempo == 0 && fallbackBPM >= 20 && fallbackBPM <= 300 {
		p.Tempo = int64(math.Round(fallbackBPM * 1000))
	}
	return int64(math.Round(p.at(elapsed, 250*time.Millisecond)))
}
