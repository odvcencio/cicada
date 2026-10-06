# Crossfaded-loop playback

Crossfaded loops use direct planar PCM reads when the entire FIR window lies
in a contiguous part of the recording, including its unwrapped attack. A
window wholly inside the fade tail reads its two contiguous source spans
directly. It retains the original per-frame division and float32 blend rounding
before FIR multiplication. Windows crossing a fade, wrapped head, region
boundary or loop seam keep the generic per-frame mapping.

The filter banks, coefficient interpolation, ascending tap order and explicit
rounding are unchanged. A one-semitone upward transposition still uses 108 taps.
Input PCM stays immutable. No prepared cache, PCM copies or additional resident
sample memory are needed; the 256 MiB decoded PCM guard is unchanged.

The boundary oracle independently retains the original frame mapping and blend
arithmetic. It compares 2,361,600 stereo results with identical float64 bits in
both channels: mono/stereo, six fade lengths, first-pass/wrapped playback,
every source position, four fractional phases and twelve ratios from 0.125
through 8. It also passes with native FMA enabled. TinyGo fixtures include
larger mono/stereo loops, long fades and wider FIR banks at 44.1, 48 and 96 kHz;
all 319,488 native/WASM sample values match across 64-, 128- and 256-frame
blocks. Trigger, reset, render and release allocation counts are zero.

Actual owned banks were admitted through the pinned instrument-pack loader:
48 kHz, 128-frame blocks, velocity 102, exactly one or eight sustained voices,
captured roots 48 + 3n or one semitone above. Each bank has 30 roots, five
velocity layers and two round robins. Loop voices warmed for three seconds,
then ran continuously through their seams. Loading, triggering, polyphony
checks and finite-recording resets were outside timed rendering.

Native measurements on an Intel Core Ultra 9 285, Go 1.25.1, GOMAXPROCS=2:

| Bank | Voices | Original mapping ns/frame | Optimized ns/frame | Optimized ns/frame/voice | Optimized ms/128 frames |
| --- | ---: | ---: | ---: | ---: | ---: |
| Tine EP | 1 | 154.22 | 157.06 | 157.06 | 0.0201 |
| Tine EP | 8 | 1116.99 | 1083.06 | 135.38 | 0.1386 |
| Tonewheel Organ | 1 | 1001.90 | 302.71 | 302.71 | 0.0387 |
| Tonewheel Organ | 8 | 8166.34 | 2510.32 | 313.79 | 0.3213 |
| Soft Pad | 1 | 1050.93 | 299.30 | 299.30 | 0.0383 |
| Soft Pad | 8 | 8333.68 | 2668.19 | 333.52 | 0.3415 |

Eight captured-root voices measured Tine EP 143.69 ns/frame, Tonewheel Organ 140.69 ns/frame, Soft Pad 143.68 ns/frame. Captured roots bypass FIR, and
finite EP recordings already used direct reads, so timing variation in those
cases reflects machine load rather than these optimizations. The original and
optimized measurements used the same benchmark on a busy machine; the elapsed
costs are neither guaranteed speedup ratios nor whole-project CPU measurements.
Representative banks retain zero render/event/reset/release allocations and
zero bytes per render block.

The same full pinned banks and standalone sampler ABI were measured directly
in Windows Chrome 154.0.8037.98, with the production planar output copy included.
Each case uses eight distinct admitted slots, 4096 warm blocks and 11,264
measured blocks, at 48 kHz and velocity 102. A cross-origin-isolated page gives
5 microsecond `performance.now()` resolution. Decode/upload, events and output
validation are outside timing; loop voices continue through their fades/seams.
Auxiliary pages are excluded before loading or rendering.

| Bank/playback | Median ms | p95 ms | p99 ms |
| --- | ---: | ---: | ---: |
| Tine EP / captured roots | 0.020 | 0.025 | 0.035 |
| Tonewheel Organ / captured roots | 0.025 | 0.035 | 0.045 |
| Tonewheel Organ / one semitone up | 0.270 | 0.390 | 0.440 |
| Soft Pad / captured roots | 0.025 | 0.030 | 0.045 |
| Soft Pad / one semitone up | 0.235 | 0.330 | 0.380 |

The earlier path outside fades alone measured transposed organ/pad p99 values
of 0.845/0.675 ms on a separate run. With direct fade reads, both are below
0.67 ms in this page test. Each prepared instance retains 256 MiB of WASM memory
without render growth, rejected calls or nonfinite/silent output. Occasional
elapsed spikes remain: the final run reached 0.705 ms for transposed organ and
1.470 ms for pad. AudioContext and AudioWorklet were not constructed, so these
measurements do not establish audio-device scheduling, event traffic, underruns
or a whole-project AudioWorklet CPU gate.

With current main's clip support, the core kernel includes this sampler path.
The optimized core is 310,880 bytes raw / 108,277 Brotli, versus 310,331 /
108,291 on main: +549 raw / -14 Brotli. It fits the unchanged
327,680/122,880-byte limits. The AudioWorklet remains 5,089/5,120 bytes.
The separate sampler module is 32,814 bytes raw / 14,101 Brotli, versus
32,274/14,055 on main: +540/+46 bytes, within its existing 65,536-byte limit.
