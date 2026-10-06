// The model uses original registrations and an equal-tempered shared wheel
// bank covering pitches 24..114. Drawbar harmonics fold by octaves at the bank
// boundaries. It supports notes 21..108 with eight allocated note slots;
// physical key state remains separate from allocation, so stealing a voice
// cannot spuriously rearm the single-trigger percussion circuit.
//
// The scanner is an 18-stage dispersive all-pass line with a smoothly scanned
// tap at 6.9 Hz. V1..V3 select wet-only depths 5, 10, and 17 stages; C1..C3 mix
// dry and wet equally. This is a circuit-inspired design, not an exact clone
// of a particular historical scanner. Pickup leakage and adjacent-wheel
// crosstalk use independently adjustable normalized levels.
//
// The rotating speaker separates bands at 800 Hz. The horn rotates at
// 0.77/6.7 Hz and the drum at 0.66/6.0 Hz; their directions oppose. First-order
// rise/fall time constants are 0.9/1.3 s for the horn and 3.3/4.2 s for the
// drum. Independent amplitude and fractional-delay modulation provide
// directionality and Doppler shift. MicSpread controls the angle between
// stereo microphones. RotaryMix crossfades the direct and rotating signal.
//
// PercussionSecond and PercussionThird use the corresponding shared wheel
// pitches, cancel the 1-foot drawbar, and decay by 60 dB in 3.45 s (slow) or
// 0.725 s (fast). Soft reduces the transient; normal slightly reduces drawbar
// gain. Key velocity affects contact click, not sustained organ amplitude.
// Sustain is a modern optional hold switch, with threshold 0.5; it does not
// affect percussion rearming. Gain is a linear output multiplier (0..2).
package organ
