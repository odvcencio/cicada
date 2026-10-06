//go:build wasm_integration

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/director"
)

type gameEvent struct {
	Kind  string  `json:"kind"`
	Name  string  `json:"name,omitempty"`
	Value float32 `json:"value"`
	Tick  string  `json:"tick,omitempty"`
}
type jsReply struct {
	Commands []string `json:"commands"`
	State    string   `json:"state"`
	Layers   uint32   `json:"layers"`
}
type jsGame struct {
	in      io.WriteCloser
	out     *bufio.Scanner
	process *exec.Cmd
}

func newJSGame(t *testing.T) *jsGame {
	t.Helper()
	process := exec.Command("node", "../../sdk/js/parity-client.mjs")
	in, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	process.Stderr = os.Stderr
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	game := &jsGame{in: in, out: bufio.NewScanner(out), process: process}
	t.Cleanup(func() {
		_ = in.Close()
		if err := process.Wait(); err != nil {
			t.Errorf("JS client exited: %v", err)
		}
	})
	return game
}
func (g *jsGame) step(t *testing.T, input any) jsReply {
	t.Helper()
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.in.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	if !g.out.Scan() {
		t.Fatalf("JS client did not respond: %v", g.out.Err())
	}
	var reply jsReply
	if err := json.Unmarshal(g.out.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

type directorRow struct {
	Bar    uint32
	Layers uint32
	State  string
}
type directorClient struct {
	name                        string
	native                      *engine.Engine
	wasm                        api.Module
	goSDK                       *director.Client
	js                          *jsGame
	ctx                         context.Context
	log                         []directorRow
	events                      []cmd.Message
	layers                      uint32
	state                       string
	initialAlloc, initialMemory uint64
}

func (c *directorClient) call(t *testing.T, name string, args ...uint64) uint64 {
	t.Helper()
	fn := c.wasm.ExportedFunction(name)
	if fn == nil {
		t.Fatalf("missing %s", name)
	}
	values, err := fn.Call(c.ctx, args...)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) > 0 {
		return values[0]
	}
	return 0
}
func (c *directorClient) send(t *testing.T, command cmd.Command) {
	t.Helper()
	if c.native != nil {
		if !c.native.Push(command) {
			t.Fatalf("%s rejected %+v", c.name, command)
		}
		return
	}
	data, err := cmd.EncodeCommand(command, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !c.wasm.Memory().Write(uint32(c.call(t, "gosx_audio_cmd_ptr")), data[:]) {
		t.Fatal("command memory")
	}
	if c.call(t, "gosx_audio_cmd_commit", 1) != 0 {
		t.Fatal("WASM command rejected")
	}
}
func (c *directorClient) jsCommands(t *testing.T, reply jsReply) {
	for _, record := range reply.Commands {
		data, err := base64.StdEncoding.DecodeString(record)
		if err != nil {
			t.Fatal(err)
		}
		command, err := cmd.DecodeCommand(data, 3)
		if err != nil {
			t.Fatal(err)
		}
		c.send(t, command)
	}
	c.layers, c.state = reply.Layers, reply.State
}
func (c *directorClient) drive(t *testing.T, events []gameEvent) {
	t.Helper()
	if c.js != nil {
		c.jsCommands(t, c.js.step(t, map[string]any{"events": events}))
		return
	}
	for _, event := range events {
		var tick int64
		if event.Tick != "" {
			if _, err := fmt.Sscan(event.Tick, &tick); err != nil {
				t.Fatal(err)
			}
		}
		var err error
		switch event.Kind {
		case "setup":
			err = c.goSDK.Setup()
		case "state":
			err = c.goSDK.SetState(event.Name, tick)
		case "macro":
			err = c.goSDK.SetMacro(event.Name, event.Value, tick)
		case "stinger":
			err = c.goSDK.TriggerStinger(event.Name, tick)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
func (c *directorClient) render(t *testing.T, frames int) ([]float32, []float32) {
	t.Helper()
	l, r := make([]float32, frames), make([]float32, frames)
	var messages []cmd.Message
	if c.native != nil {
		c.native.Render(l, r)
		var m cmd.Message
		for c.native.Poll(&m) {
			messages = append(messages, m)
		}
	} else {
		c.call(t, "gosx_audio_render", uint64(frames))
		ptr := uint32(c.call(t, "gosx_audio_out_ptr"))
		for i := range frames {
			var ok bool
			l[i], ok = c.wasm.Memory().ReadFloat32Le(ptr + uint32(i*4))
			if !ok {
				t.Fatal("PCM memory")
			}
			r[i], ok = c.wasm.Memory().ReadFloat32Le(ptr + uint32((128+i)*4))
			if !ok {
				t.Fatal("PCM memory")
			}
		}
		n := int(c.call(t, "gosx_audio_msg_drain"))
		ptr = uint32(c.call(t, "gosx_audio_msg_ptr"))
		for i := range n {
			record, ok := c.wasm.Memory().Read(ptr+uint32(i*16), 16)
			if !ok {
				t.Fatal("message memory")
			}
			m, err := cmd.DecodeMessage(record)
			if err != nil {
				t.Fatal(err)
			}
			messages = append(messages, m)
		}
	}
	var relevant []cmd.Message
	var bars []cmd.Message
	for _, m := range messages {
		switch m.Kind {
		case cmd.Fault, cmd.Overload, cmd.Late:
			t.Fatalf("%s control failure: %+v", c.name, m)
		case cmd.Bar:
			bars = append(bars, m)
			relevant = append(relevant, m)
		case cmd.StateChanged, cmd.LayerChanged, cmd.PhraseEnd, cmd.MacroReached, cmd.StingerStarted, cmd.StingerEnded:
			relevant = append(relevant, m)
		}
	}
	c.events = append(c.events, relevant...)
	if c.js != nil && len(relevant) > 0 {
		var records []byte
		for _, m := range relevant {
			record := cmd.EncodeMessage(m)
			records = append(records, record[:]...)
		}
		c.jsCommands(t, c.js.step(t, map[string]any{"messages": base64.StdEncoding.EncodeToString(records)}))
	} else if c.goSDK != nil {
		for _, m := range relevant {
			c.goSDK.Handle(m)
		}
		c.layers, c.state = c.goSDK.LayerMask(), c.goSDK.State()
	}
	for _, bar := range bars {
		c.log = append(c.log, directorRow{Bar: bar.B, Layers: c.layers, State: c.state})
	}
	return l, r
}

// TestAudioWASMDirectorM5 is the four-client, 64-bar Game Director parity gate:
// Go/native, JS/native, Go/WASM and JS/WASM execute the same game events through
// the shipped SDKs, consume their own engine messages, and log layer state.
func TestAudioWASMDirectorM5(t *testing.T) {
	source, err := os.ReadFile("../../examples/game-director.cicada")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(source), "tempo 120", "tempo 300", 1)
	text = strings.Replace(text, "track bed acid { cutoff = 500Hz }", "instrument tone { voice mono { out = sine(pitch) * env(gate, 80ms) } }\ntrack bed tone {}", 1)
	text = strings.Replace(text, "track battle acid { cutoff = 1400Hz }", "track battle tone {}", 1)
	text = strings.Replace(text, "track cue acid { cutoff = 2400Hz }", "track cue tone {}", 1)
	score, ds := notation.Parse([]byte(text))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	const rate = 48000
	cfg, err := project.CompileEngine(p, rate, 128)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := project.DirectorSurfaceOf(p, rate)
	if err != nil {
		t.Fatal(err)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	wasm, err := os.ReadFile("../../build/cicada-kernel.wasm")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	clients := make([]*directorClient, 4)
	for i, name := range []string{"Go/native", "JS/native", "Go/WASM", "JS/WASM"} {
		c := &directorClient{name: name, ctx: ctx}
		clients[i] = c
		if i < 2 {
			c.native, err = engine.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			c.wasm, err = runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(name))
			if err != nil {
				t.Fatal(err)
			}
			c.call(t, "_initialize")
			ptr := uint32(c.call(t, "gosx_audio_project_alloc", uint64(len(image))))
			if ptr == 0 || !c.wasm.Memory().Write(ptr, image) {
				t.Fatal("image memory")
			}
			if c.call(t, "gosx_audio_init", rate, 128, 2) != 0 {
				t.Fatal("init failed")
			}
			c.initialAlloc = c.call(t, "gosx_audio_alloc_bytes")
			c.initialMemory = uint64(c.wasm.Memory().Size())
		}
		if i%2 == 0 {
			c.goSDK, err = director.New(surface, func(command cmd.Command) error { c.send(t, command); return nil })
			if err != nil {
				t.Fatal(err)
			}
		} else {
			c.js = newJSGame(t)
			c.jsCommands(t, c.js.step(t, map[string]any{"surface": surface}))
		}
		c.drive(t, []gameEvent{{Kind: "setup"}, {Kind: "state", Name: "explore"}})
		c.send(t, cmd.Command{Op: cmd.OpPlay, Track: 255})
	}
	clock, _ := seq.NewClock(rate, cfg.BPMMilli)
	sample := int64(0)
	peak := float64(0)
	sounded := false
	for bar := range 64 {
		tick := int64(bar) * seq.TicksPerBar
		value := []float32{.1, .9, .7, 0}[bar/8%4]
		events := []gameEvent{{Kind: "macro", Name: "intensity", Value: value, Tick: fmt.Sprint(tick + 111)}}
		if bar%16 == 4 {
			events = append(events, gameEvent{Kind: "state", Name: "combat", Tick: fmt.Sprint(tick + 333)})
		}
		if bar%16 == 12 {
			events = append(events, gameEvent{Kind: "state", Name: "explore", Tick: fmt.Sprint(tick + 333)})
		}
		if bar%3 == 1 {
			events = append(events, gameEvent{Kind: "stinger", Name: "pickup", Tick: fmt.Sprint(tick + 222)})
		}
		for _, c := range clients {
			c.drive(t, events)
		}
		end := clock.SampleAtTick(tick + seq.TicksPerBar)
		for sample < end {
			n := int(min(int64(128), end-sample))
			var baseL, baseR []float32
			for i, c := range clients {
				l, r := c.render(t, n)
				if i == 0 {
					baseL, baseR = l, r
					continue
				}
				for frame := range n {
					sounded = sounded || l[frame] != 0 || r[frame] != 0
					if math.IsNaN(float64(l[frame])) || math.IsNaN(float64(r[frame])) || math.IsInf(float64(l[frame]), 0) || math.IsInf(float64(r[frame]), 0) {
						t.Fatal("nonfinite PCM")
					}
					peak = max(peak, math.Abs(float64(l[frame]-baseL[frame])), math.Abs(float64(r[frame]-baseR[frame])))
				}
			}
			sample += int64(n)
		}
	}
	masks := map[uint32]bool{}
	states := map[string]bool{}
	stingers, phrases := 0, 0
	for _, row := range clients[0].log {
		masks[row.Layers] = true
		states[row.State] = true
	}
	for _, m := range clients[0].events {
		if m.Kind == cmd.StingerStarted {
			stingers++
		}
		if m.Kind == cmd.PhraseEnd {
			phrases++
		}
	}
	if clients[0].log[0].Layers != 5 || len(masks) < 2 || len(states) < 2 || stingers != 21 || phrases != 7 {
		t.Fatalf("gate did not exercise controls: masks=%v states=%v stingers=%d phrases=%d", masks, states, stingers, phrases)
	}
	for _, c := range clients {
		if len(c.log) != 64 || !reflect.DeepEqual(c.log, clients[0].log) || !reflect.DeepEqual(c.events, clients[0].events) {
			t.Fatalf("%s diverged: bars=%d events=%d", c.name, len(c.log), len(c.events))
		}
		if c.wasm != nil {
			if c.call(t, "gosx_audio_alloc_bytes") != c.initialAlloc || uint64(c.wasm.Memory().Size()) != c.initialMemory {
				t.Fatal("WASM render allocated or grew memory")
			}
		}
		t.Logf("M5 %s bars=64 equal_layer_state=true control_events=%d", c.name, len(c.events))
	}
	if !sounded || peak > 1e-6 {
		t.Fatalf("PCM parity: sounded=%v peak=%g", sounded, peak)
	}
	data, _ := json.Marshal(clients[0].log)
	t.Logf("M5 layer_log_sha256=%x", sha256.Sum256(data))
	t.Logf("M5 clients=4 bars=64 stingers=%d phrase_ends=%d peak_pcm_difference=%g wasm_render_alloc_bytes=0", stingers, phrases, peak)
}
