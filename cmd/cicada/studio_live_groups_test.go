package main

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
)

func TestGoSXLiveSharedControlsReachTailsOnlyForTheirLease(t *testing.T) {
	for _, track := range []string{"mono", "keys"} {
		t.Run(track, func(t *testing.T) {
			render := func(mode string) []byte {
				source := strings.ReplaceAll(authoredLiveScore, "20ms)", "1800ms)")
				source = strings.ReplaceAll(source, "amp * velocity *", "amp * velocity * (0.1+pressure) *")
				score, err := compileLiveScore(studioTestPath(t, source))
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
				sequence := uint64(0)
				call := func(owner string, messages ...audioClientMessage) int {
					t.Helper()
					sequence++
					r := studioCall(t, h, "/api/live", studioLiveRequest{Owner: owner, Sequence: sequence, Messages: messages})
					if r.Code != 200 && r.Code != 409 {
						t.Fatal(r.Code, r.Body.String())
					}
					return r.Code
				}
				owner := "shared-owned-window"
				on, off, note, velocity := true, false, 60, 100
				pitch, pressure, timbre := float32(0), float32(.2), float32(.5)
				shared := audioClientMessage{Type: "group-expression", Track: track, GroupID: "1", PitchCents: &pitch, Pressure: &pressure, Timbre: &timbre}
				call(owner, audioClientMessage{Type: "note", Track: track, NoteID: float64(7), Note: &note, Velocity: &velocity, On: &on}, audioClientMessage{Type: "note-group", Track: track, NoteID: float64(7), GroupID: "1"}, shared)
				result := make([]byte, liveBlockFrames*8*12)
				for block := range 12 {
					if block == 2 {
						call(owner, audioClientMessage{Type: "note", Track: track, NoteID: float64(7), Note: &note, Velocity: &velocity, On: &off})
					}
					if block == 3 && mode == "expired" {
						s.liveControls.leases[owner].Seen = time.Now().Add(-2 * studioLiveLeaseTimeout)
						if s.expireLiveInputs(time.Now()) != 1 {
							t.Fatal("released shared lease did not expire")
						}
						if s.liveControls.leases[owner].Groups != nil {
							t.Fatal("retired lease retained its shared input map")
						}
					}
					if block == 4 && mode != "baseline" {
						pressure = .8
						target := owner
						if mode == "foreign" {
							target = "foreign-owned-window"
						}
						status := call(target, shared)
						if mode == "expired" && status != 409 {
							t.Fatal("expired lease admitted tail controls")
						}
					}
					if _, err := stream.Read(result[block*liveBlockFrames*8 : (block+1)*liveBlockFrames*8]); err != nil {
						t.Fatal(err)
					}
				}
				return result
			}
			baseline := render("baseline")
			for _, mode := range []string{"foreign", "expired", "owned"} {
				got := render(mode)
				first, peak := -1, float64(0)
				for i := 0; i < len(got); i += 4 {
					delta := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(got[i:])) - math.Float32frombits(binary.LittleEndian.Uint32(baseline[i:]))))
					if delta > 0 && first < 0 {
						first = i / 8
					}
					peak = max(peak, delta)
				}
				if mode == "owned" {
					if first < liveBlockFrames*4 || first >= liveBlockFrames*5 || peak < .001 {
						t.Fatal("owned master did not reach released audio", first, peak)
					}
				} else if first != -1 {
					t.Fatal("foreign or expired lease changed a tail", mode, first, peak)
				}
				t.Logf("%s: first changed frame %d, peak delta %.6f", mode, first, peak)
			}
		})
	}
}
