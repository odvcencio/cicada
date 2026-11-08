//go:build !tinygo

package drum

// samplerLanesEnabled keeps pack-backed kit lanes out of portable (TinyGo)
// kernels, whose images refuse them; native builds play them.
const samplerLanesEnabled = true
