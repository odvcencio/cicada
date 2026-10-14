package main

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"m31labs.dev/cicada/internal/testwav"
)

func m4ClipSource() (string, []byte) {
	wav := testwav.Bytes(48000, 1, 16, 48000, 1)
	return strings.Replace(clipEditScore, strings.Repeat("a", 64), fmt.Sprintf("%x", sha256.Sum256(wav)), 1), wav
}
