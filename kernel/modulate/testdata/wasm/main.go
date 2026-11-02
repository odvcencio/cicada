package main

import (
	"unsafe"

	"m31labs.dev/cicada/kernel/modulate/testdata/fixture"
)

var digest [32]byte

//go:wasmexport modulate_fixture_run
func run() { digest = fixture.Digest() }

//go:wasmexport modulate_fixture_ptr
func pointer() uint32 { return uint32(uintptr(unsafe.Pointer(&digest[0]))) }

func main() {}
