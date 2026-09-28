//go:build linux || windows

package audiobackend

import (
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
	err := tymbalOpenError("ALSA default", false, tymbal.ErrBusy)
	for _, want := range []string{"ALSA default", "--audio oto"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("busy error %q does not contain %q", err, want)
		}
	}
}
