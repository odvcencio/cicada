package liveplay

import "testing"

func TestTelemetryFramesRoundTripAndEncodeWithoutAllocating(t *testing.T) {
	transport := TransportFrame{SampleFrame: 123456, SampleRate: 48000, WallNanos: 99, TempoMilli: 120000, Playing: true, LoopStart: 3840, LoopEnd: 7680, InsertMarker: 960, Bar: 2, Beat: 3, Tick: 480}
	var buf [TransportFrameSize]byte
	if n := transport.Encode(buf[:]); n != TransportFrameSize {
		t.Fatalf("encoded %d bytes", n)
	}
	decoded, err := DecodeTransportFrame(buf[:])
	if err != nil || decoded != transport {
		t.Fatalf("round trip: %+v %v", decoded, err)
	}
	if buf[0] != FrameVersion || buf[1] != byte(FrameTransport) {
		t.Fatalf("header %v", buf[:2])
	}
	meters := MetersFrame{TrackCount: 2}
	meters.Tracks[1] = TrackMeter{PeakL: .5, PeakR: .25, RMSL: .1, RMSR: .05}
	meters.Master = MasterMeter{MomentaryLUFS: -14, ShortTermLUFS: -15, TruePeakDBTP: -1}
	var mbuf [MetersFrameSize]byte
	meters.Encode(mbuf[:])
	if got, err := DecodeMetersFrame(mbuf[:]); err != nil || got != meters {
		t.Fatalf("meters round trip: %+v %v", got, err)
	}
	health := HealthFrame{CPULoad: .42, Dropouts: 3, LatencyNanos: 5_300_000}
	var hbuf [HealthFrameSize]byte
	health.Encode(hbuf[:])
	if got, err := DecodeHealthFrame(hbuf[:]); err != nil || got != health {
		t.Fatalf("health round trip: %+v %v", got, err)
	}
	if allocs := testing.AllocsPerRun(1000, func() { transport.Encode(buf[:]); meters.Encode(mbuf[:]); health.Encode(hbuf[:]) }); allocs != 0 {
		t.Fatalf("encode allocated %g", allocs)
	}
	if _, err := DecodeTransportFrame(buf[:TransportFrameSize-1]); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestTelemetryPublisherKeepsLatestFrameAndNotifiesSubscribers(t *testing.T) {
	pub := NewPublisher()
	sub := pub.Subscribe(4)
	defer pub.Unsubscribe(sub)
	pub.PublishTransport(TransportFrame{Bar: 1})
	pub.PublishTransport(TransportFrame{Bar: 2})
	latest := pub.Latest()
	if got, _ := DecodeTransportFrame(latest[FrameTransport]); got.Bar != 2 {
		t.Fatalf("latest transport bar %d", got.Bar)
	}
	if latest[FrameHealth] != nil {
		t.Fatal("health published before start")
	}
	frames := 0
	for len(sub.C) > 0 {
		<-sub.C
		frames++
	}
	if frames == 0 {
		t.Fatal("subscriber was not notified")
	}
	late := pub.Subscribe(1)
	if got, _ := DecodeTransportFrame(pub.Latest()[FrameTransport]); got.Bar != 2 || len(late.C) != 0 {
		t.Fatal("late subscriber snapshot must come from Latest, not a replayed channel")
	}
}

func TestTelemetryDecodeMetersFrameRejectsTrackCountAboveSixteen(t *testing.T) {
	var buf [MetersFrameSize]byte
	MetersFrame{TrackCount: 2}.Encode(buf[:])
	buf[2] = 17
	if _, err := DecodeMetersFrame(buf[:]); err == nil {
		t.Fatal("track count 17 accepted")
	}
}
