package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/sample"
)

func writePack(w *writer, p *engine.PackFactory) error {
	if len(p.Zones) < 1 || len(p.Zones) > 4096 {
		return Error("invalid pack zone count")
	}
	c := p.Config
	w.u16(uint16(len(p.Zones)))
	w.byte(byte(c.Voices))
	for _, v := range []float64{c.Amp.Attack, c.Amp.Decay, c.Amp.Sustain, c.Amp.Release, c.Filter.Attack, c.Filter.Decay, c.Filter.Sustain, c.Filter.Release, c.Cutoff, c.FilterDepth, c.Gain, c.TuneCents, c.Humanize.DelayMS, c.Humanize.Velocity, c.Humanize.Cents} {
		w.f64(v)
	}
	w.u64(c.Humanize.Seed)
	// Regions share an asset table; avoid multiplying PCM by velocity zones.
	var regions []sample.Region
	for _, z := range p.Zones {
		index := -1
		for i, r := range regions {
			if len(r.Left) > 0 && len(z.Region.Left) > 0 && &r.Left[0] == &z.Region.Left[0] {
				index = i
				break
			}
		}
		if index < 0 {
			index = len(regions)
			regions = append(regions, z.Region)
		}
		w.u16(uint16(index))
		for _, b := range []byte{z.ChokeGroup, boolByte(z.OneShot), boolByte(z.ChokeSustain), z.KeyLow, z.KeyHigh, z.VelocityLow, z.VelocityHigh, z.Layer, z.Group, z.Position, z.Count, boolByte(z.Release), z.Region.RootKey, boolByte(z.Region.Loop)} {
			w.byte(b)
		}
		w.f64(z.Gain)
		w.f64(z.TuneCents)
		for _, n := range []int{z.Region.Start, z.Region.End, z.Region.LoopStart, z.Region.LoopEnd, z.Region.Crossfade} {
			w.u32(uint32(n))
		}
	}
	w.u16(uint16(len(regions)))
	for _, r := range regions {
		w.u32(uint32(r.SampleRate))
		w.u32(uint32(len(r.Left)))
		w.byte(boolByte(len(r.Right) > 0))
		for _, pcm := range [][]float32{r.Left, r.Right} {
			for _, x := range pcm {
				w.f32(x)
			}
		}
		if len(w.data) > MaxImageBytes {
			return Error("project image exceeds limit")
		}
	}
	return nil
}
func readPack(r *reader) (*engine.PackFactory, error) {
	count, err := r.u16()
	if err != nil || count == 0 || count > 4096 {
		return nil, Error("invalid pack zone count")
	}
	p := &engine.PackFactory{}
	voices, err := r.byte()
	if err != nil || voices < 1 || voices > 32 {
		return nil, Error("invalid pack voice count")
	}
	p.Config.Voices = int(voices)
	c := &p.Config
	values := []*float64{&c.Amp.Attack, &c.Amp.Decay, &c.Amp.Sustain, &c.Amp.Release, &c.Filter.Attack, &c.Filter.Decay, &c.Filter.Sustain, &c.Filter.Release, &c.Cutoff, &c.FilterDepth, &c.Gain, &c.TuneCents, &c.Humanize.DelayMS, &c.Humanize.Velocity, &c.Humanize.Cents}
	for _, v := range values {
		*v, err = r.f64()
		if err != nil {
			return nil, err
		}
	}
	c.Humanize.Seed, err = r.u64()
	if err != nil {
		return nil, err
	}
	p.Zones = make([]sample.Zone, int(count))
	indices := make([]uint16, int(count))
	for i := range p.Zones {
		z := &p.Zones[i]
		indices[i], err = r.u16()
		if err != nil {
			return nil, err
		}
		b, err := r.take(14)
		if err != nil {
			return nil, err
		}
		for _, i := range []int{1, 2, 11, 13} {
			if b[i] > 1 {
				return nil, Error("invalid pack boolean")
			}
		}
		z.ChokeGroup, z.OneShot, z.ChokeSustain = b[0], b[1] == 1, b[2] == 1
		z.KeyLow, z.KeyHigh, z.VelocityLow, z.VelocityHigh, z.Layer, z.Group, z.Position, z.Count = b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10]
		z.Release, z.Region.RootKey, z.Region.Loop = b[11] == 1, b[12], b[13] == 1
		z.Gain, err = r.f64()
		if err != nil {
			return nil, err
		}
		z.TuneCents, err = r.f64()
		if err != nil {
			return nil, err
		}
		for _, v := range []*int{&z.Region.Start, &z.Region.End, &z.Region.LoopStart, &z.Region.LoopEnd, &z.Region.Crossfade} {
			n, e := r.u32()
			if e != nil || n > 1<<30 {
				return nil, Error("invalid pack region")
			}
			*v = int(n)
		}
	}
	assets, err := r.u16()
	if err != nil || assets == 0 || assets > count {
		return nil, Error("invalid pack asset count")
	}
	regions := make([]sample.Region, int(assets))
	for i := range regions {
		rate, e := r.u32()
		if e != nil {
			return nil, e
		}
		frames, e := r.u32()
		if e != nil {
			return nil, e
		}
		stereo, e := r.byte()
		if e != nil || stereo > 1 || frames == 0 || frames > MaxImageBytes/4 {
			return nil, Error("invalid pack PCM")
		}
		if uint64(frames)*4*uint64(1+stereo) > uint64(len(r.data)-r.at) {
			return nil, Error("truncated pack PCM")
		}
		regions[i].SampleRate = int(rate)
		regions[i].Left = make([]float32, int(frames))
		if stereo == 1 {
			regions[i].Right = make([]float32, int(frames))
		}
		for _, pcm := range [][]float32{regions[i].Left, regions[i].Right} {
			for j := range pcm {
				pcm[j], err = r.f32()
				if err != nil {
					return nil, err
				}
			}
		}
	}
	for i := range p.Zones {
		if int(indices[i]) >= len(regions) {
			return nil, Error("invalid pack asset index")
		}
		a := regions[indices[i]]
		p.Zones[i].Region.Left = a.Left
		p.Zones[i].Region.Right = a.Right
		p.Zones[i].Region.SampleRate = a.SampleRate
	}
	return p, nil
}
