//go:build !darwin

package main

import "m31labs.dev/gosx/desktop"

func attachNativeMIDI(*desktop.App) (func(), error) { return func() {}, nil }
