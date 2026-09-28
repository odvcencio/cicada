//go:build linux || windows

package audiobackend

import (
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"m31labs.dev/tymbal"
	"m31labs.dev/tymbal/tymbaltest"
)

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
