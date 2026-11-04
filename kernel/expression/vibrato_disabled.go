//go:build tinygo && !modular

package expression

type defaultVibrato struct{}

func (*defaultVibrato) supported() bool                  { return false }
func (*defaultVibrato) configure(VibratoConfig, float32) {}
func (*defaultVibrato) reset()                           {}
func (*defaultVibrato) active() bool                     { return false }
func (*defaultVibrato) next() float64                    { return 0 }
