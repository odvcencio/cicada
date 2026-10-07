//go:build keys

package main

import (
	"m31labs.dev/cicada/host/kernelimage"
	_ "m31labs.dev/cicada/host/keyboard"
)

const keyboardCapabilities = uint32(kernelimage.KeysCapability)
