// Package elastic prepares pitch-preserving audio away from the audio callback.
package elastic

import (
	"context"
	"fmt"
	"math"
)

// Anchor maps a source frame to its destination frame. Successive anchors
// describe constant-rate regions; both coordinates are exclusive at the end.
type Anchor struct{ Source, Target int64 }

type Config struct {
	Rate                       int
	SourceFrames, TargetFrames int64
	Anchors                    []Anchor
}

// Reader and Writer transfer mono PCM in bounded blocks. Render calls Reader
// only inside SourceFrames and writes every destination frame exactly once.
type Reader func(context.Context, int64, []float32) error
type Writer func(context.Context, int64, []float32) error

func (c Config) validate() error {
	if c.Rate < 8000 || c.Rate > 192000 || c.SourceFrames < 1 || c.TargetFrames < 1 || c.TargetFrames > 16<<20 || len(c.Anchors) < 2 || len(c.Anchors) > 4096 {
		return fmt.Errorf("elastic audio needs a supported rate, bounded duration and at least two anchors")
	}
	if c.Anchors[0] != (Anchor{}) || c.Anchors[len(c.Anchors)-1] != (Anchor{c.SourceFrames, c.TargetFrames}) {
		return fmt.Errorf("elastic anchors must cover the complete source and destination")
	}
	for i := 1; i < len(c.Anchors); i++ {
		a, b := c.Anchors[i-1], c.Anchors[i]
		s, t := b.Source-a.Source, b.Target-a.Target
		if s <= 0 || t <= 0 || float64(t)/float64(s) < .5 || float64(t)/float64(s) > 2 {
			return fmt.Errorf("elastic anchors must increase; each stretch must be between half and double duration")
		}
	}
	return nil
}

// Render uses waveform-similarity overlap/add with a shared musical-time map.
// Source PCM is never resampled: grain alignment changes timing while retaining
// pitch. Its working memory depends on sample rate, not recording duration.
func Render(ctx context.Context, c Config, read Reader, write Writer) error {
	if err := c.validate(); err != nil {
		return err
	}
	if read == nil || write == nil {
		return fmt.Errorf("elastic audio needs PCM readers and writers")
	}
	identity := true
	for _, a := range c.Anchors {
		identity = identity && a.Source == a.Target
	}
	if identity {
		block := make([]float32, 4096)
		for at := int64(0); at < c.TargetFrames; {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(int64(len(block)), c.TargetFrames-at)
			if err := read(ctx, at, block[:n]); err != nil {
				return err
			}
			if err := finite(block[:n]); err != nil {
				return err
			}
			if err := write(ctx, at, block[:n]); err != nil {
				return err
			}
			at += n
		}
		return nil
	}
	hop := max(32, c.Rate/100)
	grain, search := 4*hop, hop
	acc, weight := make([]float64, grain), make([]float64, grain)
	window, cache := make([]float64, grain), make([]float32, grain+2*search)
	output := make([]float32, hop)
	for i := range window {
		s := math.Sin(math.Pi * (float64(i) + .5) / float64(grain))
		window[i] = s * s
	}
	anchor := 1
	for at := int64(0); at < c.TargetFrames; at += int64(hop) {
		if err := ctx.Err(); err != nil {
			return err
		}
		for anchor < len(c.Anchors)-1 && at >= c.Anchors[anchor].Target {
			anchor++
		}
		a, b := c.Anchors[anchor-1], c.Anchors[anchor]
		expected := a.Source + int64(math.Round(float64(at-a.Target)*float64(b.Source-a.Source)/float64(b.Target-a.Target)))
		base := expected - int64(search)
		clear(cache)
		lo, hi := max(int64(0), base), min(c.SourceFrames, base+int64(len(cache)))
		if hi > lo {
			if err := read(ctx, lo, cache[lo-base:hi-base]); err != nil {
				return err
			}
		}
		if err := finite(cache); err != nil {
			return err
		}
		best, bestScore := search, math.Inf(-1)
		// Coarse correlation followed by a frame-accurate search avoids an
		// O(window*search) pass for every grain of a long recording.
		score := func(start int) float64 {
			var dot, x2, y2 float64
			for j := 0; j < grain-hop; j += 4 {
				if weight[j] < 1e-9 {
					continue
				}
				x, y := acc[j]/weight[j], float64(cache[start+j])
				dot += x * y
				x2 += x * x
				y2 += y * y
			}
			if x2*y2 < 1e-18 {
				return -math.Abs(float64(start-search)) / float64(search)
			}
			return dot/math.Sqrt(x2*y2) - .001*math.Abs(float64(start-search))/float64(search)
		}
		if at > 0 {
			step := max(1, c.Rate/3000)
			for start := 0; start <= 2*search; start += step {
				if base+int64(start) < 0 || base+int64(start) >= c.SourceFrames {
					continue
				}
				if v := score(start); v > bestScore {
					best, bestScore = start, v
				}
			}
			center := best
			for start := max(0, center-step); start <= min(2*search, center+step); start++ {
				if base+int64(start) < 0 || base+int64(start) >= c.SourceFrames {
					continue
				}
				if v := score(start); v > bestScore {
					best, bestScore = start, v
				}
			}
		}
		for j, w := range window {
			acc[j] += float64(cache[best+j]) * w
			weight[j] += w
		}
		n := int(min(int64(hop), c.TargetFrames-at))
		for j := 0; j < n; j++ {
			output[j] = float32(acc[j] / weight[j])
		}
		if err := write(ctx, at, output[:n]); err != nil {
			return err
		}
		copy(acc, acc[hop:])
		clear(acc[grain-hop:])
		copy(weight, weight[hop:])
		clear(weight[grain-hop:])
	}
	return nil
}

func finite(values []float32) error {
	for _, v := range values {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return fmt.Errorf("elastic source contains non-finite PCM")
		}
	}
	return nil
}
