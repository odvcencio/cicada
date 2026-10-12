//go:build !js || !wasm

// Package padsampler prepares immutable pad sounds and prints performances.
// Preparation and file access happen on the host, outside audio callbacks.
package padsampler

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/voice/sample"
)

func Clean() Effects { return Effects{Cutoff: 20000, DelayMS: 278, Feedback: .3} }

func (p Pad) Validate(frames int64) error {
	if len(p.Name) > 64 || p.Start < 0 || p.End <= p.Start || p.End > frames || p.Pitch < -24 || p.Pitch > 24 ||
		!finite(p.GainDB) || p.GainDB < -60 || p.GainDB > 12 || !finite(p.AttackMS) || p.AttackMS < 0 || p.AttackMS > 500 ||
		!finite(p.ReleaseMS) || p.ReleaseMS < 2 || p.ReleaseMS > 2000 || (p.Mode != "oneshot" && p.Mode != "gate") || p.Choke < 0 || p.Choke > 4 {
		return fmt.Errorf("choose a valid source region, pitch −24 to +24, gain −60 to +12 dB, envelope, and choke group 0 to 4")
	}
	return nil
}

func (e Effects) Validate() error {
	if !finite(e.Cutoff) || e.Cutoff < 80 || e.Cutoff > 20000 || !finite(e.Drive) || e.Drive < 0 || e.Drive > 24 ||
		!finite(e.DelayMS) || e.DelayMS < 1 || e.DelayMS > 1000 || !finite(e.Feedback) || e.Feedback < 0 || e.Feedback > .7 ||
		!finite(e.DelayMix) || e.DelayMix < 0 || e.DelayMix > 1 || !finite(e.ReverbMix) || e.ReverbMix < 0 || e.ReverbMix > 1 {
		return fmt.Errorf("effect settings are outside their supported ranges")
	}
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func Decode(data []byte) (audition.Audio, error) {
	h, err := audioasset.ReadWAVHeader(bytes.NewReader(data))
	if err != nil {
		return audition.Audio{}, err
	}
	if len(data) > (64<<20)+44 || h.Frames > MaxFrames || h.RateHz > 192000 {
		return audition.Audio{}, fmt.Errorf("WAV exceeds the 64 MiB or frame limit")
	}
	a := audition.Audio{Rate: h.RateHz, Left: make([]float32, h.Frames), Right: make([]float32, h.Frames)}
	width, offset := h.BitDepth/8, int(h.DataOffset)
	for i := range a.Left {
		for ch := 0; ch < h.Channels; ch++ {
			b := data[offset : offset+width]
			offset += width
			var x float32
			switch {
			case h.Encoding == "float":
				x = math.Float32frombits(binary.LittleEndian.Uint32(b))
			case width == 2:
				x = float32(int16(binary.LittleEndian.Uint16(b))) / 32768
			case width == 3:
				n := int32(b[0]) | int32(b[1])<<8 | int32(b[2])<<16
				x = float32(n<<8>>8) / 8388608
			default:
				x = float32(float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648)
			}
			if !finite(float64(x)) || math.Abs(float64(x)) > 8 {
				return audition.Audio{}, fmt.Errorf("invalid PCM at frame %d", i)
			}
			if ch == 0 {
				a.Left[i] = x
				if h.Channels == 1 {
					a.Right[i] = x
				}
			} else {
				a.Right[i] = x
			}
		}
	}
	return a, nil
}

func WAV(a audition.Audio) ([]byte, error) {
	var b bytes.Buffer
	err := a.WriteWAV(&b)
	return b.Bytes(), err
}

func Peaks(a audition.Audio) []float32 {
	peaks := make([]float32, 256)
	for i := range peaks {
		lo, hi := i*len(a.Left)/len(peaks), (i+1)*len(a.Left)/len(peaks)
		for j := lo; j < hi; j++ {
			peaks[i] = max(peaks[i], float32(math.Abs(float64(a.Left[j]))), float32(math.Abs(float64(a.Right[j]))))
		}
	}
	return peaks
}

func Prepare(a audition.Audio, p Pad, e Effects) (audition.Audio, error) {
	if p.BypassEffects {
		e = Clean()
	}
	if err := p.Validate(int64(len(a.Left))); err != nil {
		return audition.Audio{}, err
	}
	if err := e.Validate(); err != nil {
		return audition.Audio{}, err
	}
	left, right := a.Left[p.Start:p.End], a.Right[p.Start:p.End]
	if p.Reverse {
		left = append([]float32(nil), left...)
		right = append([]float32(nil), right...)
		for i, j := 0, len(left)-1; i < j; i, j = i+1, j-1 {
			left[i], left[j] = left[j], left[i]
			right[i], right[j] = right[j], right[i]
		}
	}
	v, err := sample.New(Rate, sample.Region{Left: left, Right: right, SampleRate: a.Rate, RootKey: 60, End: len(left)})
	if err != nil {
		return audition.Audio{}, err
	}
	if err = v.NoteOn(uint8(60+p.Pitch), 127); err != nil {
		return audition.Audio{}, err
	}
	body := int(math.Ceil(float64(len(left)) / v.Ratio()))
	tail := 0
	if e.DelayMix > 0 {
		tail = int(min(8., e.DelayMS/1000*max(1., math.Ceil(math.Log(.001)/math.Log(max(.01, e.Feedback))))) * Rate)
	}
	if e.ReverbMix > 0 {
		tail = max(tail, 3*Rate)
	}
	if body+tail > MaxFrames {
		return audition.Audio{}, fmt.Errorf("pitched pad and effect tail exceed the output frame limit")
	}
	out := audition.Audio{Rate: Rate, Left: make([]float32, body+tail), Right: make([]float32, body+tail)}
	attack, release := max(1, int(p.AttackMS*Rate/1000)), max(1, int(p.ReleaseMS*Rate/1000))
	gain := float32(math.Pow(10, p.GainDB/20))
	for i := 0; i < body; i++ {
		l, r := v.NextStereo()
		env := float32(min(1., float64(i+1)/float64(attack), float64(body-i)/float64(release)))
		out.Left[i], out.Right[i] = l*gain*env, r*gain*env
	}
	if e.Cutoff < 20000 { // Butterworth low-pass, with independent stereo state.
		w := 2 * math.Pi * e.Cutoff / Rate
		c, s := math.Cos(w), math.Sin(w)
		alpha := s / math.Sqrt2
		den := 1 + alpha
		b0, b1, b2 := (1-c)/2/den, (1-c)/den, (1-c)/2/den
		a1, a2 := -2*c/den, (1-alpha)/den
		for _, pcm := range [][]float32{out.Left, out.Right} {
			var x1, x2, y1, y2 float64
			for i, x := range pcm {
				y := b0*float64(x) + b1*x1 + b2*x2 - a1*y1 - a2*y2
				x2, x1 = x1, float64(x)
				y2, y1 = y1, y
				pcm[i] = float32(y)
			}
		}
	}
	if e.Drive > 0 {
		d, _ := fx.NewDrive(Rate)
		params := fx.DefaultDriveParams()
		params.GainDB = e.Drive
		_ = d.SetParams(params)
		d.Reset()
		if err = out.Process(d); err != nil {
			return audition.Audio{}, err
		}
	}
	if e.DelayMix > 0 {
		d, _ := fx.NewDelay(Rate, 120000)
		params := fx.DefaultDelayParams()
		params.Division = fx.FreeDelay
		params.TimeMs = e.DelayMS
		params.Feedback = e.Feedback
		params.Mix = e.DelayMix
		params.PingPong = true
		_ = d.SetParams(params)
		d.Reset()
		if err = out.Process(d); err != nil {
			return audition.Audio{}, err
		}
	}
	if e.ReverbMix > 0 {
		r, _ := fx.NewReverb(Rate)
		params := fx.DefaultReverbParams()
		params.DecaySec = 1.2
		params.Mix = e.ReverbMix
		_ = r.SetParams(params)
		r.Reset()
		if err = out.Process(r); err != nil {
			return audition.Audio{}, err
		}
	}
	Limit(out)
	return out, nil
}

// Limit applies one fixed gain to an entire print, preserving its dynamics.
func Limit(a audition.Audio) float64 {
	peak := float32(0)
	for i := range a.Left {
		peak = max(peak, float32(math.Abs(float64(a.Left[i]))), float32(math.Abs(float64(a.Right[i]))))
	}
	if peak <= .8912509 {
		return 0
	}
	gain := float32(.8912509) / peak
	for i := range a.Left {
		a.Left[i] *= gain
		a.Right[i] *= gain
	}
	return 20 * math.Log10(float64(gain))
}

type Voice struct {
	Audio audition.Audio
	Pad   Pad
}

// Print uses the exact prepared sounds used by the browser pad monitor.
// Same-pad retriggers and choke groups release over five milliseconds.
func Print(perf Performance, sounds map[string]Voice) (audition.Audio, error) {
	if perf.Frames < 1 || perf.Frames > MaxFrames || len(perf.Hits) < 1 || len(perf.Hits) > 512 {
		return audition.Audio{}, fmt.Errorf("record 1 to 512 hits within the output frame limit")
	}
	for _, h := range perf.Hits {
		v, ok := sounds[h.Sound]
		if !ok || h.Pad < 1 || h.Pad > 16 || h.Frame < 0 || h.Frame >= perf.Frames || !finite(h.Velocity) || h.Velocity <= 0 || h.Velocity > 1 || h.GateFrames < 0 || h.GateFrames > perf.Frames || h.Choke != v.Pad.Choke {
			return audition.Audio{}, fmt.Errorf("invalid performance hit or missing prepared sound")
		}
	}
	out := audition.Audio{Rate: Rate, Left: make([]float32, perf.Frames), Right: make([]float32, perf.Frames)}
	for n, h := range perf.Hits {
		v := sounds[h.Sound]
		end := min(h.Frame+int64(len(v.Audio.Left)), perf.Frames)
		release := int64(0)
		if v.Pad.Mode == "gate" && h.GateFrames > 0 && h.Frame+h.GateFrames < end {
			end = min(end, h.Frame+h.GateFrames+240)
			release = min(240, end-h.Frame)
		}
		for m, next := range perf.Hits {
			if m == n || next.Frame < h.Frame || (next.Frame == h.Frame && m < n) {
				continue
			}
			if (next.Pad == h.Pad || (h.Choke > 0 && next.Choke == h.Choke)) && next.Frame < end {
				end = min(end, next.Frame+240)
				release = min(240, end-h.Frame)
			}
		}
		for frame := h.Frame; frame < end; frame++ {
			i := frame - h.Frame
			gain := float32(h.Velocity)
			if release > 0 && frame >= end-release {
				gain *= float32(end-frame) / float32(release)
			}
			out.Left[frame] += v.Audio.Left[i] * gain
			out.Right[frame] += v.Audio.Right[i] * gain
		}
	}
	// A loop print ends exactly at the requested frame; never cut at full gain.
	for i := max(0, len(out.Left)-240); i < len(out.Left); i++ {
		g := float32(len(out.Left)-i) / 240
		out.Left[i] *= g
		out.Right[i] *= g
	}
	Limit(out)
	return out, nil
}
