//go:build !tinygo

package seq

// HumanizeEnabled keeps pattern humanize out of portable (TinyGo) kernels,
// whose images refuse it; native builds apply it.
const HumanizeEnabled = true
