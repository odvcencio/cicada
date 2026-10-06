package main

import (
	"math"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/voice/sample"
)

func samplerRatio(s scenario) float64 {
	if s.kind == "sampler-1.5" {
		return 1.5
	}
	if s.sampler() {
		return 1
	}
	return 0
}

func syntheticRegion(rate int) sample.Region {
	const frames = 65536
	r := sample.Region{Left: make([]float32, frames), Right: make([]float32, frames), SampleRate: rate, RootKey: 60, End: frames, Loop: true, LoopEnd: frames}
	for i := range r.Left {
		phase := 2 * math.Pi * float64(i) / frames
		r.Left[i] = float32(.3 * math.Sin(phase*257))
		r.Right[i] = float32(.3 * math.Cos(phase*313))
	}
	return r
}

// This is a standalone DSP comparator, not an alternative engine. Main does
// not route samplers through either live Engine.Render or offline render.WAV.
type samplerSession struct {
	voices                       [16]*sample.Voice
	drives                       [16]*fx.Drive
	delay                        *fx.Delay
	reverb                       *fx.Reverb
	limiter                      *mix.Limiter
	mixer                        mix.Track
	tracks                       int
	scenes                       bool
	position, nextBar, barFrames int64
	changes                      uint64
	noteFault                    bool
}

func newSampler(s scenario, region sample.Region) (*samplerSession, error) {
	r := &samplerSession{tracks: s.tracks, scenes: s.scenes, nextBar: int64(s.rate) * 2, barFrames: int64(s.rate) * 2, mixer: mix.NewTrack(-24, 0, false)}
	var err error
	r.limiter, err = mix.NewLimiter(s.rate)
	if err != nil {
		return nil, err
	}
	for i := 0; i < s.tracks; i++ {
		r.voices[i], err = sample.New(s.rate, region)
		if err != nil {
			return nil, err
		}
		if err = r.voices[i].SetParams(sample.Params{Gain: 1, FineTuneCents: 1200 * math.Log2(samplerRatio(s))}); err != nil {
			return nil, err
		}
		if err = r.voices[i].NoteOn(60, 100); err != nil {
			return nil, err
		}
		if s.drive {
			r.drives[i], err = fx.NewDrive(s.rate)
			if err != nil {
				return nil, err
			}
			p := fx.DefaultDriveParams()
			p.GainDB, p.ToneHz, p.Mix = 9, 9000, .7
			if err = r.drives[i].SetParams(p); err != nil {
				return nil, err
			}
			r.drives[i].Reset()
		}
	}
	if s.sends {
		r.delay, err = fx.NewDelay(s.rate, 120000)
		if err != nil {
			return nil, err
		}
		if err = r.delay.SetParams(fx.DefaultDelayParams()); err != nil {
			return nil, err
		}
		r.delay.Reset()
		r.reverb, err = fx.NewReverb(s.rate)
		if err != nil {
			return nil, err
		}
		if err = r.reverb.SetParams(fx.DefaultReverbParams()); err != nil {
			return nil, err
		}
		r.reverb.Reset()
	}
	return r, nil
}

func (r *samplerSession) Render(left, right []float32) {
	for frame := range left {
		if r.scenes && r.position == r.nextBar {
			for track := 0; track < r.tracks; track++ {
				if r.voices[track].NoteOn(60, uint8(100+27*(r.changes%2))) != nil {
					r.noteFault = true
				}
			}
			r.changes++
			r.nextBar += r.barFrames
		}
		var dry mix.Dry
		var al, ar, bl, br float32
		for track := 0; track < r.tracks; track++ {
			l, rr := r.voices[track].NextStereo()
			if r.drives[track] != nil {
				l, rr = r.drives[track].Process(l, rr)
			}
			dry.Add(l, rr, r.mixer)
			if r.delay != nil {
				al += l * r.mixer.Left * .3
				ar += rr * r.mixer.Right * .3
				bl += l * r.mixer.Left * .35
				br += rr * r.mixer.Right * .35
			}
		}
		if r.delay != nil {
			l, rr := r.delay.Process(al, ar)
			dry.AddReturn(l, rr)
			l, rr = r.reverb.Process(bl, br)
			dry.AddReturn(l, rr)
		}
		l, rr := dry.Music()
		left[frame], right[frame], _ = r.limiter.Process(l, rr)
		r.position++
	}
}

func (r *samplerSession) faulted() bool {
	if r.noteFault || r.limiter.Fault() || r.delay != nil && (r.delay.Fault() || r.reverb.Fault()) {
		return true
	}
	for i := 0; i < r.tracks; i++ {
		if r.drives[i] != nil && r.drives[i].Fault() {
			return true
		}
	}
	return false
}
