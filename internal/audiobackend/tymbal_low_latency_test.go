//go:build linux || windows

package audiobackend

import (
	"fmt"
	"math"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/tymbal"
)

func TestTymbalCommonPeriodNegotiation(t *testing.T) {
	for _, test := range []struct {
		name          string
		request       int
		output, input tymbal.Device
		duplex        bool
		want          int
	}{
		{name: "unknown output capabilities", request: 64, want: 64},
		{name: "output quantum", request: 128, output: tymbal.Device{MinPeriod: 240, MaxPeriod: 960, FundamentalPeriod: 120}, want: 240},
		{name: "common duplex quantum", request: 64, output: tymbal.Device{MinPeriod: 128, MaxPeriod: 4096, FundamentalPeriod: 128}, input: tymbal.Device{MinPeriod: 240, MaxPeriod: 3840, FundamentalPeriod: 240}, duplex: true, want: 1920},
		{name: "never lower selected buffer", request: 512, output: tymbal.Device{MinPeriod: 48, MaxPeriod: 960, FundamentalPeriod: 48}, want: 528},
		{name: "incompatible duplex endpoints", request: 64, output: tymbal.Device{MinPeriod: 128, MaxPeriod: 512, FundamentalPeriod: 128}, input: tymbal.Device{MinPeriod: 240, MaxPeriod: 960, FundamentalPeriod: 240}, duplex: true},
		{name: "oversized requested buffer", request: 10000},
		{name: "oversized device quantum", request: 64, output: tymbal.Device{FundamentalPeriod: int(^uint(0) >> 1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var input *tymbal.Device
			if test.duplex {
				input = &test.input
			}
			got := tymbalPeriodCandidates(test.request, test.output, input)
			if test.want == 0 {
				if len(got) != 0 {
					t.Fatalf("unsupported geometry accepted: %v", got)
				}
				return
			}
			if len(got) == 0 || got[0] != test.want {
				t.Fatalf("candidates=%v, want first=%d", got, test.want)
			}
			for i, period := range got {
				if period < test.request || i > 0 && got[i-1] >= period {
					t.Fatalf("decreasing/duplicate period: %v", got)
				}
			}
		})
	}
}

func TestTymbalLowPeriodDuplexPreservesRawPCMAndDeviceTiming(t *testing.T) {
	for _, period := range []int{64, 128, 256, 512, 1024} {
		t.Run(fmt.Sprint(period), func(t *testing.T) {
			host, control := tymbal.NewFakeHost(tymbal.FakeConfig{Manual: true, Loopback: true, Delay: 1})
			var blocks int
			var invalid bool
			var lastFrame uint64
			stream, err := (tymbalBackend{host: host}).Open(Config{
				SampleRate: 48000, Channels: 2, CaptureChannels: 2, FramesPerPeriod: period,
				Capture: func(block capture.Block, input [][]float32) {
					if blocks > 0 && block.DeviceFrame != lastFrame+uint64(period) {
						invalid = true
					}
					if block.Frames != period || block.Period != period || block.SampleRate != 48000 || block.Layout != capture.LayoutStereo || block.DeviceEpoch == 0 {
						invalid = true
					}
					if blocks > 0 {
						for i := range input[0] {
							if math.Float32bits(input[0][i]) != math.Float32bits(.1234567) || math.Float32bits(input[1][i]) != math.Float32bits(-.7654321) {
								invalid = true
							}
						}
					}
					lastFrame = block.DeviceFrame
					blocks++
				},
			}, func(_ [][]float32, output [][]float32) error {
				for i := range output[0] {
					output[0][i], output[1][i] = .1234567, -.7654321
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if err := stream.Start(); err != nil {
				t.Fatal(err)
			}
			if err := control.Advance(200); err != nil {
				t.Fatal(err)
			}
			stream.Pause()
			actual := stream.Format()
			if blocks != 200 || invalid || actual.FramesPerPeriod != period || actual.CaptureChannels != 2 || actual.SampleRate != 48000 || actual.Priority == "" || stream.Stats().Dropouts != 0 {
				t.Fatalf("low period failed: blocks=%d invalid=%t actual=%+v stats=%+v", blocks, invalid, actual, stream.Stats())
			}
			t.Logf("METRIC period=%d callbacks=%d raw_pcm_max_delta=0 dropouts=0", period, blocks)
		})
	}
}

func TestTymbalLowRequestedPeriodHonorsReportedDeviceCapabilities(t *testing.T) {
	device := tymbal.Device{ID: "duplex", Name: "Fake constrained duplex", Inputs: 2, Outputs: 2, SampleRates: []int{48000}, MinPeriod: 240, MaxPeriod: 960, FundamentalPeriod: 120, DefaultPeriod: 480, Default: tymbal.Input | tymbal.Output}
	host, _ := tymbal.NewFakeHost(tymbal.FakeConfig{Manual: true, Devices: []tymbal.Device{device}})
	backend := tymbalBackend{host: host}
	devices, _, err := backend.Devices()
	if err != nil || len(devices) != 1 || devices[0].MinPeriod != 240 || devices[0].FundamentalPeriod != 120 {
		t.Fatalf("lost period capabilities: %+v, %v", devices, err)
	}
	stream, err := backend.Open(Config{SampleRate: 48000, Channels: 2, CaptureChannels: 2, FramesPerPeriod: 64}, func(_, _ [][]float32) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if got := stream.Format().FramesPerPeriod; got != 240 {
		t.Fatalf("negotiated period=%d, want240", got)
	}
}
