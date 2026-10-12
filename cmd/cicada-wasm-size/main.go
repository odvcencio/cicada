// cicada-wasm-size enforces the kernel download budget on the built artifact.
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/andybalholm/brotli"
)

// 320 KiB: raised from 300 KiB on 2026-10-04 by owner decision so the graph delay/comb,
// experimental guitar and phase-modulation voices fit together (about 321.7 KB raw).
const rawLimit = 320 * 1024
const brotliLimit = 120 * 1024
const processorLimit = 5 * 1024

func main() {
	reportOnly := len(os.Args) > 1 && os.Args[1] == "--report-only"
	if reportOnly {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if len(os.Args) != 2 && len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: cicada-wasm-size <kernel.wasm> [processor.js]")
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
	if reportOnly {
		fmt.Printf("optional audio WASM: raw=%d bytes, Brotli=%d bytes (reported separately)\n", len(data), compressed.Len())
	} else {
		fmt.Printf("kernel WASM: raw=%d/%d bytes, Brotli=%d/%d bytes\n", len(data), rawLimit, compressed.Len(), brotliLimit)
	}
	if !reportOnly && (len(data) > rawLimit || compressed.Len() > brotliLimit) {
		fail(fmt.Errorf("kernel WASM exceeds the size budget"))
	}
	if len(os.Args) == 3 {
		processor, err := os.ReadFile(os.Args[2])
		if err != nil {
			fail(err)
		}
		fmt.Printf("AudioWorklet processor: raw=%d/%d bytes\n", len(processor), processorLimit)
		if len(processor) > processorLimit {
			fail(fmt.Errorf("AudioWorklet processor exceeds the size budget"))
		}
		fmt.Println("PASS budget-size")
		return
	}
	if reportOnly {
		fmt.Println("REPORT optional audio module; default core budget unchanged")
	} else {
		fmt.Println("PASS build-kernel-wasm")
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "FAIL size budget:", err)
	os.Exit(1)
}
