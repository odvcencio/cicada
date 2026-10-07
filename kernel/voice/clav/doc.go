// Clav is an original struck-string design. Modal string displacement comes
// from a tangent impulse; damping yarn progressively damps higher modes as
// Mute increases. Two spatial pickups sample the same modes at .12 and .72
// of string length. Their voltage follows modal velocity, the derivative of
// magnetic flux. Bridge, Neck, Both and Difference select their wiring.
// Release adds deterministic contact noise and engages string damping.
// A mild preamp and output low-pass follow the pickup signal.
//
// Params: Mute/Click/Drive [0,1], Tangent [.05,.45] of string length, Gain
// [0,2]. The clav, clav_muted and clav_hollow patches are original designs.
// No samples or factory patch data are embedded. New prepares all 88 keys
// at 44.1, 48, 96 or 192 kHz. Audio calls allocate nothing; eight voices use
// oldest stealing and a 3 ms residual fade. A single owner serializes calls.
// The clav_wasm gate compares float32 bits across rates/block sizes and
// checks TinyGo allocation telemetry. Score integration is a separate layer.
package clav
