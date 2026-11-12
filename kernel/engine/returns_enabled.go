//go:build !tinygo

package engine

// returnsEnabled lets TinyGo drop every extra-return call site. Portable images
// refuse a score with more than one reverb return before an engine is built.
const returnsEnabled = true
