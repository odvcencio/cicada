package transcription

import (
	"fmt"
	"math"
	"strings"
)

var pitchClasses = [...]string{"c", "db", "d", "eb", "e", "f", "gb", "g", "ab", "a", "bb", "b"}

var keyProfiles = [2][12]float64{
	{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88},
	{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17},
}

func validateOptions(o Options) error {
	if math.IsNaN(o.Tempo) || math.IsInf(o.Tempo, 0) || o.Tempo != 0 && (o.Tempo < 20 || o.Tempo > 300) {
		return fmt.Errorf("tempo must be 0 (auto) or 20–300 BPM")
	}
	if o.Grid != 4 && o.Grid != 8 && o.Grid != 16 {
		return fmt.Errorf("grid must be 4, 8 or 16 (quarter, eighth or sixteenth notes)")
	}
	if o.Strict != 1 {
		return fmt.Errorf("score rows require strict quantization (strict = 1)")
	}
	if o.Key != "" && o.Key != "auto" {
		_, _, err := parseKey(o.Key)
		return err
	}
	return nil
}

func parseKey(key string) (root, mode int, err error) {
	parts := strings.Fields(strings.ToLower(key))
	if len(parts) != 2 || parts[1] != "major" && parts[1] != "minor" {
		return 0, 0, fmt.Errorf("key must be auto or a tonic and major/minor, such as c major")
	}
	if parts[1] == "minor" {
		mode = 1
	}
	for i, name := range pitchClasses {
		if parts[0] == name {
			return i, mode, nil
		}
	}
	for name, value := range map[string]int{"c#": 1, "d#": 3, "f#": 6, "g#": 8, "a#": 10, "cb": 11, "fb": 4, "e#": 5, "b#": 0} {
		if parts[0] == name {
			return value, mode, nil
		}
	}
	return 0, 0, fmt.Errorf("invalid key tonic")
}

// inferKey correlates duration/confidence-weighted pitch classes with the
// fixed major/minor probe-tone profiles. Ties prefer tonic then major mode.
func inferKey(notes []Note) (string, float64) {
	var hist [12]float64
	for _, n := range notes {
		hist[int(math.Round(n.MIDI))%12] += (n.End - n.Start) * n.Confidence
	}
	best, second, bestRoot, bestMode := -2.0, -2.0, 0, 0
	for root := range 12 {
		for mode := range 2 {
			var meanH, meanP float64
			for i := range 12 {
				meanH += hist[i] / 12
				meanP += keyProfiles[mode][i] / 12
			}
			var cross, varH, varP float64
			for i := range 12 {
				h, p := hist[(i+root)%12]-meanH, keyProfiles[mode][i]-meanP
				cross += h * p
				varH += h * h
				varP += p * p
			}
			correlation := 0.0
			if varH*varP > 0 {
				correlation = cross / math.Sqrt(varH*varP)
			}
			if correlation > best+1e-12 {
				second, best, bestRoot, bestMode = best, correlation, root, mode
			} else if correlation > second {
				second = correlation
			}
		}
	}
	mode := "major"
	if bestMode == 1 {
		mode = "minor"
	}
	confidence := min(1, max(0, best-second)*4) * min(1, float64(len(notes))/8)
	return pitchClasses[bestRoot] + " " + mode, confidence
}

// inferTempo evaluates an onset autocorrelation at candidate beat periods.
// Subdivisions contribute less than whole-beat pairs; a broad preference for
// 100 BPM resolves half/double-time ties without presenting them as certainty.
func inferTempo(notes []Note) (float64, float64) {
	if len(notes) < 3 {
		return 120, 0
	}
	bestTempo, bestScore, second := 120.0, -1.0, -1.0
	for bpm := 40.0; bpm <= 220; bpm += .25 {
		period, score, weight := 60/bpm, 0.0, 0.0
		for i := 1; i < len(notes); i++ {
			for j := max(0, i-8); j < i; j++ {
				beats := (notes[i].Start - notes[j].Start) / period
				if beats < .2 || beats > 8 {
					continue
				}
				whole := math.Abs(beats - math.Round(beats))
				half := math.Abs(beats*2-math.Round(beats*2)) / 2
				value := math.Exp(-.5 * math.Pow(whole/.045, 2))
				value = max(value, .65*math.Exp(-.5*math.Pow(half/.045, 2)))
				w := 1 / float64(i-j)
				score += value * w
				weight += w
			}
		}
		if weight == 0 {
			continue
		}
		score = score/weight - .04*math.Abs(math.Log2(bpm/100))
		if score > bestScore {
			if math.Abs(math.Log2(bpm/bestTempo)) > .08 {
				second = bestScore
			}
			bestScore, bestTempo = score, bpm
		} else if math.Abs(math.Log2(bpm/bestTempo)) > .08 {
			second = max(second, score)
		}
	}
	confidence := min(1, max(0, bestScore-second)*4) * min(1, float64(len(notes)-2)/8)
	return bestTempo, confidence
}

// inferMeter compares recurring accents on three- and four-beat groups.
// Unaccented or short melodies use 4/4 with zero confidence.
func inferMeter(notes []Note, tempo float64) (string, float64) {
	if len(notes) < 8 || (notes[len(notes)-1].Start-notes[0].Start)*tempo/60 < 6 {
		return "4/4", 0
	}
	var scores [2]float64
	for candidate, beats := range []int{3, 4} {
		for phase := range beats {
			var strong, weak, ns, nw float64
			for _, n := range notes {
				position := (n.Start - notes[0].Start) * tempo / 60
				beat := int(math.Round(position))
				if math.Abs(position-float64(beat)) > .15 {
					continue
				}
				if beat%beats == phase {
					strong += float64(n.Velocity)
					ns++
				} else {
					weak += float64(n.Velocity)
					nw++
				}
			}
			if ns >= 2 && nw >= 2 {
				scores[candidate] = max(scores[candidate], (strong/ns-weak/nw)/127)
			}
		}
	}
	meter := "4/4"
	if scores[0] > scores[1]+.02 {
		meter = "3/4"
	}
	return meter, min(1, math.Abs(scores[0]-scores[1])*4)
}
