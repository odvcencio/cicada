//go:build linux || windows

package audiobackend

import (
	"sort"

	"m31labs.dev/tymbal"
)

// tymbalPeriodCandidates requests a common device period rather than relying
// on independently rounded duplex endpoints. Only raise the requested period;
// silently reducing it could make an otherwise safe callback miss deadlines.
// Unknown capability fields impose no restriction. All setup runs off callback.
func tymbalPeriodCandidates(requested int, output tymbal.Device, input *tymbal.Device) []int {
	if requested <= 0 || requested > 8192 {
		return nil
	}
	minimum, maximum, quantum := requested, 8192, 1
	devices := []tymbal.Device{output}
	if input != nil {
		devices = append(devices, *input)
	}
	for _, device := range devices {
		minimum = max(minimum, device.MinPeriod)
		if device.MaxPeriod > 0 {
			maximum = min(maximum, device.MaxPeriod)
		}
		if device.FundamentalPeriod > 1 {
			divisor := gcdPeriod(quantum, device.FundamentalPeriod)
			if quantum/divisor > 8192/device.FundamentalPeriod {
				return nil
			}
			quantum = quantum / divisor * device.FundamentalPeriod
		}
	}
	if minimum > maximum {
		return nil
	}
	var candidates []int
	add := func(period int) {
		if period < minimum || period > maximum {
			return
		}
		period = (period + quantum - 1) / quantum * quantum
		if period > maximum {
			return
		}
		for _, previous := range candidates {
			if previous == period {
				return
			}
		}
		candidates = append(candidates, period)
	}
	add(minimum)
	for _, period := range [...]int{64, 128, 256, 384, 480, 512, 768, 960, 1024, 1536, 1920, 2048, 3072, 3840, 4096, 8192} {
		add(period)
	}
	for _, device := range devices {
		add(device.DefaultPeriod)
	}
	sort.Ints(candidates)
	return candidates
}

func gcdPeriod(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
