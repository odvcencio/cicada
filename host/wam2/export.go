// Package wam2 packages a compiled score as a Web Audio Modules 2 instrument.
package wam2

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/project"
)

//go:embed *.js *.html *.txt
var assets embed.FS

type Options struct {
	KernelPath string
	SDKPath    string
	MIDITrack  string
}

type macro struct {
	ID       string  `json:"id"`
	Index    int     `json:"index"`
	Default  float64 `json:"defaultValue"`
	SmoothMS float64 `json:"smoothMs"`
}

type manifest struct {
	Version   int               `json:"version"`
	ScoreID   string            `json:"scoreId"`
	Macros    []macro           `json:"macros"`
	Images    map[string]string `json:"images"`
	Setup     map[string][]byte `json:"setup"`
	MIDITrack int               `json:"midiTrack"`
	MIDIPiano bool              `json:"midiPiano"`
	MIDIDrums bool              `json:"midiDrums"`
	DrumNotes []int             `json:"drumNotes"`
	Tempo     float64           `json:"tempo"`
}

// Export creates a new directory. Preparation completes before publication;
// an existing output directory is never overwritten.
func Export(p *project.Project, root, output string, o Options) error {
	if err := project.ValidateProject(p); err != nil {
		return err
	}
	if output == "" {
		return fmt.Errorf("an output directory is required")
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("output directory already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	kernel, err := os.ReadFile(o.KernelPath)
	if err != nil {
		return fmt.Errorf("read kernel (run make build-wam2): %w", err)
	}
	if len(kernel) < 8 || string(kernel[:8]) != "\x00asm\x01\x00\x00\x00" {
		return fmt.Errorf("kernel must be a WebAssembly module")
	}
	sdk, err := os.ReadFile(filepath.Join(o.SDKPath, "dist", "index.js"))
	if err != nil {
		return fmt.Errorf("read WAM SDK (run make build-wam2): %w", err)
	}
	license, err := os.ReadFile(filepath.Join(o.SDKPath, "src", "RingBuffer_LICENSE.txt"))
	if err != nil {
		return err
	}
	files := map[string][]byte{"kernel.wasm": kernel, "sdk.js": sdk, "RingBuffer_LICENSE.txt": license}
	m := manifest{Version: 1, Macros: []macro{}, Images: map[string]string{}, Setup: map[string][]byte{}, MIDITrack: -1, Tempo: float64(p.TempoMilli) / 1000}
	for _, note := range drum.MIDINotes {
		m.DrumNotes = append(m.DrumNotes, int(note))
	}
	if p.Live != nil {
		for i, v := range p.Live.Macros {
			m.Macros = append(m.Macros, macro{v.Name, i, v.Default, v.SmoothMS})
		}
	}
	hash := sha256.New()
	for _, rate := range []int{44100, 48000, 96000} {
		cfg, err := schedule.Compile(p, root, rate, 128)
		if err != nil {
			return err
		}
		if rate == 48000 {
			for i, t := range p.Tracks {
				kind := cfg.Track[i].Kind
				eligible := cfg.Track[i].Polyphony == 0 && kind != engine.VoiceAudio
				if o.MIDITrack != "" && t.ID == o.MIDITrack {
					if !eligible {
						return fmt.Errorf("MIDI track requires a live-note voice without graph polyphony")
					}
					m.MIDITrack, m.MIDIPiano, m.MIDIDrums = i, kind == engine.VoicePiano, kind == engine.VoiceDrums
					break
				}
				if o.MIDITrack == "" && m.MIDITrack < 0 && eligible {
					m.MIDITrack, m.MIDIPiano, m.MIDIDrums = i, kind == engine.VoicePiano, kind == engine.VoiceDrums
				}
			}
			if m.MIDITrack < 0 {
				return fmt.Errorf("select a MIDI track with a live-note voice")
			}
		}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			return err
		}
		key := strconv.Itoa(rate)
		name := "score-" + key + ".bin"
		m.Images[key], files[name] = name, image
		hash.Write(image)
		for _, c := range project.LiveCommands(p) {
			encoded, err := cmd.EncodeCommand(c, uint8(len(p.Tracks)))
			if err != nil {
				return err
			}
			m.Setup[key] = append(m.Setup[key], encoded[:]...)
		}
	}
	identity, _ := json.Marshal(m)
	hash.Write(identity)
	m.ScoreID = hex.EncodeToString(hash.Sum(nil))
	descriptor := map[string]any{
		"identifier": "dev.m31labs.cicada." + m.ScoreID[:16], "name": p.Title, "vendor": "Cicada", "version": "1.0.0", "apiVersion": "2.0.0",
		"description": "A Cicada score instrument with live macros and MIDI input.", "website": "https://github.com/odvcencio/cicada", "keywords": []string{"instrument", "score"},
		"isInstrument": true, "hasAudioInput": false, "hasAudioOutput": true, "hasAutomationInput": true, "hasAutomationOutput": false,
		"hasMidiInput": true, "hasMidiOutput": false, "hasMpeInput": false, "hasMpeOutput": false, "hasOscInput": false, "hasOscOutput": false, "hasSysexInput": false, "hasSysexOutput": false,
	}
	if p.Title == "" {
		descriptor["name"] = "Cicada instrument"
	}
	for name, value := range map[string]any{"descriptor.json": descriptor, "score.json": m} {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		files[name] = append(data, '\n')
	}
	entries, _ := assets.ReadDir(".")
	for _, entry := range entries {
		data, err := assets.ReadFile(entry.Name())
		if err != nil {
			return err
		}
		files[entry.Name()] = data
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(parent, ".cicada-wam-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(temp, name), data, 0644); err != nil {
			return err
		}
	}
	return os.Rename(temp, output)
}
