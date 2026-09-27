package main

import "m31labs.dev/cicada/kernel/loudness"

func main() {
	meter, err := loudness.New(48_000)
	if err != nil {
		return
	}
	meter.ProcessSample(0.25, -0.25)
	_ = meter.Metrics()
}
