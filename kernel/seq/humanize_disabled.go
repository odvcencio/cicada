//go:build tinygo

package seq

// HumanizeEnabled is false in portable (TinyGo) kernels, whose images refuse
// pattern humanize.
const HumanizeEnabled = false
