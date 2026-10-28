package recording

import (
	"fmt"
	"math"
	"slices"
	"sort"
)

// Options controls automatic slicing. Root is the fallback for unpitched hits;
// detected musical notes get individual roots when AutoPitch is enabled.
//
// Ringing is for drums and cymbals, whose tails outlast the usual 120 ms
// minimum between hits and swell again after the first impact. Hits must then
// be at least half a second apart. A hit starts only where the energy doubles
// within 10 ms and, within the next 20 ms while it is still climbing, rises 1.5
// times above its loudest level between 100 and 10 ms before the doubling, so
// that a tail beating against itself does not look like a new hit. That energy
// is smoothed over 10 ms, so it finds a hit a few milliseconds after it began.
// A slice starts 3 ms before the 2 ms window in which the hit began, and the
// slice before it ends there, so a cut tail does not end in the next attack.
// The room's noise is read from the quietest 200 ms of each recording;
// TakeReport.NoRoomTone says when the recording does not open with room tone,
// where that stretch may be a tail. A hit keeps its tail until the energy is 60
// dB below the recording's loudest hit, or twice the noise floor when that is
// higher. If the next hit or the end of the recording comes first, the tail is
// cut there and fades out over 5 ms; the hit is marked Cut when the tail was
// still more than 6 dB above that level.
type Options struct {
	Root      int  `json:"root"`
	Layers    int  `json:"layers"`
	AutoPitch bool `json:"autoPitch"`
	Ringing   bool `json:"ringing,omitempty"`
}

func DefaultOptions() Options { return Options{Root: 60, Layers: 3} }

type Hit struct {
	Input        int     `json:"input"`
	Start        int     `json:"start"`
	End          int     `json:"end"`
	Rate         int     `json:"rate"`
	LoudnessDB   float64 `json:"loudnessDB"`
	Peak         float64 `json:"peak"`
	PitchHz      float64 `json:"pitchHz"`
	Confidence   float64 `json:"confidence"`
	Root         int     `json:"root"`
	TuneCents    float64 `json:"tuneCents"`
	SourceSHA256 string  `json:"sourceSHA256"`
	// Cut is set in ringing mode when the next hit, or the end of the
	// recording, ended this hit while its tail was still more than 6 dB above
	// the level at which tails count as finished. CutDB is the tail's level at
	// the cut, in dBFS of the source recording.
	Cut   bool      `json:"cut,omitempty"`
	CutDB float64   `json:"cutDB,omitempty"`
	PCM   []float32 `json:"-"`
}

// TakeReport describes what AnalyzeTakes measured in one recording besides its
// hits. Levels are in dBFS of the source recording.
type TakeReport struct {
	// LoudestDB is the loudest 10 ms of the recording.
	LoudestDB float64
	// NoiseDB is the room's noise level. In ringing mode it is the quietest
	// 200 ms of the recording, so it reads the tail of the last hit when the
	// recording has no quiet stretch.
	NoiseDB float64
	// NoRoomTone is set in ringing mode when the recording does not open with
	// 200 ms of room tone: a stretch as quiet as the quietest 200 ms, before
	// the first sound that stands clear of the room. The quietest 200 ms then
	// stands in for the room, and it may be a tail that was still ringing when
	// the recording stopped.
	NoRoomTone bool
	// NoQuiet is set in ringing mode when the quietest 200 ms lies within
	// 26 dB of the loudest sound and cannot be read as the room. Either the
	// recording does not open with room tone (NoRoomTone), so that stretch may
	// be a ring that never faded, or it does and the room itself is within
	// 20 dB of the loudest sound, so the hits do not stand clear of it. The
	// command then assumes a floor 26 dB below the loudest sound to find the
	// hits, and ends tails where they reach the room (with room tone) or 20 dB
	// below the loudest sound (without).
	NoQuiet bool
	// QuietDB is the level below which a tail counts as finished.
	QuietDB float64
	// Merged counts attacks that came after a hit but sooner than the minimum
	// gap, half a second in ringing mode. They are kept inside that hit.
	Merged int
}

// Ringing-mode measurements. Times are in milliseconds.
const (
	ringGapMS    = 500 // least time between two hits
	ringSwellMS  = 120 // a rise sooner than this after a hit is its own swell
	ringRoomMS   = 200 // the quietest stretch of this length sets the noise floor
	ringRecentMS = 100 // a new hit must beat the loudest energy from this long before it until 10 ms before it
	cutFadeMS    = 5   // a hit that is cut off fades out over this time

	ringRiseSpanMS     = 20  // a hit has this long after its rise starts to beat the recent maximum
	ringRiseGapMS      = 10  // rises closer together than this belong to one surge of energy
	ringRiseFactor     = 2   // a rise is energy this many times above its quietest level in the previous 10 ms
	ringStepFactor     = 2   // a hit begins in the 2 ms window that is this many times louder than the 12 ms before it
	ringNearMinimum    = 1.1 // energy this close to its lowest is as quiet, for finding where a hit began
	ringRiseOverRecent = 1.5 // a new hit must beat the recent maximum by this factor
	ringNoiseCap       = .05 // the noise floor is read as at most this fraction of the loudest energy
	ringCutMargin      = 2   // a cut tail is reported when it is this many times above the level where tails finish
	ringLeadInOverRoom = 1.5 // a lead-in is room tone when its quietest stretch is within this factor of the recording's
	ringLeadInSound    = 3   // the first sound of a recording is this many times louder than the room
	ringCloseRoom      = .1  // a room louder than this fraction of the loudest energy leaves the hits too little to stand on
)

// dB converts an amplitude to dBFS, flooring silence.
func dB(x float64) float64 { return 20 * math.Log10(max(1e-12, x)) }

// mul rounds a product to float64 before anything is added to it. Go lets a
// compiler fuse x*y+z into one instruction with a different last digit, and
// arm64 and GOAMD64=v3 builds do; a conversion forbids it, so every machine
// computes the same energies, pitches and checksums.
func mul(x, y float64) float64 { return float64(x * y) }

// Analyze finds attacks above the measured noise floor and trims their tails.
// A short rising-energy test separates taps even when a previous tail rings.
func Analyze(inputs []Audio, options Options) ([]Hit, error) {
	hits, _, err := AnalyzeTakes(inputs, options)
	return hits, err
}

// AnalyzeTakes does what Analyze does and also reports, for each input, what it
// measured beyond the hits: the noise floor, the level where tails count as
// finished and the attacks it folded into an earlier hit.
func AnalyzeTakes(inputs []Audio, options Options) ([]Hit, []TakeReport, error) {
	if len(inputs) == 0 || len(inputs) > 32 || options.Root < 12 || options.Root > 95 || options.Layers < 1 || options.Layers > 8 {
		return nil, nil, fmt.Errorf("choose 1–32 recordings, a root from 12–95, and 1–8 layers")
	}
	var hits []Hit
	reports := make([]TakeReport, 0, len(inputs))
	total := 0
	for index, a := range inputs {
		total += len(a.PCM)
		if a.Rate < 8000 || a.Rate > 192000 || len(a.PCM) == 0 {
			return nil, nil, fmt.Errorf("recording exceeds rate or total frame limit")
		}
		if total > MaxFrames {
			return nil, nil, frameBudgetError(inputs)
		}
		for _, x := range a.PCM {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) || math.Abs(float64(x)) > 8 {
				return nil, nil, fmt.Errorf("recording contains invalid PCM")
			}
		}
		hop := max(1, a.Rate/500)
		energy := make([]float64, (len(a.PCM)+hop-1)/hop)
		for w := range energy {
			var e float64
			end := min(len(a.PCM), (w+1)*hop)
			for _, x := range a.PCM[w*hop : end] {
				e += mul(float64(x), float64(x))
			}
			energy[w] = math.Sqrt(e / float64(end-w*hop))
		}
		// Smooth over 10 ms so low notes do not create attacks at each zero
		// crossing. Keep the raw source coordinates for slicing.
		rawEnergy := append([]float64(nil), energy...)
		for w := range energy {
			var sum float64
			from := max(0, w-4)
			for _, e := range rawEnergy[from : w+1] {
				sum += mul(e, e)
			}
			energy[w] = math.Sqrt(sum / float64(w-from+1))
		}
		loudest := slices.Max(energy)
		report := TakeReport{LoudestDB: dB(loudest)}
		var noise float64
		// floor is the level that tails are measured against: the room's, when
		// the recording shows it.
		var floor float64
		if options.Ringing {
			// Tails can fill much of a ringing take, so a percentile of the
			// whole take reads a tail, not the room. The quietest stretch is the
			// room, as long as the take holds one.
			room := max(1, ringRoomMS*a.Rate/1000/hop)
			noise = quietestStretch(energy, room)
			report.NoiseDB = dB(noise)
			floor = noise
			report.NoRoomTone = !startsWithRoomTone(energy, loudest, room, noise)
			// A quietest stretch within 26 dB of the loudest sound may be a tail
			// that never faded. It is the room all the same when the recording
			// starts as quiet, the room tone before the first hit, unless the
			// room is so loud that the hits barely stand clear of it. Then the
			// hits are found against an assumed floor, and tails end at the room.
			if limit := loudest * ringNoiseCap; noise > limit && (report.NoRoomTone || noise > loudest*ringCloseRoom) {
				noise, report.NoQuiet = limit, true
				if report.NoRoomTone {
					floor = limit
				}
			}
		} else {
			ordered := append([]float64(nil), energy...)
			sort.Float64s(ordered)
			noise = ordered[len(ordered)/5]
			report.NoiseDB = dB(noise)
		}
		threshold := max(.0008, max(noise*6, loudest*.10))
		quietLevel := max(threshold*.3, noise*2)
		if options.Ringing {
			quietLevel = max(threshold*.01, floor*2)
		}
		report.QuietDB = dB(quietLevel)
		gap := a.Rate * 120 / 1000
		if options.Ringing {
			gap = a.Rate * ringGapMS / 1000
		}
		swell := a.Rate * ringSwellMS / 1000
		recent := max(1, ringRecentMS*a.Rate/1000/hop)
		span := max(1, ringRiseSpanMS*a.Rate/1000/hop)
		surge := max(1, ringRiseGapMS*a.Rate/1000/hop)
		var onsets []int
		last := -a.Rate
		wasAttack := false
		// A hit that lands on a loud ring rises first and beats the ring a little
		// later, and its own energy then enters the recent maximum it must beat.
		// So a rise arms the detector and fixes the recent maximum as it stood
		// before the rise; the hit then has the span to beat that, while its
		// energy is still climbing. A ring that beats slowly climbs past a stale
		// maximum too, but it does not double within 20 ms once it is out of its
		// null. The hit began where the surge of energy that carried it over the
		// maximum began (surgeFrom), which is not the arming rise when a bump in
		// the ring came first.
		armedAt, armedRef := -span-1, 0.0
		roseAt, surgeFrom := -surge-1, 0
		for w, e := range energy {
			frame := w * hop
			attack := false
			if options.Ringing {
				floor, climb := 0.0, 0.0
				if w > 0 {
					floor = slices.Min(energy[max(0, w-5):w])
					climb = slices.Min(energy[max(0, w-span):w])
				}
				if e > floor*ringRiseFactor {
					if w-roseAt > surge {
						surgeFrom = w
					}
					roseAt = w
					if w-armedAt > span {
						// The 10 ms before this window belong to the rise itself,
						// which the floor test covers; the maximum looks further back.
						armedAt, armedRef = w, 0
						if from, to := max(0, w-recent), w-5; from < to {
							armedRef = slices.Max(energy[from:to])
						}
					}
				}
				attack = w-armedAt <= span && e > threshold && e > armedRef*ringRiseOverRecent && e > climb*ringRiseFactor
			} else if e > threshold {
				previous := 0.0
				if w > 0 {
					previous = energy[w-1]
				}
				attack = previous < threshold || e > max(threshold, previous*2.5)
			}
			switch {
			case attack && frame-last >= gap:
				if options.Ringing {
					// The smoothed energy shows the surge a few windows after it
					// began. Look back through the 2 ms energy for the window in
					// which the hit began: the first that is well above the 12 ms
					// before it.
					begin, found := surgeFrom, false
					for k := max(0, surgeFrom-4); k <= w && !found; k++ {
						if before := rawEnergy[max(0, k-6):k]; len(before) == 0 || rawEnergy[k] > slices.Max(before)*ringStepFactor {
							begin, found = k, true
						}
					}
					// A swell has no such window, since each step of it is small, and
					// neither has a hit that lands on a ring. The energy doubled within
					// the span before the hit was found, and the hit began where the
					// energy was lowest in that time.
					if !found {
						begin = lastNearMinimum(energy, max(0, w-span), w)
					}
					frame = begin * hop
				}
				onsets = append(onsets, frame)
				last = w * hop
			case attack && !wasAttack && frame-last >= swell:
				// A new attack too soon after a hit: it stays inside that hit.
				report.Merged++
			}
			wasAttack = attack
		}
		reports = append(reports, report)
		for j, onset := range onsets {
			limit := len(a.PCM)
			if j+1 < len(onsets) {
				limit = onsets[j+1]
				if options.Ringing {
					// Leave out the lead-in of the next hit.
					limit -= a.Rate * 3 / 1000
				}
			}
			start := max(0, onset-a.Rate*3/1000)
			end := limit
			quiet := 0
			finished := false
			for w := onset / hop; w < len(energy) && w*hop < limit; w++ {
				if energy[w] < quietLevel {
					quiet++
				} else {
					quiet = 0
				}
				if quiet >= 15 && w*hop-onset > a.Rate*30/1000 {
					end = min(limit, (w-quiet+2)*hop+a.Rate*5/1000)
					finished = true
					break
				}
			}
			if end-start < a.Rate/100 {
				continue
			}
			hit := Hit{Input: index, Start: start, End: end, Rate: a.Rate, Root: options.Root, SourceSHA256: a.SHA256, PCM: append([]float32(nil), a.PCM[start:end]...)}
			// The next hit or the end of the recording ended this one before its
			// tail finished.
			forced := options.Ringing && !finished
			if forced {
				// Read the tail's level over the last 12 ms, whose minimum leaves
				// out the rise of a next hit.
				lastWindow := min(len(energy)-1, (end-1)/hop)
				if level := slices.Min(energy[max(0, lastWindow-5) : lastWindow+1]); level >= quietLevel*ringCutMargin {
					hit.Cut, hit.CutDB = true, dB(level)
				}
			}
			var dc float64
			for _, x := range hit.PCM {
				dc += float64(x)
			}
			dc /= float64(len(hit.PCM))
			var power float64
			measure := min(len(hit.PCM), a.Rate*40/1000)
			for i, x := range hit.PCM {
				y := float64(x) - dc
				hit.PCM[i] = float32(y)
				hit.Peak = max(hit.Peak, math.Abs(y))
				if i < measure {
					power += mul(y, y)
				}
			}
			if hit.Peak < threshold {
				continue
			}
			hit.LoudnessDB = dB(math.Sqrt(power / float64(measure)))
			hit.PitchHz, hit.Confidence = estimatePitch(hit.PCM, a.Rate)
			if options.AutoPitch && hit.Confidence >= .8 && hit.PitchHz > 0 {
				midi := 69 + mul(12, math.Log2(hit.PitchHz/440))
				hit.Root = max(12, min(95, int(math.Round(midi))))
				hit.TuneCents = (float64(hit.Root) - midi) * 100
			}
			// Fade in over half a millisecond. A hit that was cut off fades out
			// over a few milliseconds, so the cut does not click; otherwise the
			// tail has already died away and half a millisecond is enough.
			fade := min(len(hit.PCM)/2, max(1, a.Rate/2000))
			fadeOut := fade
			if forced {
				fadeOut = min(len(hit.PCM)/2, max(1, a.Rate*cutFadeMS/1000))
			}
			for i := range hit.PCM {
				gain := .9 / hit.Peak
				if i < fade {
					gain *= float64(i) / float64(fade)
				}
				if len(hit.PCM)-1-i < fadeOut {
					gain *= float64(len(hit.PCM)-1-i) / float64(fadeOut)
				}
				hit.PCM[i] *= float32(gain)
			}
			hits = append(hits, hit)
			if len(hits) > 256 {
				return nil, nil, fmt.Errorf("recording contains more than 256 hits")
			}
		}
	}
	if len(hits) == 0 {
		return nil, nil, fmt.Errorf("no hits detected; record closer to the microphone or drop a WAV with clear attacks")
	}
	return hits, reports, nil
}

// quietestStretch returns the RMS of the quietest run of n consecutive values.
func quietestStretch(energy []float64, n int) float64 {
	n = max(1, min(n, len(energy)))
	best := math.Inf(1)
	for from := 0; from+n <= len(energy); from++ {
		var sum float64
		for _, e := range energy[from : from+n] {
			sum += mul(e, e)
		}
		best = min(best, sum)
	}
	return math.Sqrt(best / float64(n))
}

// startsWithRoomTone reports whether a recording opens with a stretch of n
// values as quiet as noise, its quietest stretch anywhere. The recording opens
// where the first sound begins: the first value within 20 dB of the loudest
// that also stands clear of the room, which a room louder than that would
// otherwise hide. A recording that starts with a hit, or in the middle of a
// ring, has no such stretch.
func startsWithRoomTone(energy []float64, loudest float64, n int, noise float64) bool {
	sound := max(loudest*.10, noise*ringLeadInSound)
	first := slices.IndexFunc(energy, func(e float64) bool { return e > sound })
	return first >= n && quietestStretch(energy[:first], n) <= noise*ringLeadInOverRoom
}

// lastNearMinimum returns the last index in [from, to] whose value is within
// ringNearMinimum of the lowest value there. Where the energy is steady, the
// lowest value is whichever window the noise made lowest; the last window near
// it is where the energy stopped being steady.
func lastNearMinimum(energy []float64, from, to int) int {
	low := slices.Min(energy[from : to+1])
	for k := to; k > from; k-- {
		if energy[k] <= low*ringNearMinimum {
			return k
		}
	}
	return from
}

// frameBudgetError says how far a set of recordings is over the frame limit.
func frameBudgetError(inputs []Audio) error {
	frames, seconds := 0, 0.0
	for _, a := range inputs {
		frames += len(a.PCM)
		seconds += float64(len(a.PCM)) / float64(max(1, a.Rate))
	}
	return fmt.Errorf("the recordings hold %d frames (%.0f s), over the limit of %d frames in all (%.0f s at 44.1 kHz, %.0f s at 48 kHz); use fewer or shorter recordings", frames, seconds, MaxFrames, float64(MaxFrames)/44100, float64(MaxFrames)/48000)
}

// Normalized autocorrelation uses a downsampled, bounded 80 ms window. The
// first strong local maximum avoids octave-down estimates on tonal notes.
func estimatePitch(pcm []float32, rate int) (float64, float64) {
	stride := max(1, rate/12000)
	effective := float64(rate) / float64(stride)
	start := min(len(pcm)/8, rate*4/1000)
	n := min((len(pcm)-start)/stride, int(effective*.08))
	if n < 64 {
		return 0, 0
	}
	x := make([]float64, n)
	var mean float64
	for i := range x {
		for j := 0; j < stride; j++ {
			x[i] += float64(pcm[start+i*stride+j]) / float64(stride)
		}
		mean += x[i]
	}
	mean /= float64(n)
	for i := range x {
		x[i] -= mean
	}
	low, high := max(2, int(effective/2000)), min(n/2, int(effective/60))
	correlation := make([]float64, high+2)
	for lag := low; lag <= high+1; lag++ {
		var dot, a, b float64
		for i := 0; i < n-lag; i++ {
			dot += mul(x[i], x[i+lag])
			a += mul(x[i], x[i])
			b += mul(x[i+lag], x[i+lag])
		}
		if a*b > 1e-20 {
			correlation[lag] = dot / math.Sqrt(a*b)
		}
	}
	best := low
	for lag := low + 1; lag <= high; lag++ {
		if correlation[lag] > correlation[best] {
			best = lag
		}
	}
	for lag := low + 1; lag < high; lag++ {
		if correlation[lag] >= max(.8, correlation[best]*.95) && correlation[lag] >= correlation[lag-1] && correlation[lag] > correlation[lag+1] {
			best = lag
			break
		}
	}
	a, b, c := correlation[max(low, best-1)], correlation[best], correlation[best+1]
	delta := 0.0
	if denominator := a - 2*b + c; math.Abs(denominator) > 1e-12 {
		delta = max(-.5, min(.5, .5*(a-c)/denominator))
	}
	if b < .35 {
		return 0, max(0, b)
	}
	return effective / (float64(best) + delta), min(1, max(0, b))
}
