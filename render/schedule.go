package render

import (
	"fmt"
	"io"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"math"
)

func renderScheduleWAV(score *notation.Score, opts Options, w io.Writer, stemsDir string, outputGain float32) (Report, error) {
	var report Report
	if opts.SampleRate == 0 {
		opts.SampleRate = 48000
	}
	if opts.Bits == 0 {
		opts.Bits = 24
	}
	if opts.Block == 0 {
		opts.Block = 4096
	}
	if opts.Block < 1 || opts.Block > 4096 || opts.TailSec < 0 || opts.TailSec > 10 || math.IsNaN(opts.TailSec) || math.IsInf(opts.TailSec, 0) || math.IsNaN(opts.MasterGainDB) || math.IsInf(opts.MasterGainDB, 0) || opts.MasterGainDB < -120 || opts.MasterGainDB > 24 {
		return report, fmt.Errorf("invalid scheduled render options")
	}
	p, ds := project.FromScore(score)
	if p == nil {
		return report, fmt.Errorf("invalid scheduled score: %+v", ds)
	}
	root := opts.AssetRoot
	if root == "" {
		root = "."
	}
	cfg, err := schedule.Compile(p, root, opts.SampleRate, opts.Block)
	if err != nil {
		return report, err
	}
	cfg.MasterGainDB += opts.MasterGainDB
	cfg.MasterBiasL, cfg.MasterBiasR = opts.MasterBiasL, opts.MasterBiasR
	player, err := engine.New(cfg)
	if err != nil {
		return report, err
	}
	events, err := project.CompileSchedule(p)
	if err != nil {
		return report, err
	}
	var duration int64
	for _, v := range events {
		duration = max(duration, v.Tick, v.EndTick)
	}
	songBars := int((duration + seq.TicksPerBar - 1) / seq.TicksPerBar)
	if opts.From < 0 || opts.From >= songBars || opts.Bars < 0 || opts.Bars > songBars-opts.From {
		return report, fmt.Errorf("requested range must fit the arrangement")
	}
	endTick := int64(songBars) * seq.TicksPerBar
	if opts.Bars > 0 {
		endTick = int64(opts.From+opts.Bars) * seq.TicksPerBar
	}
	clock, err := seq.NewClock(opts.SampleRate, score.TempoMilli)
	if err != nil {
		return report, err
	}
	fromFrame := clock.SampleAtTick(int64(opts.From) * seq.TicksPerBar)
	endFrame := clock.SampleAtTick(endTick)
	report = Report{SampleRate: opts.SampleRate, Bars: int((endTick+seq.TicksPerBar-1)/seq.TicksPerBar) - opts.From, From: opts.From, MasterGainDB: cfg.MasterGainDB, TailFrames: int64(math.Ceil(opts.TailSec * float64(opts.SampleRate)))}
	report.Frames = endFrame + report.TailFrames - fromFrame
	dither := opts.Dither == nil || *opts.Dither
	encoder, err := newWAVEncoder(opts.Bits, dither, score.Seed, outputGain)
	if err != nil {
		return report, err
	}
	dataBytes := report.Frames * int64(encoder.frameBytes())
	if dataBytes > int64(^uint32(0))-60 {
		return report, fmt.Errorf("WAV exceeds RIFF size limit")
	}
	if err = writeWAVHeader(w, opts.SampleRate, opts.Bits, uint32(dataBytes)); err != nil {
		return report, err
	}
	var stems *stemOutput
	if stemsDir != "" {
		stems, err = newStemOutput(stemsDir, p, report)
		if err != nil {
			return report, err
		}
		defer stems.abort()
		stems.skipFrames = fromFrame
	}
	latency := int64(player.LatencyFrames())
	insertLatency := int64(player.TrackLatencyFrames())
	metricStart := fromFrame + insertLatency
	if opts.From == 0 {
		metricStart = 0
	}
	metricEnd := endFrame + report.TailFrames + insertLatency
	ceiling := math.Pow(10, mix.CeilingDB/20)
	taps := make([]engine.TapFrame, opts.Block)
	left, right := make([]float32, opts.Block), make([]float32, opts.Block)
	buffer := make([]byte, opts.Block*encoder.frameBytes())
	player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	var message cmd.Message
	for position := int64(0); position < endFrame+report.TailFrames+latency; {
		frames := int(min(int64(opts.Block), endFrame+report.TailFrames+latency-position))
		if position < endFrame {
			frames = int(min(int64(frames), endFrame-position))
		} else if position == endFrame {
			player.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff})
		}
		player.RenderWithTaps(left[:frames], right[:frames], taps[:frames])
		for player.Poll(&message) {
			if message.Kind == cmd.Fault {
				return report, fmt.Errorf("scheduled render engine fault %d", message.A)
			}
		}
		written := 0
		for i := 0; i < frames; i++ {
			tap := &taps[i]
			if at := position + int64(i); at >= metricStart && at < metricEnd {
				for _, value := range [...]float32{tap.PreMaster.Left, tap.PreMaster.Right} {
					peak := math.Abs(float64(value))
					report.Peak = max(report.Peak, float32(peak))
					if peak > ceiling {
						report.PreLimiterOvers++
					}
				}
			}
			report.MaxLimiterGainReductionDB = max(report.MaxLimiterGainReductionDB, tap.LimiterReductionDB)
			if stems != nil {
				for t := range p.Tracks {
					track := tap.Tracks[t]
					gain := float32(mix.MusicGain)
					if cfg.Track[t].BusSFX {
						gain = 1
					}
					stems.frame[t] = stemPair{track.Left * gain, track.Right * gain}
				}
				stems.returnA(tap.ReturnA.Left, tap.ReturnA.Right)
				stems.returnB(tap.ReturnB.Left, tap.ReturnB.Right)
				stems.buses(tap.Music.Left, tap.Music.Right, tap.SFX.Left, tap.SFX.Right)
				stems.appendMixFrame(position + int64(i) - insertLatency)
			}
			if position+int64(i) < latency {
				continue
			}
			if position+int64(i) < fromFrame+latency {
				encoder.advanceDither()
				continue
			}
			l, r := left[i], right[i]
			if stems != nil {
				stems.appendMaster(l*encoder.gain, r*encoder.gain)
			}
			encoder.writeFrame(buffer[written:written+encoder.frameBytes()], l, r, ceiling, &report)
			written += encoder.frameBytes()
		}
		if n, writeErr := w.Write(buffer[:written]); writeErr != nil {
			return report, writeErr
		} else if n != written {
			return report, io.ErrShortWrite
		}
		if stems != nil {
			if err = stems.flush(); err != nil {
				return report, err
			}
		}
		position += int64(frames)
	}
	if err = writeMetadata(w, uint32(score.TempoMilli), uint32(report.Bars), uint32(report.TailFrames)); err != nil {
		return report, err
	}
	if stems != nil {
		if err = stems.finish(uint32(score.TempoMilli)); err != nil {
			return report, err
		}
	}
	return report, nil
}
