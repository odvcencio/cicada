// Package demobrowser owns the browser demo's scores and immutable page assets.
package demobrowser

import (
	_ "embed"
	"fmt"
	"strings"

	"m31labs.dev/cicada/host/demopolicy"
	"m31labs.dev/cicada/host/demoweb"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

//go:embed index.html
var page []byte

//go:embed page.js
var script []byte

//go:embed style.css
var style []byte

type Preset struct {
	ID, Name, Source string
}

const ensemble = `cicada 2
title "Night garden"
tempo 112
key a minor
seed 4242
instrument glass {
  voice mono {
    out = (sine(pitch) + sine(pitch * 2.01) * 0.3) * env(gate, 350ms) * 0.25
  }
}
kit acoustic {
  bd = model.kick
  sd = model.snare
  ch = model.hat_closed
}
track bass acid { cutoff = 620Hz level = 0.4dB }
track keys model_marimba { level = 0.4dB }
track lead glass { level = 0.4dB }
track drums acoustic { level = -3.6dB }
pattern bassline { 1 . 1 . 3 . 5 . | 1 . 1 3 . 5 7 . }
pattern melody { a4 . c5 . e5 . c5 . | a4 . g4 . e4 . g4 . }
pattern sparkle { . . e4 . . . a4 . | . . c5 . . . g4 . }
pattern beat drums {
  bd: x... x... x... x...
  sd: .... x... .... x...
  ch: ..x. ..x. ..x. ..x.
}
scene garden { bass = bassline keys = melody lead = sparkle drums = beat }
song { garden*999 garden*999 }
`

func Presets() []Preset {
	presets := []Preset{{ID: "ensemble", Name: "Night garden · four instruments", Source: ensemble}}
	// Split the sketch gain between track and master, retaining their +6 dB
	// limits and the kernel's safety limiter. Custom edits keep these levels.
	gains := [...]float64{12.6, 13.7, 14.2, 23.9, 18.1, 22, 18.8, 24, 24}
	for i, name := range modal.Names {
		trackGain := min(gains[i], 18)
		velocity := ""
		if name == "felt" {
			velocity = "\n  velocity: 127 . . . . . . . . . . . . . . .\n"
		}
		presets = append(presets, Preset{ID: name, Name: strings.ToUpper(name[:1]) + name[1:], Source: fmt.Sprintf(`cicada 2
title "%s"
tempo 108
key c major
seed 4242
track sound model_%s { level = %.1fdB }
master { level = %.1fdB }
pattern phrase { c4 . e4 . g4 . c5 . | c4 c4 g4 . e4 . . . %s}
scene demo { sound = phrase }
song { demo*999 demo*999 }
`, name, name, -12+trackGain, gains[i]-trackGain, velocity)})
	}
	presets = append(presets,
		Preset{ID: "kit", Name: "Modeled drum kit", Source: ensemble[:strings.Index(ensemble, "track bass")] + `track drums acoustic { level = 6dB bd_level = -4.3dB sd_level = -4.3dB ch_level = -4.3dB }
master { level = 6dB }
pattern beat drums {
 bd: x... x... x... x...
 sd: .... x... .... x...
 ch: x.x. x.x. x.x. x.x.
}
scene demo { drums = beat }
song { demo*999 demo*999 }
`},
		Preset{ID: "graph", Name: "Glass · authored synth", Source: ensemble[:strings.Index(ensemble, "kit acoustic")] + `track lead glass { level = 6dB }
master { level = 1.5dB }
pattern phrase { a3 . c4 . e4 . g4 . | a4 . g4 . e4 . c4 . }
scene demo { lead = phrase }
song { demo*999 demo*999 }
`})
	return presets
}

func Parse(source []byte) (*project.Project, error) {
	if len(notation.ReadImports(notation.SourceFile{Source: source})) != 0 {
		return nil, fmt.Errorf("use instruments defined in this score; external files are unavailable")
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("line %d: %s", d.Position.Line, d.Message)
		}
	}
	p, diagnostics := project.FromScore(score)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("line %d: %s", d.Position.Line, d.Message)
		}
	}
	// Browser sessions have no resolver or file access. External assets and
	// libraries need a bundled instrument pack rather than a server path.
	if len(p.Assets) != 0 {
		return nil, fmt.Errorf("use instruments defined in this score; external files are unavailable")
	}
	if p.NeedsKeysEngine() {
		return nil, fmt.Errorf("keyboard instruments are unavailable in this demo; choose a modeled instrument, acid bass, or an authored synth")
	}
	if err := project.ValidateProject(p); err != nil {
		return nil, err
	}
	return p, nil
}

func Prepare(source []byte, rate int) (*project.Project, engine.Config, []byte, error) {
	p, err := Parse(source)
	if err != nil {
		return nil, engine.Config{}, nil, err
	}
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		return nil, engine.Config{}, nil, err
	}
	image, err := kernelimage.Encode(cfg)
	return p, cfg, image, err
}

func NewSession() (*demopolicy.Session, error) {
	return demopolicy.NewSession([]byte(ensemble), func(source []byte) error {
		_, _, _, err := Prepare(source, 48000)
		return err
	})
}

// Build publishes a fixed set of assets, with no directory traversal or APIs.
func Build(revision, host string, kernel, bridge, runtime []byte, processor []byte) demoweb.Build {
	return demoweb.Build{Revision: revision, Host: host, Assets: map[string]demoweb.Asset{
		"/":                    {ContentType: "text/html; charset=utf-8", Bytes: page},
		"/assets/demo.js":      {ContentType: "application/javascript", Bytes: script},
		"/assets/demo.css":     {ContentType: "text/css", Bytes: style},
		"/assets/wasm_exec.js": {ContentType: "application/javascript", Bytes: runtime},
		"/assets/demo.wasm":    {ContentType: "application/wasm", Bytes: bridge},
		"/assets/kernel.wasm":  {ContentType: "application/wasm", Bytes: kernel},
		"/audio/processor.js":  {ContentType: "application/javascript", Bytes: processor},
	}}
}
