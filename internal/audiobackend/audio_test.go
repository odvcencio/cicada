package audiobackend

import (
	"strings"
	"testing"

	"m31labs.dev/tymbal"
	"m31labs.dev/tymbal/tymbaltest"
)

func TestAudioSelectionPrecedence(t *testing.T) {
	for _, test := range []struct {
		name     string
		flag     string
		flagSet  bool
		env      string
		fallback Name
		want     Name
	}{
		{name: "default", fallback: Oto, want: Oto},
		{name: "environment", env: "tymbal", fallback: Oto, want: Tymbal},
		{name: "flag overrides environment", flag: "null", flagSet: true, env: "tymbal", fallback: Oto, want: Null},
		{name: "flag overrides invalid environment", flag: "oto", flagSet: true, env: "broken", fallback: Null, want: Oto},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Select(test.flag, test.flagSet, test.env, test.fallback)
			if err != nil || got != test.want {
				t.Fatalf("Select() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestAudioSelectionRejectsUnknownNameWithValidNames(t *testing.T) {
	_, err := Select("alsa", true, "", Oto)
	if err == nil || err.Error() != `unknown audio backend "alsa" (valid: tymbal, oto, null)` {
		t.Fatalf("Select() error = %v", err)
	}
	_, err = Select("", false, "pulse", Oto)
	if err == nil || !strings.Contains(err.Error(), "valid: tymbal, oto, null") {
		t.Fatalf("environment error = %v", err)
	}
}

func TestAudioArgumentParsing(t *testing.T) {
	args, value, set, err := ParseFlag([]string{"score.cicada", "--audio", "null", "--listen", "127.0.0.1:8160"})
	if err != nil || !set || value != "null" || strings.Join(args, " ") != "score.cicada --listen 127.0.0.1:8160" {
		t.Fatalf("ParseFlag() = %v, %q, %t, %v", args, value, set, err)
	}
	if _, _, _, err := ParseFlag([]string{"--audio"}); err == nil || !strings.Contains(err.Error(), "tymbal, oto, null") {
		t.Fatalf("missing value error = %v", err)
	}
}

func TestAudioDefaultsByCommandAndOS(t *testing.T) {
	for _, test := range []struct {
		command, goos string
		want          Name
	}{
		{command: "play", goos: "windows", want: Tymbal},
		{command: "play", goos: "linux", want: Tymbal},
		{command: "play", goos: "darwin", want: Oto},
		{command: "studio", goos: "windows", want: Tymbal},
		{command: "studio", goos: "linux", want: Tymbal},
		{command: "studio", goos: "darwin", want: Oto},
		{command: "other", goos: "windows", want: Oto},
	} {
		if got := DefaultForOS(test.command, test.goos); got != test.want {
			t.Errorf("DefaultForOS(%q, %q) = %q, want %q", test.command, test.goos, got, test.want)
		}
	}
}

func TestNullPeriodIsSilentAndAllocationFree(t *testing.T) {
	render := Callback(func(_ [][]float32, output [][]float32) error {
		for _, channel := range output {
			for i := range channel {
				channel[i] = 0.25
			}
		}
		return nil
	})
	callback := func(_ tymbal.Time, input, output [][]float32) {
		if err := renderNullPeriod(render, input, output); err != nil {
			panic(err)
		}
	}
	tymbaltest.NoAlloc(t, callback, 0, 2, 256)
	output := [][]float32{make([]float32, 256), make([]float32, 256)}
	if err := renderNullPeriod(render, nil, output); err != nil {
		t.Fatal(err)
	}
	for channelIndex, channel := range output {
		for frame, sample := range channel {
			if sample != 0 {
				t.Fatalf("null output channel %d frame %d = %f, want zero", channelIndex, frame, sample)
			}
		}
	}
}

func TestCallbackReaderEncodesFloat32LittleEndian(t *testing.T) {
	reader := newCallbackReader(Config{Channels: 2, FramesPerPeriod: 2}, func(_ [][]float32, output [][]float32) error {
		output[0][0], output[0][1] = 0.5, -0.25
		output[1][0], output[1][1] = -1, 1
		return nil
	})
	var got [16]byte
	if _, err := reader.Read(got[:]); err != nil {
		t.Fatal(err)
	}
	want := [16]byte{0, 0, 0, 0x3f, 0, 0, 0x80, 0xbf, 0, 0, 0x80, 0xbe, 0, 0, 0x80, 0x3f}
	if got != want {
		t.Fatalf("PCM = %v, want %v", got, want)
	}
}
