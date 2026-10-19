//go:build tinygo && cicada_reactor

package kernelimage

// The audio reactor reports numeric initialization results, never Go error
// text. Discarding these strings lets LLVM remove their data while keeping all
// validation branches. Ordinary Go and other TinyGo consumers keep diagnostics.
func imageError(_ string) error { return Error("invalid kernel image") }
