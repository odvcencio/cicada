package main

import (
	"fmt"
	"strings"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

const seed = 4242

type scenario struct {
	kind                 string
	tracks, rate, block  int
	drive, sends, scenes bool
}

func scenarios() []scenario {
	var all []scenario
	for _, rate := range []int{44100, 48000} {
		for _, block := range []int{128, 256} {
			for _, kind := range []string{"acid", "drums", "graph", "sampler-1", "sampler-1.5"} {
				for _, tracks := range []int{1, 4, 8, 16} {
					for _, drive := range []bool{false, true} {
						for _, sends := range []bool{false, true} {
							for _, scenes := range []bool{false, true} {
								all = append(all, scenario{kind, tracks, rate, block, drive, sends, scenes})
							}
						}
					}
				}
			}
		}
	}
	return all
}

func (s scenario) sampler() bool { return strings.HasPrefix(s.kind, "sampler-") }

func (s scenario) key() string {
	return fmt.Sprintf("kind=%s tracks=%d rate_hz=%d block_frames=%d drive=%t sends=%t scene_every_bar=%t", s.kind, s.tracks, s.rate, s.block, s.drive, s.sends, s.scenes)
}

// source constructs scores in memory; no asset files or parsed example files
// participate in a measurement. An explicit kit keeps drums at one voice/track.
func (s scenario) source() string {
	var b strings.Builder
	fmt.Fprintf(&b, "tempo 120\nkey a minor\nseed %d\n", seed)
	if s.kind == "graph" {
		b.WriteString(`instrument glassbass {
  octave = 2
  param cutoff = 680Hz
  param bite = 0.58
  voice mono {
    let osc = saw(pitch)
    let sub = square(pitch / 2)
    let body = mix(osc, sub, 0.32)
    let shape = env(gate, 330ms)
    out = ladder(body, cutoff * exp2(shape * 3.2), bite) * shape
  }
}
`)
	}
	if s.kind == "drums" {
		b.WriteString("kit pulse { bd = builtin.bd }\n")
	}
	if s.drive {
		b.WriteString("fx drive drive { shape = soft gain = 9dB tone = 9kHz mix = 0.7 }\n")
	}
	if s.sends {
		b.WriteString("fx delay delay { time = 1/8 feedback = 0.35 damp = 6kHz width = 1 mix = 1 }\n")
		b.WriteString("fx reverb reverb { size = 1 decay = 2.4s damp = 8kHz highpass = 120Hz mix = 1 }\n")
	}
	kind := s.kind
	if kind == "graph" {
		kind = "glassbass"
	}
	if kind == "drums" {
		kind = "pulse"
	}
	for track := 0; track < s.tracks; track++ {
		fmt.Fprintf(&b, "track t%d %s { level = -24dB", track, kind)
		if s.kind == "acid" {
			b.WriteString(" cutoff = 680Hz reso = 0.58")
		}
		if s.drive {
			b.WriteString(" insert = drive")
		}
		if s.sends {
			b.WriteString(" send delay = 0.3 send reverb = 0.35")
		}
		b.WriteString(" }\n")
	}
	if s.kind == "drums" {
		b.WriteString("pattern a drums { bd: X.x.X.x.X.x.X.x. }\npattern b drums { bd: X..xX..xX..xX..x }\n")
	} else {
		b.WriteString("pattern a { gate = 75% 1 1 5 1 3 1 7, 1 | 1 5 1 3 1 5 7, 1 }\n")
		b.WriteString("pattern b { gate = 75% 5 1 3 1 5 1 7, 1 | 3 1 5 1 7, 1 5 1 }\n")
	}
	for i, name := range []string{"first", "second"} {
		fmt.Fprintf(&b, "scene %s {\n", name)
		for track := 0; track < s.tracks; track++ {
			fmt.Fprintf(&b, "t%d = %s\n", track, []string{"a", "b"}[i])
		}
		b.WriteString("}\n")
	}
	b.WriteString("song {\n")
	if s.scenes {
		for i := 0; i < 128; i++ {
			b.WriteString("first second\n")
		}
	} else {
		b.WriteString("first*256\n")
	}
	b.WriteString("}\n")
	return b.String()
}

func (s scenario) score() (*notation.Score, engine.Config, error) {
	score, diagnostics := notation.ParseEdition([]byte(s.source()), 2)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, engine.Config{}, fmt.Errorf("%s: %s", d.Code, d.Message)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		return nil, engine.Config{}, fmt.Errorf("project: %v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, s.rate, s.block)
	return score, cfg, err
}

type renderer interface{ Render([]float32, []float32) }

// nativeSession includes the host's command submission and message draining
// in the timed callback. No formatting, growth or construction occurs here.
type nativeSession struct {
	engine                         *engine.Engine
	scenes                         bool
	position, barFrames, queuedBar int64
	fault                          bool
	changes                        uint64
}

func newNative(s scenario, cfg engine.Config) (*nativeSession, error) {
	cfg.LoopSong = true
	if s.scenes {
		cfg.Song = nil
	}
	e, err := engine.New(cfg)
	if err != nil {
		return nil, err
	}
	n := &nativeSession{engine: e, scenes: s.scenes, barFrames: int64(s.rate) * 2, queuedBar: 1}
	if !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		return nil, fmt.Errorf("play rejected")
	}
	if s.scenes && !e.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff}) {
		return nil, fmt.Errorf("initial scene rejected")
	}
	return n, nil
}

func (n *nativeSession) Render(left, right []float32) {
	if n.scenes && n.position >= (n.queuedBar-1)*n.barFrames {
		if !n.engine.Push(cmd.Command{Op: cmd.OpLaunchScene, Track: 0xff, Index: uint16(n.queuedBar % 2), Tick: n.queuedBar * seq.TicksPerBar}) {
			n.fault = true
		}
		n.queuedBar++
	}
	n.engine.Render(left, right)
	n.position += int64(len(left))
	var m cmd.Message
	for n.engine.Poll(&m) {
		if m.Kind == cmd.Fault {
			n.fault = true
		}
	}
	_, n.changes = n.engine.CurrentScene()
}
