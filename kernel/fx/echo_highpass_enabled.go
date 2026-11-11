//go:build !tinygo

package fx

// EchoHighpassEnabled is false in portable (TinyGo) kernels, whose images
// refuse the echo high-pass (the core parameter table has no rows for it), so
// TinyGo drops the filter and every call site.
const EchoHighpassEnabled = true
