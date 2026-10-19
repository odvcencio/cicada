package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
)

func TestGoSXLiveHeartbeatAndCrashedClientExpiry(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			score, err := compileLiveScore(studioTestPath(t, authoredLiveScore))
			if err != nil {
				t.Fatal(err)
			}
			stream, err := liveplay.New(score, liveSampleRate)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			s := &studio{transport: &studioTransport{stream: stream, playing: true, audioNull: true}}
			h := s.domainRoutes()
			on, velocity, note := true, 100, 60
			message := audioClientMessage{Type: "note", Track: "keys", Note: &note, Velocity: &velocity, On: &on}
			owner := "expiring-native-midi-mount"
			request := studioLiveRequest{Owner: owner, Sequence: 1, Messages: []audioClientMessage{message}}
			if r := studioCall(t, h, "/api/live", request); r.Code != 200 {
				t.Fatalf("note on: %d %s", r.Code, r.Body.String())
			}
			if shared {
				other := request
				other.Owner = "healthy-other-input-mount"
				if r := studioCall(t, h, "/api/live", other); r.Code != 200 {
					t.Fatal(r.Code, r.Body.String())
				}
			}
			power := func() float64 {
				t.Helper()
				buffer := make([]byte, liveBlockFrames*8)
				for range 32 {
					if _, err := io.ReadFull(stream, buffer); err != nil {
						t.Fatal(err)
					}
				}
				var total float64
				for range 16 {
					if _, err := io.ReadFull(stream, buffer); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < len(buffer); i += 4 {
						v := float64(math.Float32frombits(binary.LittleEndian.Uint32(buffer[i:])))
						total += v * v
					}
				}
				return total / float64(16*liveBlockFrames*2)
			}
			if p := power(); p < 1e-5 {
				t.Fatalf("held note is silent: %g", p)
			}
			now := time.Now()
			s.liveControls.mu.Lock()
			s.liveControls.leases[owner].Seen = now.Add(-studioLiveLeaseTimeout - time.Second)
			if shared {
				s.liveControls.leases["healthy-other-input-mount"].Seen = now
			}
			s.liveControls.mu.Unlock()
			ping := studioLiveRequest{Owner: owner, Sequence: 2, Ping: true}
			if r := studioCall(t, h, "/api/live", ping); r.Code != 200 {
				t.Fatal(r.Code, r.Body.String())
			}
			if expired := s.expireLiveInputs(now); expired != 0 {
				t.Fatal("heartbeat did not renew held input", expired)
			}
			s.liveControls.mu.Lock()
			s.liveControls.leases[owner].Seen = now.Add(-studioLiveLeaseTimeout - time.Second)
			s.liveControls.mu.Unlock()
			if expired := s.expireLiveInputs(now); expired != 1 {
				t.Fatal("crashed input did not expire", expired)
			}
			if shared {
				if p := power(); p < 1e-5 {
					t.Fatalf("expiry silenced another input's held pitch: %g", p)
				}
				if r := studioCall(t, h, "/api/live", studioLiveRequest{Owner: "healthy-other-input-mount", Sequence: 2, Release: true}); r.Code != 200 {
					t.Fatal(r.Code, r.Body.String())
				}
			}
			if p := power(); p > 1e-12 {
				t.Fatalf("expired input still sounds: %g", p)
			}
			request.Sequence = 3
			if r := studioCall(t, h, "/api/live", request); r.Code != http.StatusConflict {
				t.Fatal("late note resurrected expired input", r.Code)
			}
			if p := power(); p > 1e-12 {
				t.Fatalf("late input still sounds: %g", p)
			}
		})
	}
}

func TestGoSXLiveIdleMountCanPlayAgainAfterARest(t *testing.T) {
	score, err := compileLiveScore(studioTestPath(t, studioScore))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := liveplay.New(score, liveSampleRate)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	s := &studio{transport: &studioTransport{stream: stream, playing: true, audioNull: true}}
	h := s.domainRoutes()
	on, velocity, note := true, 100, 60
	request := studioLiveRequest{Owner: "idle-musical-input-mount", Sequence: 1, Messages: []audioClientMessage{{Type: "note", Track: "bass", Note: &note, Velocity: &velocity, On: &on}}}
	if r := studioCall(t, h, "/api/live", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	on, velocity = false, 0
	request.Sequence = 2
	if r := studioCall(t, h, "/api/live", request); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	now := time.Now()
	s.liveControls.mu.Lock()
	s.liveControls.leases[request.Owner].Seen = now.Add(-time.Hour)
	s.liveControls.mu.Unlock()
	if expired := s.expireLiveInputs(now); expired != 0 {
		t.Fatal("a musical rest ended its input mount")
	}
	on, velocity = true, 100
	request.Sequence = 3
	if r := studioCall(t, h, "/api/live", request); r.Code != 200 {
		t.Fatal("input did not resume after rest", r.Code, r.Body.String())
	}
}
