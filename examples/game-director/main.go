// A minimal game loop that drives music and writes a shared JS control manifest.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/director"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	scorePath := flag.String("score", "examples/game-director.cicada", "score file")
	manifestPath := flag.String("manifest", "", "optional JSON manifest output")
	flag.Parse()
	score, ds, err := project.LoadScore(*scorePath, nil)
	if err != nil {
		return err
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return d
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return d
		}
	}
	const rate = 48000
	surface, err := project.DirectorSurfaceOf(p, rate)
	if err != nil {
		return err
	}
	if *manifestPath != "" {
		data, err := json.MarshalIndent(surface, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(*manifestPath, append(data, '\n'), 0644); err != nil {
			return err
		}
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		return err
	}
	music, err := engine.New(cfg)
	if err != nil {
		return err
	}
	// This example owns Render and SDK calls on one goroutine. A real game sends
	// records through its bounded queue to the audio thread instead.
	game, err := director.New(surface, func(c cmd.Command) error {
		if !music.Push(c) {
			return fmt.Errorf("music command queue is full")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := game.Setup(); err != nil {
		return err
	}
	if err := game.SetState("explore", 0); err != nil {
		return err
	}
	if !music.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
		return fmt.Errorf("play rejected")
	}
	clock, _ := seq.NewClock(rate, cfg.BPMMilli)
	var l, r [128]float32
	var sample int64
	for bar := range 8 {
		tick := int64(bar) * seq.TicksPerBar
		if bar == 2 {
			if err := game.SetMacro("intensity", .9, tick+111); err != nil {
				return err
			}
			if err := game.SetState("combat", tick+333); err != nil {
				return err
			}
		}
		if bar == 4 {
			if err := game.TriggerStinger("pickup", tick+222); err != nil {
				return err
			}
		}
		end := clock.SampleAtTick(tick + seq.TicksPerBar)
		for sample < end {
			frames := int(min(int64(len(l)), end-sample))
			music.Render(l[:frames], r[:frames])
			sample += int64(frames)
			var message cmd.Message
			for music.Poll(&message) {
				if message.Kind == cmd.Fault || message.Kind == cmd.Overload {
					return fmt.Errorf("music fault: %+v", message)
				}
				game.Handle(message)
			}
		}
		fmt.Printf("bar=%d state=%s layers=%03b\n", bar+1, game.State(), game.LayerMask())
	}
	return nil
}
