// cicada-mix auditions an opt-in mix chain on a rendered score.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/kernel/loudness"
	"math"
	"os"
)

func run() error {
	score := flag.String("score", "", "score to render")
	output := flag.String("o", "", "output float32 WAV")
	preset := flag.String("preset", "dry", "mix preset")
	rate := flag.Int("rate", 48000, "sample rate")
	bars := flag.Int("bars", 0, "bars; zero selects whole song")
	tail := flag.Float64("tail", 3, "tail seconds")
	gainDB := flag.Float64("gain-db", 0, "gain applied before processing, in dB")
	target := flag.Float64("lufs", 0, "integrated loudness target; zero disables normalization")
	flag.Parse()
	if *score == "" || *output == "" {
		return fmt.Errorf("usage: cicada-mix -score score.cicada -o audio.wav -preset mastering [-lufs -14]")
	}
	params, ok := pro.Preset(*preset)
	if *preset == "dry" {
		params, ok = pro.Params{}, true
	}
	if !ok {
		return fmt.Errorf("unknown mix preset %q", *preset)
	}
	source, err := os.ReadFile(*score)
	if err != nil {
		return err
	}
	audio, err := audition.Render(source, *rate, *bars, *tail)
	if err != nil {
		return err
	}
	if math.IsNaN(*gainDB) || math.IsInf(*gainDB, 0) || *gainDB < -60 || *gainDB > 24 {
		return fmt.Errorf("input gain must be -60..24 dB")
	}
	gain := float32(math.Pow(10, *gainDB/20))
	for i := range audio.Left {
		audio.Left[i] *= gain
		audio.Right[i] *= gain
	}
	chain, err := pro.New(*rate, params)
	if err != nil {
		return err
	}
	if err = audio.Process(chain); err != nil {
		return err
	}
	var normalization *pro.Normalization
	if *target != 0 {
		result, err := pro.NormalizeStereo(*rate, audio.Left, audio.Right, *target, -1)
		if err != nil {
			return err
		}
		normalization = &result
	}
	meter, err := loudness.New(*rate)
	if err != nil {
		return err
	}
	if err = meter.ProcessBlock(audio.Left, audio.Right); err != nil {
		return err
	}
	if err = meter.Finish(); err != nil {
		return err
	}
	file, err := os.Create(*output)
	if err != nil {
		return err
	}
	err = audio.WriteWAV(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	report := map[string]any{"preset": *preset, "input_gain_db": *gainDB, "rate": *rate, "frames": len(audio.Left), "latency_frames": chain.LatencyFrames()}
	metrics := meter.Metrics()
	number := func(v float64) any {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil
		}
		return v
	}
	report["metrics"] = map[string]any{"integrated_lufs": number(metrics.IntegratedLUFS), "true_peak_dbtp": number(metrics.TruePeakDBTP), "sample_peak_dbfs": number(metrics.SamplePeakDBFS), "rms_dbfs": number(metrics.RMSDBFS)}
	if normalization != nil {
		report["normalization"] = map[string]any{"target_lufs": normalization.TargetLUFS, "applied_gain_db": normalization.AppliedGainDB, "peak_limited": normalization.PeakLimited, "reachable": normalization.Reachable, "before_lufs": number(normalization.Before.IntegratedLUFS), "after_lufs": number(normalization.After.IntegratedLUFS), "true_peak_dbtp": number(normalization.After.TruePeakDBTP)}
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
