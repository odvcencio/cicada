// cicada-wasm-size enforces the kernel download budget on the built artifact.
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/andybalholm/brotli"
)

const rawLimit = 300 * 1024
const brotliLimit = 120 * 1024

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cicada-wasm-size <kernel.wasm>")
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	var compressed bytes.Buffer
	writer := brotli.NewWriterLevel(&compressed, 11)
	if _, err := writer.Write(data); err != nil {
		fail(err)
	}
	if err := writer.Close(); err != nil {
		fail(err)
	}
	fmt.Printf("kernel WASM: raw=%d/%d bytes, Brotli=%d/%d bytes\n", len(data), rawLimit, compressed.Len(), brotliLimit)
	if len(data) > rawLimit || compressed.Len() > brotliLimit {
		fail(fmt.Errorf("kernel WASM exceeds the size budget"))
	}
	fmt.Println("PASS build-kernel-wasm")
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "FAIL build-kernel-wasm:", err)
	os.Exit(1)
}
