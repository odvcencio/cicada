// cicada-wasm-size enforces the kernel download budget on the built artifact.
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/andybalholm/brotli"
)

// Two pairs of numbers bound the core kernel (decision 10, hypha://m31labs/cicada):
//   - the working limit, rawLimit and brotliLimit: the measured size plus 4,096 bytes each,
//     re-based by a pull request whose purpose adds kernel code, which states the old limit,
//     the measured size and the new limit here;
//   - the hard ceiling of decision 9, rawCeiling and brotliCeiling: 335,872 bytes raw and
//     122,880 Brotli. No pull request moves it; going over it needs the owner.
//
// 332,139 bytes raw and 120,863 Brotli: re-based on 2026-11-12 by #323, which flushes
// subnormal numbers in the decaying state and smoothed controls of the voices and effects
// (kernel/dsp/subnormal). Old limit 328,525 raw and 119,562 Brotli; measured size with main
// fc3f73ad merged 328,043 raw and 116,767 Brotli (TinyGo 0.41.1, Binaryen 133); new limit the
// measured size plus 4,096. The flushes add about 3,458 raw bytes: the piano's and the
// reverb's sweeps about 770 and 570 (wasm-opt 116, when they were added), the rest the sweeps
// of the delay, compressor, acid voice, drum kit and sample voice and the reverb's
// per-channel diffuser sweep.
//
// 328,525 bytes raw and 119,562 bytes Brotli: lowered on 2026-11-12 from 328 KiB raw and
// 120 KiB Brotli to the core's size with the whole determinism stack merged with main 6f8d023a
// (324,429 bytes raw and 115,466 Brotli at commit db973cb2, TinyGo 0.41.1 and Binaryen 133) plus
// 4 KiB each. The raw limit is below 322 KiB (329,728 bytes), the limit before Decision 9,
// because the voices no longer call Go's math functions, which drop out of the binary. (An
// earlier commit of the stack set 327,187 bytes raw the same way, from 323,091 at 6c6416b5.)
// 328 KiB (335,872 bytes): raised from 322 KiB on 2026-11-10 under Decision 9 of the
// stewardship decision (hypha://m31labs/cicada, decisions/2026-11-10-stewardship-and-direction):
// the determinism contract outranks the core size budget. The 6,144 bytes are for one purpose
// only, the deterministic math package (kernel/dsp/detmath) that replaces the math library
// functions whose last bits differ by architecture in the track effects and the mix.
// 322 KiB: raised from 320 KiB on 2026-10-27 by owner decision for the modal sustain pedal
// and the modeled-kit brushes. With both, the kernel is 328,793 bytes raw when built with
// Binaryen 133, the version CI pins. Built with the wasm-opt that TinyGo 0.41.1 bundles
// (version 116, which scripts/wasm-opt.sh uses when WASMOPT is unset) it is 329,264.
// Raised from 300 KiB to 320 KiB on 2026-10-04 by owner decision so the graph delay/comb,
// experimental guitar and phase-modulation voices fit together (about 321.7 KB raw).
const rawLimit = 332_139
const brotliLimit = 120_863

// rawCeiling and brotliCeiling are decision 9's hard ceiling (see above).
const rawCeiling = 335_872
const brotliCeiling = 122_880
const processorLimit = 5 * 1024
const keysModularRawLimit = 576 * 1024
const keysModularBrotliLimit = 224 * 1024

func main() {
	reportOnly := len(os.Args) > 1 && os.Args[1] == "--report-only"
	keysModular := len(os.Args) > 1 && os.Args[1] == "--keys-modular"
	modular := len(os.Args) > 1 && os.Args[1] == "--modular"
	if reportOnly {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if keysModular {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if modular {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if len(os.Args) != 2 && len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: cicada-wasm-size [--report-only|--modular|--keys-modular] <kernel.wasm> [processor.js]")
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
	if keysModular || modular {
		name := "keys+modular"
		if modular {
			name = "modular"
		}
		fmt.Printf("%s WASM: raw=%d/%d bytes, Brotli=%d/%d bytes\n", name, len(data), keysModularRawLimit, compressed.Len(), keysModularBrotliLimit)
		if len(data) > keysModularRawLimit || compressed.Len() > keysModularBrotliLimit {
			fail(fmt.Errorf("%s WASM exceeds the size budget", name))
		}
	} else if reportOnly {
		fmt.Printf("optional audio WASM: raw=%d bytes, Brotli=%d bytes (reported separately)\n", len(data), compressed.Len())
	} else {
		fmt.Printf("kernel WASM: raw=%d/%d bytes, Brotli=%d/%d bytes\n", len(data), rawLimit, compressed.Len(), brotliLimit)
	}
	if !reportOnly && !keysModular && !modular {
		fmt.Printf("kernel WASM ceiling (decision 9): raw=%d/%d bytes, Brotli=%d/%d bytes\n", len(data), rawCeiling, compressed.Len(), brotliCeiling)
		if len(data) > rawCeiling || compressed.Len() > brotliCeiling {
			fail(fmt.Errorf("kernel WASM is over the ceiling of decision 9: needs the owner"))
		}
		if len(data) > rawLimit || compressed.Len() > brotliLimit {
			fail(fmt.Errorf("kernel WASM exceeds the size budget (the working limit; a pull request whose purpose adds kernel code re-bases it, see cmd/cicada-wasm-size)"))
		}
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
	} else if keysModular || modular {
		fmt.Println("PASS modular budget; default core budget unchanged")
	} else {
		fmt.Println("PASS build-kernel-wasm")
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "FAIL size budget:", err)
	os.Exit(1)
}
