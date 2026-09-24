// Package render contains the first offline target for Cicada scores. This
// prototype renders custom mono instrument graphs as stereo PCM24 WAV.
package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/kernel/graph"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type Options struct {
	SampleRate int
	TailSec    int
}

type Report struct {
	SampleRate int
	Bars       int
	Frames     int64
	Peak       float32
}

type trackRuntime struct {
	name     string
	voice    *graph.Voice
	patterns map[string]seq.Pattern
	current  seq.Pattern
	active   *seq.Pattern
}

type scheduled struct {
	track int
	event seq.Event
}

// WAV renders the song arrangement from a valid score. This first audio path
// supports custom mono instruments; built-in acid and drum DSP remains M0 work.
func WAV(score *notation.Score, opts Options, writer io.Writer) (Report, error) {
	var report Report
	if score == nil {
		return report, fmt.Errorf("nil score")
	}
	if opts.SampleRate == 0 {
		opts.SampleRate = 48_000
	}
	if opts.TailSec < 0 || opts.TailSec > 10 {
		return report, fmt.Errorf("tail must be 0 to 10 seconds")
	}
	clock, err := seq.NewClock(opts.SampleRate, score.TempoMilli)
	if err != nil {
		return report, err
	}
	if len(score.Song) == 0 {
		return report, fmt.Errorf("score has no song arrangement")
	}
	for _, entry := range score.Song {
		report.Bars += entry.Bars
	}
	if report.Bars < 1 || report.Bars > 256 {
		return report, fmt.Errorf("render supports 1 to 256 bars")
	}
	report.SampleRate = opts.SampleRate
	report.Frames = clock.SampleAtTick(int64(report.Bars)*seq.TicksPerBar) + int64(opts.TailSec*opts.SampleRate)
	dataBytes := report.Frames * 6
	if dataBytes > int64(^uint32(0))-36 {
		return report, fmt.Errorf("WAV exceeds RIFF size limit")
	}
	tracks, err := compileTracks(score, opts.SampleRate)
	if err != nil {
		return report, err
	}
	if err := writeHeader(writer, opts.SampleRate, uint32(dataBytes)); err != nil {
		return report, err
	}
	var eventBuf [128]seq.Event
	block := make([]byte, 4096*6)
	var events []scheduled
	var position int64
	bar := 0
	for _, entry := range score.Song {
		scene := findScene(score, entry.Scene)
		if scene == nil {
			return report, fmt.Errorf("unknown scene %s", entry.Scene)
		}
		for range entry.Bars {
			if err := applyScene(tracks, scene); err != nil {
				return report, err
			}
			end := clock.SampleAtTick(int64(bar+1) * seq.TicksPerBar)
			for position < end {
				frames := 4096
				if position+int64(frames) > end {
					frames = int(end - position)
				}
				events = events[:0]
				for ti := range tracks {
					if tracks[ti].active == nil {
						continue
					}
					n, overflow := seq.EventsInBlock(tracks[ti].active, clock, uint8(ti), 0, position, frames, eventBuf[:])
					if overflow {
						return report, fmt.Errorf("too many events in render block")
					}
					for _, event := range eventBuf[:n] {
						events = append(events, scheduled{track: ti, event: event})
					}
				}
				sort.Slice(events, func(i, j int) bool {
					if events[i].event.Sample != events[j].event.Sample {
						return events[i].event.Sample < events[j].event.Sample
					}
					return events[i].track < events[j].track
				})
				if err := renderBlock(writer, tracks, events, position, frames, block, &report); err != nil {
					return report, err
				}
				position += int64(frames)
			}
			bar++
		}
	}
	for position < report.Frames {
		frames := 4096
		if position+int64(frames) > report.Frames {
			frames = int(report.Frames - position)
		}
		if err := renderBlock(writer, tracks, nil, position, frames, block, &report); err != nil {
			return report, err
		}
		position += int64(frames)
	}
	return report, nil
}

func compileTracks(score *notation.Score, sampleRate int) ([]trackRuntime, error) {
	programs := make(map[string]*instrument.Program, len(score.Instruments))
	for _, definition := range score.Instruments {
		program, ds := instrument.Compile(definition)
		if len(ds) > 0 {
			return nil, fmt.Errorf("instrument %s: %s", definition.Name, ds[0].Message)
		}
		programs[definition.Name] = program
	}
	tracks := make([]trackRuntime, 0, len(score.Tracks))
	for _, source := range score.Tracks {
		program := programs[source.Kind]
		if program == nil {
			return nil, fmt.Errorf("audio renderer does not yet implement %s track %s", source.Kind, source.Name)
		}
		if program.Mode != "mono" {
			return nil, fmt.Errorf("audio renderer does not yet implement poly voices")
		}
		overrides := make(map[string]string, len(source.Params))
		for _, param := range source.Params {
			overrides[param.Name] = param.Value
		}
		kernelProgram, err := instrument.Lower(program, overrides)
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", source.Name, err)
		}
		voice, err := graph.NewVoice(kernelProgram, sampleRate)
		if err != nil {
			return nil, err
		}
		track := trackRuntime{name: source.Name, voice: voice, patterns: map[string]seq.Pattern{}}
		for _, pattern := range score.Patterns {
			if pattern.Kind != "notes" {
				continue
			}
			compiled, err := project.CompilePattern(score, pattern, source)
			if err != nil {
				return nil, err
			}
			track.patterns[pattern.Name] = compiled[0].Pattern
		}
		tracks = append(tracks, track)
	}
	return tracks, nil
}

func findScene(score *notation.Score, name string) *notation.Scene {
	for i := range score.Scenes {
		if score.Scenes[i].Name == name {
			return &score.Scenes[i]
		}
	}
	return nil
}

func applyScene(tracks []trackRuntime, scene *notation.Scene) error {
	for _, binding := range scene.Bindings {
		for ti := range tracks {
			if tracks[ti].name != binding.Track {
				continue
			}
			switch binding.Pattern {
			case "keep":
			case "off":
				tracks[ti].active = nil
			default:
				pattern, ok := tracks[ti].patterns[binding.Pattern]
				if !ok {
					return fmt.Errorf("track %s cannot play pattern %s", binding.Track, binding.Pattern)
				}
				// The pattern value lives in the map; copy it into a stable field.
				tracks[ti].current = pattern
				tracks[ti].active = &tracks[ti].current
			}
		}
	}
	return nil
}

func renderBlock(w io.Writer, tracks []trackRuntime, events []scheduled, start int64, frames int, buffer []byte, report *Report) error {
	eventIndex := 0
	for frame := 0; frame < frames; frame++ {
		sample := start + int64(frame)
		for eventIndex < len(events) && events[eventIndex].event.Sample == sample {
			event := events[eventIndex]
			tracks[event.track].voice.NoteOn(event.event.Note, event.event.Velocity, event.event.Slide)
			eventIndex++
		}
		var mixed float32
		for ti := range tracks {
			mixed += tracks[ti].voice.Next() * 0.28
		}
		if abs := float32(math.Abs(float64(mixed))); abs > report.Peak {
			report.Peak = abs
		}
		if mixed > 0.999 {
			mixed = 0.999
		} else if mixed < -0.999 {
			mixed = -0.999
		}
		pcm := int32(math.Round(float64(mixed) * 8388607))
		index := frame * 6
		for channel := 0; channel < 2; channel++ {
			buffer[index+channel*3] = byte(pcm)
			buffer[index+channel*3+1] = byte(pcm >> 8)
			buffer[index+channel*3+2] = byte(pcm >> 16)
		}
	}
	n, err := w.Write(buffer[:frames*6])
	if err != nil {
		return err
	}
	if n != frames*6 {
		return io.ErrShortWrite
	}
	return nil
}

func writeHeader(w io.Writer, sampleRate int, dataBytes uint32) error {
	var header [44]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], dataBytes+36)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 2)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*6))
	binary.LittleEndian.PutUint16(header[32:34], 6)
	binary.LittleEndian.PutUint16(header[34:36], 24)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataBytes)
	n, err := w.Write(header[:])
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	return nil
}
