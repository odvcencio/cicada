package control

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"m31labs.dev/cicada/host/liveplay"
)

func TestNoteClockUsesAuthoredTempoBoundary(t *testing.T) {
	for _, tc := range []struct{ before, after, tick, want int64 }{
		{120000, 60000, 3648, 3936},
		{60000, 240000, 3744, 4224},
		{137333, 83333, 3621, 3973},
	} {
		t.Run(fmt.Sprintf("%d-to-%d", tc.before, tc.after), func(t *testing.T) {
			var raw [liveplay.TransportFrameSize]byte
			liveplay.TransportFrame{Bar: 1, Beat: 4, Tick: int32(tc.tick - 2880), TempoMilli: uint32(tc.before), Playing: true}.Encode(raw[:])
			// Decode the actual host projection, including time already spent in transit.
			data := fmt.Sprintf(`{"playing":true,"frameAgeNanos":50000000,"nextTempo":{"afterNanos":100000000,"tick":3840,"bpmMilli":%d},"frames":{"transport":%q}}`, tc.after, base64.StdEncoding.EncodeToString(raw[:]))
			var state PlaybackState
			if err := json.Unmarshal([]byte(data), &state); err != nil {
				t.Fatal(err)
			}
			state.RoundTrip = 40 * time.Millisecond
			head := state.Playhead()
			if got := head.NoteTick(130*time.Millisecond, 100); got != tc.want {
				t.Fatalf("note crossed tempo boundary at tick %d, want %d", got, tc.want)
			}
		})
	}
}
