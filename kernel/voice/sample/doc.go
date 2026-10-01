// Playback and SRC contract
//
// Region PCM is planar, caller-owned and immutable. All methods run on one
// audio owner. Constructors and package init run before the callback. Output
// rates are 44100, 48000 and 96000 Hz; source rates are 8000..192000 Hz. Forward
// playback increments are admitted in [0.125,8], including the source/output
// rate ratio. Quality qualification covers the ratios in quality_test.go;
// admission of another ratio does not itself qualify its quality or CPU cost.
//
// At ratio 1, unity gain and velocity 127, Voice copies the exact float32 bits
// until a release, gain change or retained retrigger tail applies. Natural
// one-shot completion appends a held-output fade rather than changing source
// frames. NoteOff and the held tail last at most 2 ms; retriggers keep at most
// one tail per slot. Basic loops have no crossfade and can click if endpoints
// differ. The symmetric SRC reads future PCM, with zero padding at region
// bounds and periodic wrapping at loop bounds; it requires the whole region.
//
// SRC uses Kaiser-windowed sinc (beta 12), 1024 phases plus an endpoint, and
// linear interpolation between coefficient rows. Bank upper ratios/taps are
// 1/96, 1.125/108, 1.25/120, 1.5/144, 2/192, 3/288, 4/384 and 8/768. Select the
// first upper ratio >= playback increment, with a 1e-14 relative tolerance
// for pitch rounding at bank boundaries. In source cycles/frame, passband
// ends at .38/bankRatio, cutoff is .45/bankRatio and stopband starts at
// .5/bankRatio. The conservative bank choice narrows bandwidth between banks.
// The eight shared float32 tables occupy 8,610,000 bytes, generated once at
// package init rather than embedded in the download. No coefficient generation
// or math library calls occur in Render or NextStereo.
//
// One float64 phase advances once per output frame; it is never reconstructed
// at block boundaries. Gain smoothing also advances per frame. Coefficients
// are stored as float32, promoted before row interpolation; coefficient
// interpolation products and source products explicitly round to float64
// before addition. Taps accumulate in ascending order, gain applies in
// float64, and the voice output rounds once to float32. Exact table phases
// skip coefficient interpolation with the same tap order. Declick tails
// multiply held float32 outputs in float64, add to the rounded voice output,
// and round to float32 again. Pool accumulation uses ascending slot order in
// float64 and then rounds to float32. These explicit
// rounding boundaries prevent native FMA contraction across multiply/add
// operations. Loop wrapping uses a rounded float64 integer-times-length
// product before subtraction. Pitch math occurs only on NoteOn. See the
// standalone WASM fixture test for native/TinyGo verification.
//
// Reproduce quality with go test ./kernel/voice/sample -run SRC -count=1 -v;
// reproduce CPU percentiles with go run ./cmd/cicada-sample-metrics. Use
// go test -tags sample_wasm ./kernel/voice/sample to verify native/TinyGo bits.
// Use GOWORK=off, nice -n 10 and the local-heavy.lock for these commands. The tools
// print METRIC lines. Quality excludes startup/end transients, fits clean
// fundamentals by least squares, and measures total residual rather than
// hiding aliases in an FFT-bin exclusion window. See quality_test.go for the
// exact tone sweep, measurement length and pass/fail gates.
package sample
