//go:build tinygo || js

package engine

// masterInputTapEnabled is false in the core build: only native hosts replay
// a stored master input, so the TapFrame.MasterInput store compiles out.
const masterInputTapEnabled = false
