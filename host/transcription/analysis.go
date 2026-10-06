package transcription

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"m31labs.dev/cicada/host/recording"
)

// ErrNoNotes reports a recording with no supported voiced notes.
var ErrNoNotes = errors.New("no voiced notes found")

const (
	analysisRate = 16000
	analysisHop  = 80
	minimumHz    = 55.0
	maximumHz    = 2093.01
	maximumTime  = 300
)

type pitchFrame struct {
	time, midi, confidence, energy float64
}

// DecodeWAV accepts the bounded PCM WAV formats supported by recording.
func DecodeWAV(data []byte, options Options) (Result, error) {
	audio, err := recording.DecodeWAV(data)
	if err != nil {
		return Result{}, err
	}
	result, err := Analyze(audio.PCM, audio.Rate, options)
	result.InputSHA256 = audio.SHA256
	return result, err
}

// Analyze measures monophonic notes without changing their onsets to a grid.
// Sample rates from 8 to 96 kHz and recordings up to five minutes are accepted.
func Analyze(pcm []float32, rate int, options Options) (Result, error) {
	if rate < 8000 || rate > 96000 {
		return Result{}, fmt.Errorf("sample rate must be between 8 and 96 kHz")
	}
	if len(pcm) == 0 || len(pcm) > rate*maximumTime || len(pcm) > recording.MaxInputBytes/4 {
		return Result{}, fmt.Errorf("recording must contain audio and fit within five minutes and 64 MiB")
	}
	var dc float64
	clipped := 0
	for _, sample := range pcm {
		x := float64(sample)
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1 {
			return Result{}, fmt.Errorf("recording contains invalid or clipped PCM")
		}
		if math.Abs(x) >= .999 {
			clipped++
		}
		dc += x
	}
	if clipped > max(2, len(pcm)/1000) {
		return Result{}, fmt.Errorf("recording is clipped; lower the recording level")
	}
	dc /= float64(len(pcm))
	samples := resample(pcm, rate, dc)
	frames, threshold := trackPitch(samples)
	result := Result{Notes: segmentNotes(frames, threshold, float64(len(pcm))/float64(rate))}
	if len(result.Notes) == 0 {
		return Result{}, fmt.Errorf("%w; use a clear solo voice or instrument recording", ErrNoNotes)
	}
	return result, nil
}

// Four low-pass stages suppress aliases before linear sample-rate conversion.
// The filter is evaluated at the input rate and needs no input-sized scratch.
func resample(pcm []float32, rate int, dc float64) []float64 {
	n := (len(pcm)*analysisRate + rate - 1) / rate
	out := make([]float64, n)
	alpha := 1 - math.Exp(-2*math.Pi*5000/float64(rate))
	if rate <= analysisRate {
		alpha = 1
	}
	var stages [4]float64
	position := -1
	previous, current := 0.0, 0.0
	for i := range out {
		at := float64(i) * float64(rate) / analysisRate
		left := int(at)
		for position < min(len(pcm)-1, left+1) {
			position++
			x := float64(pcm[position]) - dc
			for j := range stages {
				stages[j] += alpha * (x - stages[j])
				x = stages[j]
			}
			previous, current = current, x
		}
		if left+1 >= len(pcm) {
			out[i] = current
		} else {
			out[i] = previous + (current-previous)*(at-float64(left))
		}
	}
	return out
}

func trackPitch(samples []float64) ([]pitchFrame, float64) {
	n := (len(samples) + analysisHop - 1) / analysisHop
	frames := make([]pitchFrame, n)
	rawEnergy := make([]float64, n)
	var peak float64
	for i := range frames {
		from, end := i*analysisHop, min(len(samples), (i+1)*analysisHop)
		var power float64
		for _, x := range samples[from:end] {
			power += x * x
		}
		rawEnergy[i] = power / float64(end-from)
	}
	for i := range frames {
		var power float64
		from, end := max(0, i-1), min(n, i+2)
		for _, x := range rawEnergy[from:end] {
			power += x
		}
		frames[i].energy = math.Sqrt(power / float64(end-from))
		frames[i].time = float64(i*analysisHop+analysisHop/2) / analysisRate
		peak = max(peak, frames[i].energy)
	}
	threshold := max(.0006, peak*.025)
	// Estimate the noise floor only when the recording contains quiet portions;
	// a continuously sustained note must not become its own silence threshold.
	ordered := make([]float64, n)
	for i := range frames {
		ordered[i] = frames[i].energy
	}
	sort.Float64s(ordered)
	if n > 0 && ordered[n/10] < peak*.08 {
		threshold = max(threshold, ordered[n/10]*3)
	}
	var window [720]float64
	var difference [304]float64
	// Sub-sample period estimates can fall just outside an endpoint. Admit the
	// same 15-cent estimation margin at both edges without changing confidence
	// thresholds or quantizing the measured pitch to the advertised note range.
	frequencyMargin := math.Exp2(15.0 / 1200)
	for i := range frames {
		if frames[i].energy < threshold {
			continue
		}
		center := i*analysisHop + analysisHop/2
		for j := range window {
			at := center - len(window)/2 + j
			window[j] = 0
			if at >= 0 && at < len(samples) {
				window[j] = samples[at]
			}
		}
		hz, confidence := yinPitch(window[:], difference[:])
		if hz >= minimumHz/frequencyMargin && hz <= maximumHz*frequencyMargin && confidence >= .72 {
			frames[i].midi = 69 + 12*math.Log2(hz/440)
			frames[i].confidence = confidence
		}
	}
	// Five-frame medians remove isolated octave mistakes without averaging
	// adjacent notes into pitches that were never sung.
	raw := append([]pitchFrame(nil), frames...)
	var pitches [5]float64
	for i := range frames {
		if raw[i].confidence == 0 {
			continue
		}
		count := 0
		for j := max(0, i-2); j < min(n, i+3); j++ {
			if raw[j].confidence > 0 {
				pitches[count] = raw[j].midi
				count++
			}
		}
		sort.Float64s(pitches[:count])
		frames[i].midi = pitches[count/2]
	}
	return frames, threshold
}

// Each lag compares samples symmetrically around the frame center. Summing
// every fourth sample reduces work while retaining the original lag resolution.
func yinPitch(window, difference []float64) (float64, float64) {
	const comparison = 400
	var sum float64
	for lag := 1; lag < len(difference); lag++ {
		left := (len(window) - comparison - lag) / 2
		var d float64
		for j := 0; j < comparison; j += 4 {
			delta := window[left+j] - window[left+j+lag]
			d += delta * delta
		}
		sum += d
		if sum > 1e-20 {
			difference[lag] = d * float64(lag) / sum
		} else {
			difference[lag] = 1
		}
	}
	selected, best := 0, 1.0
	for lag := 7; lag < len(difference)-1; lag++ {
		if difference[lag] < best {
			selected, best = lag, difference[lag]
		}
		if difference[lag] < .12 {
			for lag+1 < len(difference)-1 && difference[lag+1] < difference[lag] {
				lag++
			}
			selected, best = lag, difference[lag]
			break
		}
	}
	if selected == 0 || best > .28 {
		return 0, 0
	}
	lag := float64(selected)
	a, b, c := difference[selected-1], difference[selected], difference[selected+1]
	if denominator := a - 2*b + c; math.Abs(denominator) > 1e-12 {
		lag += max(-.5, min(.5, .5*(a-c)/denominator))
	}
	return analysisRate / lag, max(0, 1-best)
}

func segmentNotes(frames []pitchFrame, threshold, duration float64) []Note {
	var notes []Note
	start, quiet, changed := -1, 0, 0
	startTime, anchor, candidate := 0.0, 0.0, 0.0
	peak, lastAttack := 0.0, -1.0
	finish := func(end int, endTime float64) {
		if start < 0 || end <= start {
			return
		}
		note, ok := makeNote(frames[start:end], startTime, min(duration, endTime), threshold)
		if ok {
			notes = append(notes, note)
		}
	}
	for i, frame := range frames {
		if frame.confidence == 0 {
			if start >= 0 {
				quiet++
				if quiet >= 5 {
					end := i - quiet + 1
					finish(end, frames[end].time)
					start = -1
				}
			}
			continue
		}
		if start < 0 {
			start, startTime, anchor = i, refineOnset(frames, i, threshold), stableAnchor(frames, i)
			if len(notes) > 0 {
				startTime = max(startTime, notes[len(notes)-1].End)
			}
			quiet, changed, peak, lastAttack = 0, 0, frame.energy, startTime
			continue
		}
		quiet = 0
		// A substantial energy rise after a decay separates repeated pitches.
		if i >= 4 && frame.time-lastAttack >= .08 && frames[i-3].energy < peak*.55 && frame.energy > max(threshold*2, frames[i-3].energy*1.8) {
			at := refineAttack(frames, i)
			if at-startTime >= .06 {
				finish(i, at)
				start, startTime, anchor = i, at, stableAnchor(frames, i)
				changed, peak, lastAttack = 0, frame.energy, at
				continue
			}
		}
		peak = max(peak, frame.energy)
		if math.Abs(frame.midi-anchor) > .72 {
			if changed == 0 || math.Abs(frame.midi-candidate) > .35 {
				changed, candidate = 1, frame.midi
			} else {
				changed++
			}
			if changed >= 5 {
				at := i - changed + 1
				boundary := frames[at].time
				finish(at, boundary)
				start, startTime, anchor = at, boundary, stableAnchor(frames, at)
				changed, peak, lastAttack = 0, frame.energy, boundary
			}
		} else {
			changed = 0
			// Adapt very slowly to a tuning drift, without following vibrato.
			anchor += .01 * (frame.midi - anchor)
		}
	}
	finish(len(frames), duration)
	return notes
}

// A short look-ahead estimates the center of pitch modulation. Starting an
// anchor at a vibrato extremum would mistake its opposite extremum for a note.
func stableAnchor(frames []pitchFrame, at int) float64 {
	var pitches []float64
	first := frames[at].midi
	for i := at; i < min(len(frames), at+32); i++ {
		if frames[i].confidence == 0 || math.Abs(frames[i].midi-first) > 1.1 {
			break
		}
		pitches = append(pitches, frames[i].midi)
	}
	if len(pitches) == 0 {
		return first
	}
	sort.Float64s(pitches)
	return (pitches[len(pitches)/10] + pitches[len(pitches)-1-len(pitches)/10]) / 2
}

func refineOnset(frames []pitchFrame, at int, threshold float64) float64 {
	from := max(0, at-16)
	for i := at; i >= from; i-- {
		if frames[i].energy < threshold {
			return max(0, frames[min(at, i+1)].time-.005)
		}
	}
	return max(0, frames[at].time-.01)
}

func refineAttack(frames []pitchFrame, at int) float64 {
	best := at
	for i := max(0, at-5); i < at; i++ {
		if frames[i].energy < frames[best].energy {
			best = i
		}
	}
	return max(0, frames[best].time+.0025)
}

func makeNote(frames []pitchFrame, start, end, threshold float64) (Note, bool) {
	if end-start < .045 {
		return Note{}, false
	}
	var pitches, confidence []float64
	var power float64
	for _, frame := range frames {
		if frame.confidence > 0 && frame.time >= start+.015 && frame.time <= end-.015 {
			pitches = append(pitches, frame.midi)
			confidence = append(confidence, frame.confidence)
			power += frame.energy * frame.energy
		}
	}
	if len(pitches) < 4 {
		return Note{}, false
	}
	sort.Float64s(pitches)
	sort.Float64s(confidence)
	midi := pitches[len(pitches)/2]
	velocity := max(1, min(127, int(math.Round(100+25*math.Log10(max(threshold, math.Sqrt(power/float64(len(pitches))))/.2)))))
	note := Note{Start: start, End: end, MIDI: midi, Cents: 100 * (midi - math.Round(midi)), PitchHz: 440 * math.Exp2((midi-69)/12), Confidence: confidence[len(confidence)/2], Velocity: velocity}
	stride := max(1, (len(frames)+63)/64)
	for i := 0; i < len(frames); i += stride {
		frame := frames[i]
		if frame.confidence > 0 && frame.time >= start+.01 && frame.time <= end-.01 && math.Abs(frame.midi-midi) < .72 {
			note.Pitch = append(note.Pitch, PitchPoint{Time: frame.time, Cents: 100 * (frame.midi - math.Round(midi))})
		}
	}
	return note, true
}
