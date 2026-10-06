//go:build ignore

// Generate live-only production-kernel images and isolated reference hits.
// Run: GOWORK=off go run tools/audio/kit-images.go output-directory
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/modeledkit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type strike struct {
	Track    int `json:"track"`
	Lane     int `json:"lane"`
	Note     int `json:"note"`
	Velocity int `json:"velocity"`
}
type imageInfo struct {
	Name         string   `json:"name"`
	File         string   `json:"file"`
	ActiveVoices int      `json:"active_voices"`
	Strikes      []strike `json:"strikes"`
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func compile(source string, rate int) engine.Config {
	score, diagnostics := notation.Parse([]byte(source))
	if len(diagnostics) != 0 {
		panic(fmt.Sprint(diagnostics))
	}
	p, diagnostics := project.FromScore(score)
	if len(diagnostics) != 0 {
		panic(fmt.Sprint(diagnostics))
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	check(err)
	// Live commands drive these probes. No arrangement is running during timing.
	cfg.Scenes, cfg.Song, cfg.Patterns = nil, nil, nil
	cfg.LoopSong = false
	return cfg
}

func single(profile string) string {
	return fmt.Sprintf("cicada 2\nseed 4242\nkit probe { bd=model.%s }\ntrack kit probe { level=-12dB }\npattern hit drums { bd:x............... }\nscene groove {\n  kit=hit\n}\nsong { groove }\n", profile)
}

type event struct {
	sample  int
	command cmd.Command
}

func wav(cfg engine.Config, events []event, seconds, bits int, destination string) {
	e, err := engine.New(cfg)
	check(err)
	frames := cfg.SampleRate * seconds
	width := bits / 8
	payload := make([]byte, frames*2*width)
	var left, right [128]float32
	nextEvent := 0
	for offset := 0; offset < frames; {
		for nextEvent < len(events) && events[nextEvent].sample == offset {
			if !e.Push(events[nextEvent].command) {
				panic("command queue rejected event")
			}
			nextEvent++
		}
		n := min(128, frames-offset)
		if nextEvent < len(events) {
			n = min(n, events[nextEvent].sample-offset)
		}
		e.Render(left[:n], right[:n])
		for i := 0; i < n; i++ {
			for channel, value := range [2]float32{left[i], right[i]} {
				position := ((offset+i)*2 + channel) * width
				if bits == 32 {
					binary.LittleEndian.PutUint32(payload[position:], math.Float32bits(value))
				} else {
					integer := int32(math.Round(max(-1, min(1, float64(value))) * 8388607))
					payload[position], payload[position+1], payload[position+2] = byte(integer), byte(integer>>8), byte(integer>>16)
				}
			}
		}
		offset += n
	}
	header := make([]byte, 44)
	copy(header, "RIFF")
	binary.LittleEndian.PutUint32(header[4:], uint32(len(payload)+36))
	copy(header[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(header[16:], 16)
	encoding := uint16(1)
	if bits == 32 {
		encoding = 3
	}
	binary.LittleEndian.PutUint16(header[20:], encoding)
	binary.LittleEndian.PutUint16(header[22:], 2)
	binary.LittleEndian.PutUint32(header[24:], uint32(cfg.SampleRate))
	binary.LittleEndian.PutUint32(header[28:], uint32(cfg.SampleRate*2*width))
	binary.LittleEndian.PutUint16(header[32:], uint16(2*width))
	binary.LittleEndian.PutUint16(header[34:], uint16(bits))
	copy(header[36:], "data")
	binary.LittleEndian.PutUint32(header[40:], uint32(len(payload)))
	f, err := os.Create(destination)
	check(err)
	_, err = f.Write(header)
	check(err)
	_, err = f.Write(payload)
	check(err)
	check(f.Close())
	var commands []map[string]any
	for _, item := range events {
		op := "note_on"
		if item.command.Op == cmd.OpNoteOff {
			op = "note_off_explicit_choke"
		}
		commands = append(commands, map[string]any{"sample": item.sample, "op": op, "track": item.command.Track, "lane": item.command.Index, "note": item.command.Arg0 & 127, "velocity": (item.command.Arg0 >> 8) & 127})
	}
	var bindings []map[string]any
	for track := 0; track < cfg.Tracks; track++ {
		if cfg.Track[track].Kit != nil {
			for lane, binding := range cfg.Track[track].Kit {
				if binding.Kind == engine.KitLaneModeled {
					bindings = append(bindings, map[string]any{"track": track, "lane": lane, "model": modeledkit.Names[binding.Model], "controls": binding.ModelParams, "track_gain_db": cfg.Track[track].GainDB})
				}
			}
		}
	}
	recipe := map[string]any{"generator": "tools/audio/kit-images.go", "render": "native engine, explicit live events; no score gate release", "rate_hz": cfg.SampleRate, "bits": bits, "seconds": seconds, "seed": cfg.Seed, "bindings": bindings, "events": commands}
	encoded, err := json.MarshalIndent(recipe, "", "  ")
	check(err)
	check(os.WriteFile(destination[:len(destination)-4]+".json", append(encoded, '\n'), 0644))
	var message cmd.Message
	for e.Poll(&message) {
		if message.Kind == cmd.Fault {
			panic(fmt.Sprint(message))
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: go run tools/audio/kit-images.go output-directory")
	}
	directory := os.Args[1]
	check(os.MkdirAll(directory, 0755))
	profiles := []string{"kick", "snare", "rimshot", "cross_stick", "tom_low", "tom_mid", "tom_high", "hat_closed", "hat_pedal", "hat_half_open", "hat_open", "ride_bow", "ride_bell", "crash", "splash"}
	var images []imageInfo
	for _, profile := range profiles {
		cfg := compile(single(profile), 48000)
		data, err := kernelimage.Encode(cfg)
		check(err)
		file := profile + ".bin"
		check(os.WriteFile(filepath.Join(directory, file), data, 0644))
		images = append(images, imageInfo{Name: profile, File: file, ActiveVoices: 1, Strikes: []strike{{0, 0, 36, 100}}})
		wav(cfg, []event{{0, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 0, Arg0: 36 | 100<<8}}}, 7, 24, filepath.Join(directory, "audition-"+profile+".wav"))
	}
	full, err := os.ReadFile("examples/modeled-kit/rock-after.cicada")
	check(err)
	cfg := compile(string(full), 48000)
	data, err := kernelimage.Encode(cfg)
	check(err)
	check(os.WriteFile(filepath.Join(directory, "full-kit.bin"), data, 0644))
	all := []strike{}
	for lane := 0; lane < 11; lane++ {
		all = append(all, strike{0, lane, 36, 100})
	}
	for _, lane := range []int{1, 2, 9, 10} {
		all = append(all, strike{1, lane, 36, 100})
	}
	images = append(images, imageInfo{Name: "full-kit", File: "full-kit.bin", ActiveVoices: 15, Strikes: all})
	encoded, err := json.MarshalIndent(images, "", "  ")
	check(err)
	check(os.WriteFile(filepath.Join(directory, "images.json"), append(encoded, '\n'), 0644))
	for _, entry := range []struct {
		name     string
		velocity int
	}{{"kick", 88}, {"snare", 80}, {"hat_closed", 88}, {"hat_open", 64}, {"ride_bow", 64}} {
		for _, rate := range []int{48000, 96000} {
			wav(compile(single(entry.name), rate), []event{{0, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 0, Arg0: uint32(36 | entry.velocity<<8)}}}, 7, 32, filepath.Join(directory, fmt.Sprintf("model-%s-%d.wav", entry.name, rate)))
		}
	}
	for _, profile := range []string{"kick", "snare", "hat_closed", "ride_bow"} {
		cfg := compile(single(profile), 48000)
		var events []event
		for i, velocity := range []int{24, 48, 80, 110} {
			events = append(events, event{i * 2 * 48000, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 0, Arg0: uint32(36 | velocity<<8)}})
		}
		wav(cfg, events, 12, 24, filepath.Join(directory, "velocity-"+profile+".wav"))
	}
	for _, position := range []float64{0, 1} {
		cfg := compile(single("snare"), 48000)
		cfg.Track[0].Kit[0].ModelParams.Position = position
		wav(cfg, []event{{0, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 0, Arg0: 36 | 100<<8}}}, 3, 24, filepath.Join(directory, fmt.Sprintf("position-snare-%.0f.wav", position)))
	}
	for _, profile := range []string{"crash", "splash"} {
		cfg := compile(single(profile), 48000)
		wav(cfg, []event{{0, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 0, Arg0: 36 | 100<<8}}, {24000, cmd.Command{Op: cmd.OpNoteOff, Track: 0, Index: 0}}}, 3, 24, filepath.Join(directory, "choke-"+profile+".wav"))
	}
	hatSource := "cicada 2\nseed 4242\nkit probe {\nch=model.hat_closed\noh=model.hat_open\ncp=model.hat_pedal\n}\ntrack kit probe { level=-12dB }\npattern hit drums { oh:x............... }\nscene groove {\nkit=hit\n}\nsong { groove }\n"
	for _, lane := range []uint16{2, 4} {
		wav(compile(hatSource, 48000), []event{{0, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: 3, Arg0: 46 | 100<<8}}, {24000, cmd.Command{Op: cmd.OpNoteOn, Track: 0, Index: lane, Arg0: 42 | 100<<8}}}, 3, 24, filepath.Join(directory, fmt.Sprintf("choke-hat-lane%d.wav", lane)))
	}
	fmt.Printf("Generated %d live images, ten reference hit WAVs and 25 audition WAVs\n", len(images))
}
