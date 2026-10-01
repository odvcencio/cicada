//go:build linux || windows

package audiobackend

import (
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/tymbal"
	"m31labs.dev/tymbal/tymbaltest"
)

func TestTymbalPreservesBlockTimingBeforeRender(t *testing.T) {
	var got capture.Block
	var raw float32
	timing := &tymbalTiming{
		epoch:  42,
		actual: tymbal.Actual{SampleRate: 96000, Period: 480, InChannels: 2, OutChannels: 2, LatencyIn: time.Millisecond, LatencyOut: 2 * time.Millisecond},
		tap:    func(b capture.Block, input [][]float32) { got, raw = b, input[0][0] },
	}
	cb := tymbalTimedCallback(func(in, _ [][]float32) error { in[0][0] = 0; return nil }, &atomic.Pointer[audioError]{}, timing)
	tm := tymbal.Time{Frame: 1234, InputNano: 9000, OutputNano: 12000, InputPosition: 77, InputFrequency: 96000, OutputPosition: 100, OutputFrequency: 10000000, Dropouts: 3, Discontinuity: true}
	cb(tm, [][]float32{{0.75}, {0.5}}, [][]float32{{0}, {0}})
	if raw != 0.75 || got.DeviceEpoch != 42 || got.DeviceFrame != 1234 || got.Frames != 1 || got.Period != 480 || got.SampleRate != 96000 || got.Layout != capture.LayoutStereo || got.Flags != capture.DeviceDiscontinuity || got.DeviceDropouts != 3 {
		t.Fatalf("lost block metadata: %+v, raw %g", got, raw)
	}
	if got.InputTime.Nano != 9000 || !got.InputTime.Valid || got.InputTime.Domain != capture.ClockHostMonotonic || got.OutputTime.Reference != capture.ReferenceClockObservation || got.InputPosition.Position != 77 || got.OutputPosition.Frequency != 10000000 || got.InputLatencyNano != int64(time.Millisecond) || got.OutputLatencyNano != int64(2*time.Millisecond) {
		t.Fatalf("lost clock metadata: %+v", got)
	}
	got = timing.block(tymbal.Time{}, nil, nil)
	if got.InputTime.Valid || got.OutputTime.Valid || got.InputPosition.Valid || got.OutputPosition.Valid || got.Calibration.Valid {
		t.Fatalf("fabricated timing confidence: %+v", got)
	}
}

func TestTymbalCaptureAndFaultPathsAllocateZero(t *testing.T) {
	r, _ := capture.NewRing(1, 256, 2)
	timing := &tymbalTiming{epoch: 1, actual: tymbal.Actual{SampleRate: 48000, Period: 256}, tap: func(b capture.Block, in [][]float32) { r.Push(b, in) }}
	for _, fail := range []bool{false, true} {
		errCell := &atomic.Pointer[audioError]{}
		cb := tymbalTimedCallback(func(_, _ [][]float32) error {
			if fail {
				return tymbal.ErrCallback
			}
			return nil
		}, errCell, timing)
		in, out := [][]float32{make([]float32, 256), make([]float32, 256)}, [][]float32{make([]float32, 256), make([]float32, 256)}
		if n := testing.AllocsPerRun(1000, func() { errCell.Store(nil); cb(tymbal.Time{}, in, out) }); n != 0 {
			t.Fatalf("capture/fault allocations = %g (fault %v)", n, fail)
		}
	}
}

func TestTymbalCallbackIsAllocationFree(t *testing.T) {
	render := Callback(func(_ [][]float32, output [][]float32) error {
		for _, channel := range output {
			for i := range channel {
				channel[i] = 0
			}
		}
		return nil
	})
	callbackErr := &atomic.Pointer[audioError]{}
	tymbaltest.NoAlloc(t, tymbalCallback(render, callbackErr), 0, 2, 256)
}

func TestTymbalBusyErrorNamesLinuxDeviceAndOtoFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the explicit busy-device message applies to the ALSA backend")
	}
	err := tymbalOpenError("ALSA default", "", tymbal.ErrBusy)
	for _, want := range []string{"ALSA default", "--audio oto"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("busy error %q does not contain %q", err, want)
		}
	}
}

func TestTymbalDeviceErrorLines(t *testing.T) {
	for _, test := range []struct {
		name, goos, device string
		direction          tymbal.Direction
		err                error
		want               string
	}{
		{
			name: "no ALSA playback device", goos: "linux", direction: tymbal.Output,
			err: errors.New("alsa: no default device for direction 1"), want: "no ALSA playback device found; use --audio oto",
		},
		{
			name: "busy ALSA device", goos: "linux", direction: tymbal.Output, device: "ALSA default",
			err: tymbal.ErrBusy, want: "Tymbal ALSA device \"ALSA default\" is busy; use --audio oto",
		},
		{
			name: "WASAPI failure", goos: "windows", direction: tymbal.Output, device: "Speakers",
			err: errors.New("WASAPI activation failed\nHRESULT 0x80070005"), want: "Tymbal WASAPI device \"Speakers\" could not be opened; use --audio oto: WASAPI activation failed HRESULT 0x80070005",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := tymbalDeviceErrorForOS(test.goos, test.direction, test.device, test.err).Error()
			if got != test.want {
				t.Fatalf("device error = %q, want %q", got, test.want)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("device error contains a line break: %q", got)
			}
		})
	}
}

func TestTymbalDuplexErrorNamesPlaybackAndCaptureDevices(t *testing.T) {
	err := tymbalDuplexDeviceErrorForOS("windows", "Speakers", "Microphone", tymbal.ErrBusy)
	want := "Tymbal WASAPI playback device \"Speakers\" and capture device \"Microphone\" could not be opened because one endpoint is busy; use --audio oto: tymbal: device busy"
	if err.Error() != want {
		t.Fatalf("duplex device error = %q, want %q", err, want)
	}
}
