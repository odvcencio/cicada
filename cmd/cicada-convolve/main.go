// cicada-convolve renders a score through a checksum-pinned impulse response.
package main

import (
	"flag"
	"fmt"
	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/host/irasset"
	"os"
)

type wetProcessor struct {
	impulse interface {
		Process(float32, float32) (float32, float32)
		Fault() bool
		LatencyFrames() int
	}
	mix        float32
	dryL, dryR []float32
	at         int
}

func (w *wetProcessor) Process(l, r float32) (float32, float32) {
	oldL, oldR := w.dryL[w.at], w.dryR[w.at]
	w.dryL[w.at], w.dryR[w.at] = l, r
	w.at = (w.at + 1) % len(w.dryL)
	wetL, wetR := w.impulse.Process(l, r)
	return oldL*(1-w.mix) + wetL*w.mix, oldR*(1-w.mix) + wetR*w.mix
}
func (w *wetProcessor) LatencyFrames() int { return w.impulse.LatencyFrames() }
func (w *wetProcessor) Fault() bool        { return w.impulse.Fault() }

func run() error {
	score := flag.String("score", "", "score to render")
	output := flag.String("o", "", "output float32 WAV")
	manifest := flag.String("manifest", "assets/ir/manifest.json", "pinned IR manifest")
	asset := flag.String("asset", "room", "IR asset ID")
	wav := flag.String("ir", "", "already fetched exact IR WAV")
	partition := flag.Int("partition", 128, "power-of-two partition frames")
	wet := flag.Float64("mix", .2, "wet proportion 0..1")
	bars := flag.Int("bars", 0, "bars; zero selects full song")
	flag.Parse()
	if *score == "" || *output == "" || *wav == "" || *wet < 0 || *wet > 1 {
		return fmt.Errorf("usage: cicada-convolve -score score.cicada -ir room.wav -asset room -o audio.wav [-mix .2]")
	}
	file, err := os.Open(*manifest)
	if err != nil {
		return err
	}
	m, err := irasset.ReadManifest(file)
	file.Close()
	if err != nil {
		return err
	}
	var entry *irasset.Entry
	for i := range m.Assets {
		if m.Assets[i].ID == *asset {
			entry = &m.Assets[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("unknown IR %q", *asset)
	}
	file, err = os.Open(*wav)
	if err != nil {
		return err
	}
	impulse, err := irasset.Decode(file, *entry, 48000)
	file.Close()
	if err != nil {
		return err
	}
	impulse, err = impulse.Condition(20, 1)
	if err != nil {
		return err
	}
	convolver, err := impulse.Convolver(*partition)
	if err != nil {
		return err
	}
	source, err := os.ReadFile(*score)
	if err != nil {
		return err
	}
	audio, err := audition.Render(source, 48000, *bars, 6)
	if err != nil {
		return err
	}
	p := &wetProcessor{impulse: convolver, mix: float32(*wet), dryL: make([]float32, convolver.LatencyFrames()), dryR: make([]float32, convolver.LatencyFrames())}
	if err = audio.Process(p); err != nil {
		return err
	}
	file, err = os.Create(*output)
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
	fmt.Printf("asset=%s source_rate=%d render_rate=48000 impulse_frames=%d latency_frames=%d output_frames=%d\n", entry.ID, entry.RateHz, len(impulse.Left), p.LatencyFrames(), len(audio.Left))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
