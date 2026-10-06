# Crossfaded-loop playback

Crossfaded loops now use direct planar PCM reads when the entire FIR window
lies in a contiguous part of the recording. Windows that touch the fade tail,
wrapped head, region boundary or loop seam keep the existing per-frame mapping.
The unwrapped attack can use the direct path too.

The filter banks, coefficient interpolation, ascending tap order and explicit
rounding are unchanged. A one-semitone upward transposition still uses 108 taps.
This change does not alter loop points, crossfade samples or output PCM.

The boundary test compares 1,968,000 stereo results against the generic mapping
path, with identical float64 bits in both channels. It covers mono and stereo,
five fade lengths, first-pass and wrapped playback, every source position,
four fractional phases and twelve ratios from 0.125 through 8. The same test
passes with native FMA enabled. The TinyGo fixture now includes larger mono and
stereo crossfaded loops at 48 and 96 kHz; all 270,336 native/WASM sample values
match exactly across 64-, 128- and 256-frame render blocks. Trigger, reset,
render and release tests report zero allocations.

Actual owned multisample banks were loaded through the pinned instrument-pack
loader and measured with `sample.Instrument.NextStereo`: 48 kHz, 128-frame
blocks, velocity 102, exactly one or eight sustained voices, captured roots
48 + 3n or one semitone above those roots. Loop instruments warmed for three
seconds, then ran continuously through their seams. Loading, triggering,
polyphony checks and finite-recording resets were outside timed rendering.
The banks have 30 roots, five velocity layers and two round robins.

Measured on an Intel Core Ultra 9 285 with Go 1.25.1 and GOMAXPROCS=2:

| Bank | Voices | Transposed ns/frame before | Transposed ns/frame after | After ns/frame/voice | After ms/128 frames |
| --- | ---: | ---: | ---: | ---: | ---: |
| Tine EP | 1 | 154.22 | 133.47 | 133.47 | 0.0171 |
| Tine EP | 8 | 1116.99 | 992.21 | 124.03 | 0.1270 |
| Tonewheel Organ | 1 | 1001.90 | 456.44 | 456.44 | 0.0584 |
| Tonewheel Organ | 8 | 8166.34 | 3667.92 | 458.49 | 0.4695 |
| Soft Pad | 1 | 1050.93 | 560.06 | 560.06 | 0.0717 |
| Soft Pad | 8 | 8333.68 | 3428.71 | 428.59 | 0.4389 |

Eight captured-root voices measured 114.54 ns/frame for Tine EP, 109.10 for
Tonewheel Organ and 138.89 for Soft Pad. Captured roots bypass FIR, and finite
EP recordings already used contiguous FIR reads, so timing changes in those
cases reflect load variation rather than this optimization. The before and
after runs used the same benchmark on a busy machine; they are elapsed native
costs, not browser CPU measurements or guaranteed speedup ratios.

The representative banks report zero render allocations, zero event/reset/
release allocations and zero bytes per render block. Long fades still require
the generic mapping path and can dominate transposed playback. Browser
performance must be measured separately; no quality gate or size budget was
changed to obtain these timings.

The standalone sampler module is 32,056 bytes, up 31 bytes from 32,025;
Brotli size remains 13,933 bytes. It fits the existing 65,536-byte sampler
limit. The core kernel is unchanged at 305,983 bytes raw and 93,630 bytes
Brotli, within its 327,680/122,880-byte limits. The AudioWorklet remains
5,114/5,120 bytes.
