package notation

import "m31labs.dev/cicada/kernel/seq"

// MeterMap places the score's bar and beat lines. A score cannot declare a
// meter yet, so every score is 4/4: the zero map.
func (s *Score) MeterMap() seq.MeterMap { return seq.MeterMap{} }
