package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/kernel/voice/clav"
	"m31labs.dev/cicada/kernel/voice/ep"
	kernelkeys "m31labs.dev/cicada/kernel/voice/keyboard"
)

const filterTaps = 385
const filterHalf = filterTaps / 2

type decimator struct{ coefficients [filterTaps]float64 }

// The centered 385-tap sinc has a 21 kHz cutoff and a four-term cosine window.
// Guard samples retain the linear-phase attack alignment during 4x decimation.
func newDecimator() decimator {
	var filter decimator
	var sum float64
	for i := range filter.coefficients {
		x := float64(i - filterHalf)
		value := 2.0 * 21000 / float64(modelRate)
		if x != 0 {
			value = math.Sin(2*math.Pi*21000/modelRate*x) / (math.Pi * x)
		}
		angle := 2 * math.Pi * float64(i) / float64(filterTaps-1)
		window := .35875 - .48829*math.Cos(angle) + .14128*math.Cos(2*angle) - .01168*math.Cos(3*angle)
		filter.coefficients[i] = value * window
		sum += filter.coefficients[i]
	}
	for i := range filter.coefficients {
		filter.coefficients[i] /= sum
	}
	return filter
}

func (d *decimator) downsample(input []float32, frames int) []float32 {
	output := make([]float32, frames)
	for i := range output {
		center := i * 4
		var sum float64
		for j, c := range d.coefficients {
			index := center + j - filterHalf
			if index >= 0 && index < len(input) {
				sum += float64(float64(input[index]) * c)
			}
		}
		output[i] = float32(sum)
	}
	return output
}

type capture struct {
	Left, Right  []float32
	SourceSHA256 string
}

func hashPCM(left, right []float32) string {
	hash := sha256.New()
	var encoded [8192]byte
	for first := 0; first < len(left); first += len(encoded) / 8 {
		count := min(len(encoded)/8, len(left)-first)
		for j := range count {
			binary.LittleEndian.PutUint32(encoded[j*8:], math.Float32bits(left[first+j]))
			binary.LittleEndian.PutUint32(encoded[j*8+4:], math.Float32bits(right[first+j]))
		}
		_, _ = hash.Write(encoded[:count*8])
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func takeSpec(base kernelkeys.Spec, position int) kernelkeys.Spec {
	if position == 0 {
		return base
	}
	delta := float32(position)
	switch {
	case base.Patch <= 6:
		base.Controls[2] *= 1 + float32(.0015*delta)
		base.Controls[3] += float32(.0008 * delta)
	case base.Patch <= 9:
		base.Controls[3] += float32(.00015 * delta)
	case base.Patch <= 13:
		// These shared generators vary through an unkeyed warm-up below.
	case base.Patch <= 16:
		base.Controls[4] += float32(.045 * delta)
	case base.Patch <= 20:
		base.Controls[7] += float32(.06 * delta)
	default:
		base.Controls[9] += float32(.002 * delta)
	}
	return base
}

func modelForTake(p plan, note, velocity, position int) (kernelkeys.Voice, error) {
	spec := takeSpec(p.Spec, position)
	v, err := keyboard.New(modelRate, &spec)
	if err != nil {
		return nil, err
	}
	if spec.Patch >= 10 && spec.Patch <= 13 {
		for range position * 337 {
			v.NextStereo()
		}
	}
	if err = v.NoteOn(uint8(note), uint8(velocity)); err != nil {
		return nil, err
	}
	return v, nil
}

func cloneRelease(v kernelkeys.Voice) (kernelkeys.Voice, error) {
	switch instrument := v.(type) {
	case *ep.Instrument:
		copy := *instrument
		return &copy, nil
	case *clav.Instrument:
		copy := *instrument
		return &copy, nil
	default:
		return nil, fmt.Errorf("release cloning is available for EP and Clav only")
	}
}

func captureTake(p plan, note, velocity, position int, filter *decimator) (capture, capture, error) {
	var attack, release capture
	v, err := modelForTake(p, note, velocity, position)
	if err != nil {
		return attack, release, err
	}
	frames := p.Frames*4 + filterHalf
	left, right := make([]float32, frames), make([]float32, frames)
	var releaseState kernelkeys.Voice
	releaseHold := min(p.Frames*4, modelRate/8)
	for i := range left {
		if p.ReleaseFrames > 0 && i == releaseHold {
			releaseState, err = cloneRelease(v)
			if err != nil {
				return attack, release, err
			}
		}
		left[i], right[i] = v.NextStereo()
		if !finitePCM(left[i]) || !finitePCM(right[i]) {
			return attack, release, fmt.Errorf("nonfinite modeled PCM for %s note %d", p.Patch, note)
		}
		if p.Channels == 1 && left[i] != right[i] {
			return attack, release, fmt.Errorf("%s mono probe disagrees with captured note %d", p.Patch, note)
		}
	}
	attack.SourceSHA256 = hashPCM(left, right)
	attack.Left = filter.downsample(left, p.Frames)
	if p.Channels == 2 {
		attack.Right = filter.downsample(right, p.Frames)
	}
	if !p.Loop {
		fadeEnd(attack.Left, outputRate/25)
		fadeEnd(attack.Right, outputRate/25)
	}
	if p.ReleaseFrames > 0 {
		off, err := cloneRelease(releaseState)
		if err != nil {
			return attack, release, err
		}
		off.NoteOff(uint8(note))
		frames = p.ReleaseFrames*4 + filterHalf
		left, right = make([]float32, frames), make([]float32, frames)
		dampingTime := .035
		if p.Spec.Patch <= 6 {
			dampingTime = float64(p.Spec.Controls[5])
		}
		decay := math.Exp(-math.Log(1000) / (dampingTime * modelRate))
		factor := 1.
		for i := range left {
			a, b := off.NextStereo()
			heldL, heldR := releaseState.NextStereo()
			factor *= decay
			left[i], right[i] = float32(float64(a)-float64(heldL)*factor), float32(float64(b)-float64(heldR)*factor)
			if !finitePCM(left[i]) || !finitePCM(right[i]) {
				return attack, release, fmt.Errorf("nonfinite modeled release PCM")
			}
		}
		release.SourceSHA256 = hashPCM(left, right)
		release.Left = filter.downsample(left, p.ReleaseFrames)
		if p.Channels == 2 {
			release.Right = filter.downsample(right, p.ReleaseFrames)
		}
		fadeEnd(release.Left, outputRate/100)
		fadeEnd(release.Right, outputRate/100)
	}
	return attack, release, nil
}

func finitePCM(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

func fadeEnd(pcm []float32, frames int) {
	if len(pcm) == 0 {
		return
	}
	frames = min(frames, len(pcm)/4)
	for i := range frames {
		index := len(pcm) - frames + i
		pcm[index] = float32(float64(pcm[index]) * float64(frames-1-i) / float64(frames))
	}
}

type loopPoints struct {
	Start, End, Crossfade int
	NormalizedError       float64
}

func findLoop(pcm capture, minimumStart int) (loopPoints, error) {
	frames := len(pcm.Left)
	crossfade := min(outputRate*64/1000, (frames-minimumStart)/4)
	if crossfade < outputRate*40/1000 {
		return loopPoints{}, fmt.Errorf("sustain loop needs at least a 40 ms crossfade")
	}
	maximumStart := frames - max(outputRate/10, crossfade*3)
	if maximumStart < minimumStart {
		return loopPoints{}, fmt.Errorf("capture does not leave a sustain loop after the attack")
	}
	end := frames
	score := func(start, stride int) float64 {
		var errorPower, power float64
		for i := 0; i < crossfade; i += stride {
			for _, channel := range [][]float32{pcm.Left, pcm.Right} {
				if channel == nil {
					continue
				}
				a, b := float64(channel[end-crossfade+i]), float64(channel[start+i])
				difference := a - b
				errorPower += difference * difference
				power += a*a + b*b
			}
		}
		return errorPower / max(1e-30, power)
	}
	bestStart, best := minimumStart, math.Inf(1)
	for start := minimumStart; start <= maximumStart; start += 64 {
		if value := score(start, 8); value < best {
			bestStart, best = start, value
		}
	}
	coarse := bestStart
	best = math.Inf(1)
	for start := max(minimumStart, coarse-64); start <= min(maximumStart, coarse+64); start++ {
		if value := score(start, 1); value < best {
			bestStart, best = start, value
		}
	}
	return loopPoints{Start: bestStart, End: end, Crossfade: crossfade, NormalizedError: math.Sqrt(best)}, nil
}

func wav24(pcm capture, channels int) ([]byte, float64, error) {
	if len(pcm.Left) == 0 || channels < 1 || channels > 2 || channels == 2 && len(pcm.Right) != len(pcm.Left) {
		return nil, 0, fmt.Errorf("invalid rendered PCM dimensions")
	}
	peak := 0.
	for _, channel := range [][]float32{pcm.Left, pcm.Right} {
		for _, value := range channel {
			if !finitePCM(value) {
				return nil, 0, fmt.Errorf("nonfinite PCM cannot be quantized")
			}
			peak = max(peak, math.Abs(float64(value)))
		}
	}
	scale := 1.
	if peak > .999999 {
		scale = .98 / peak
	}
	dataBytes := len(pcm.Left) * channels * 3
	padding := dataBytes & 1
	wav := make([]byte, 44+dataBytes+padding)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], uint16(channels))
	binary.LittleEndian.PutUint32(wav[24:], outputRate)
	binary.LittleEndian.PutUint32(wav[28:], uint32(outputRate*channels*3))
	binary.LittleEndian.PutUint16(wav[32:], uint16(channels*3))
	binary.LittleEndian.PutUint16(wav[34:], 24)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(dataBytes))
	offset := 44
	for i := range pcm.Left {
		for ch := 0; ch < channels; ch++ {
			value := pcm.Left[i]
			if ch == 1 {
				value = pcm.Right[i]
			}
			quantized := int32(max(-8388608., min(8388607., math.Round(float64(value)*scale*8388608))))
			wav[offset], wav[offset+1], wav[offset+2] = byte(quantized), byte(quantized>>8), byte(quantized>>16)
			offset += 3
		}
	}
	return wav, scale, nil
}

func deterministicGzip(wav []byte) ([]byte, error) {
	var result bytes.Buffer
	writer, err := gzip.NewWriterLevel(&result, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	writer.Header.OS = 255
	if _, err = writer.Write(wav); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
