package render

import (
	"fmt"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/notation"
)

type packVoice struct {
	baseline   []baselinePool
	activePool int
	instrument *sample.Instrument
	handle     sample.Handle
	fault      error
}

func (v *packVoice) NoteOn(note, velocity uint8, _ bool, slide bool) {
	if len(v.baseline) > 0 {
		for i, b := range v.baseline {
			if note >= b.lo && note <= b.hi {
				v.baseline[v.activePool].pool.NoteOff(v.handle)
				v.activePool = i
				v.handle, v.fault = b.pool.NoteOn(note, velocity)
				return
			}
		}
		v.fault = fmt.Errorf("sample note has no baseline zone")
		return
	}
	if v.fault != nil {
		return
	}
	if slide && v.instrument != nil && v.instrument.Legato(v.handle, note, 0) == nil {
		return
	}
	v.instrument.NoteOff(v.handle)
	v.handle, v.fault = v.instrument.NoteOn(note, velocity)
}
func (v *packVoice) NoteOff() {
	if len(v.baseline) > 0 {
		v.baseline[v.activePool].pool.NoteOff(v.handle)
		return
	}
	v.instrument.NoteOff(v.handle)
}
func (v *packVoice) Next() float32 { l, r := v.NextStereo(); return (l + r) * .5 }
func (v *packVoice) NextStereo() (float32, float32) {
	if len(v.baseline) > 0 {
		var sumL, sumR float64
		var l, r [1]float32
		for _, b := range v.baseline {
			b.pool.Render(l[:], r[:])
			sumL += float64(l[0])
			sumR += float64(r[0])
		}
		return float32(sumL), float32(sumR)
	}
	return v.instrument.NextStereo()
}

func preparePackVoices(score *notation.Score, opts Options) (map[string]*instrumentpack.Prepared, error) {
	banks := map[string]*instrumentpack.Prepared{}
	for _, s := range score.Samplers {
		if s.Pack == "" {
			continue
		}
		dir := opts.AssetDir
		if dir == "" {
			dir = "."
		}
		p, err := instrumentpack.LoadAtRate(dir, s.Pack, s.SHA256, opts.SampleRate)
		if err != nil {
			return nil, fmt.Errorf("sampler %s: %w", s.Name, err)
		}
		p.Manifest.Config.Voices = s.Voices
		p.Manifest.Config.Humanize.Seed = score.Seed
		banks[s.Name] = p
	}
	return banks, nil
}

// The baseline uses the existing single-region Pool DSP, with the same key
// map and one fixed recorded layer/take. It retains the original 2 ms release.
type baselinePool struct {
	lo, hi uint8
	pool   *sample.Pool
}

func legacyBaseline(p *instrumentpack.Prepared, rate int) (*packVoice, error) {
	var zones []sample.Zone
	for _, z := range p.Zones {
		if z.Release || z.Position != 0 {
			continue
		}
		found := -1
		for i, o := range zones {
			if o.Group == z.Group {
				found = i
				break
			}
		}
		if found < 0 {
			zones = append(zones, z)
		} else if abs(int(z.Layer)-88) < abs(int(zones[found].Layer)-88) {
			zones[found] = z
		}
	}
	v := &packVoice{}
	for _, z := range zones {
		z.Region.Crossfade = 0
		pool, err := sample.NewPool(rate, p.Manifest.Config.Voices, z.Region)
		if err != nil {
			return nil, err
		}
		if err = pool.SetParams(sample.Params{Gain: p.Manifest.Config.Gain * z.Gain, FineTuneCents: p.Manifest.Config.TuneCents + z.TuneCents}); err != nil {
			return nil, err
		}
		v.baseline = append(v.baseline, baselinePool{lo: z.KeyLow, hi: z.KeyHigh, pool: pool})
	}
	return v, nil
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
