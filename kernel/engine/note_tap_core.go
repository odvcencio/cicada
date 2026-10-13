//go:build tinygo || js

package engine

import "m31labs.dev/cicada/kernel/seq"

// noteTap is empty in the core build, so the tap calls compile to nothing.
type noteTap struct{}

func (noteTap) fire(int, seq.Event) {}

func (*Engine) tapRelease(int, int64) {}
