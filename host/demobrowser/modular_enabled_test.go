//go:build modular

package demobrowser

import (
	"math"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
)

const demoModular = true

func TestDriftActuallyChangesAudio(t *testing.T) {
	var source string
	for _, preset := range Presets() {
		if preset.ID == "drift" {
			source = preset.Source
		}
	}
	_, cfg, _, err := Prepare([]byte(source), 48000)
	if err != nil {
		t.Fatal(err)
	}
	render := func() []float32 {
		t.Helper()
		e, err := engine.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255})
		left, right := make([]float32, 48000), make([]float32, 48000)
		for at := 0; at < len(left); at += 128 {
			end := min(at+128, len(left))
			e.Render(left[at:end], right[at:end])
		}
		return left
	}
	withModulation := render()
	for i := range cfg.Routes {
		cfg.Routes[i].Depth = 0
	}
	withoutModulation := render()
	var difference float64
	for i, sample := range withModulation {
		difference += math.Abs(float64(sample - withoutModulation[i]))
	}
	if difference < 0.01 {
		t.Fatalf("modulation did not audibly change the rendered signal: difference %g", difference)
	}
}
