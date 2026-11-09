package gosxhost_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/gamescore"
	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/gosxhost"
)

const parityQuanta = 7500 // 128 frames each: 20 s at 48 kHz, ten bars at 120 bpm (the phrase boundary is bar 8)

// TestWorkletMatchesNativeKernel plays the same Game Director timeline through
// the native engine and through GoSX's gosx-wasm-audio processor running the
// TinyGo kernel, then compares every output sample.
//
// It needs three inputs and skips without them:
//
//	GOSX_WORKLET_PROCESSOR  path to m31labs.dev/gosx game/audio/worklet/processor.js
//	CICADA_KERNEL_WASM      the kernel module (default build/cicada-kernel.wasm; make build-kernel-wasm)
//	node                    on PATH
func TestWorkletMatchesNativeKernel(t *testing.T) {
	for _, name := range []string{"game-director", "game-music"} {
		t.Run(name, func(t *testing.T) { workletMatchesNative(t, name) })
	}
}

func workletMatchesNative(t *testing.T, name string) {
	processor := os.Getenv("GOSX_WORKLET_PROCESSOR")
	if processor == "" {
		t.Skip("set GOSX_WORKLET_PROCESSOR to GoSX's game/audio/worklet/processor.js")
	}
	kernelPath := os.Getenv("CICADA_KERNEL_WASM")
	if kernelPath == "" {
		kernelPath = filepath.Join("..", "..", "build", "cicada-kernel.wasm")
	}
	if _, err := os.Stat(kernelPath); err != nil {
		t.Skipf("kernel module missing (run make build-kernel-wasm): %v", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}

	score := filepath.Join("..", "..", "examples", name+".cicada")
	loaded, ds, err := project.LoadScore(score, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	p, ds := project.FromScore(loaded)
	for _, d := range ds {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	pack, err := gamescore.Build(p, filepath.Dir(score), 48000)
	if err != nil {
		t.Fatal(err)
	}
	// Through Marshal and ParsePack, as a game would load it.
	data, err := pack.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	pack, err = gosxhost.ParsePack(data)
	if err != nil {
		t.Fatal(err)
	}

	// The kernel decodes the image the same way in both runs.
	var cfg engine.Config
	if err := kernelimage.DecodeInto(pack.Image, pack.SampleRate, 128, &cfg); err != nil {
		t.Fatal(err)
	}
	native, err := engine.NewFromConfig(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	type batch struct {
		Q int    `json:"q"`
		B string `json:"b"`
	}
	var batches []batch
	quantum := 0
	music, err := gosxhost.NewMusic(pack, func(b []byte) error {
		batches = append(batches, batch{quantum, base64.StdEncoding.EncodeToString(b)})
		decoded := make([]cmd.Command, len(b)/cmd.CommandSize)
		n, err := cmd.DecodeCommands(b, uint8(cfg.Tracks), decoded)
		if err != nil {
			return err
		}
		if !native.PushBatch(decoded[:n]) {
			t.Fatal("native command queue full")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	script := map[int]func() error{
		0:    func() error { return music.Start("explore") },
		700:  func() error { return firstErr(music.SetIntensity(0.9), music.SetScene("combat")) },
		1500: func() error { return music.Stinger("pickup") },
		2500: func() error { return music.SetScene("explore") },
		3500: func() error { return music.SetIntensity(0.1) },
	}
	left, right := make([]float32, parityQuanta*128), make([]float32, parityQuanta*128)
	var nativeMessages [][]byte
	var msg cmd.Message
	for quantum = 0; quantum < parityQuanta; quantum++ {
		if fn := script[quantum]; fn != nil {
			if err := fn(); err != nil {
				t.Fatalf("quantum %d: %v", quantum, err)
			}
		}
		native.Render(left[quantum*128:(quantum+1)*128], right[quantum*128:(quantum+1)*128])
		for native.Poll(&msg) {
			enc := cmd.EncodeMessage(msg)
			nativeMessages = append(nativeMessages, enc[:])
			if err := music.HandleMessages(enc[:]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if music.Scene() != "explore" {
		t.Fatalf("native run ended in scene %q, want explore (combat landed and left again)", music.Scene())
	}

	dir := t.TempDir()
	backend := json.RawMessage(gosxhost.BackendJSON())
	tempo, caps := pack.Info()
	timeline, _ := json.Marshal(map[string]any{
		"rate": pack.SampleRate, "quanta": parityQuanta, "image": base64.StdEncoding.EncodeToString(pack.Image),
		"tempoMilli": tempo, "requiredCapabilities": caps, "backend": backend, "batches": batches,
	})
	timelinePath, outPath := filepath.Join(dir, "timeline.json"), filepath.Join(dir, "out.json")
	if err := os.WriteFile(timelinePath, timeline, 0o600); err != nil {
		t.Fatal(err)
	}
	run := exec.Command(node, filepath.Join("..", "..", "tools", "gosx", "worklet-replay.mjs"), processor, kernelPath, timelinePath, outPath)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("worklet replay: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Left, Right string
		Messages    []string
		CPUMicros   map[string]map[string]float64 `json:"cpuMicros"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	decode := func(s string) []float32 {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(b) != parityQuanta*128*4 {
			t.Fatalf("worklet output has %d bytes, want %d (%v)", len(b), parityQuanta*128*4, err)
		}
		f := make([]float32, len(b)/4)
		for i := range f {
			f[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		}
		return f
	}
	wl, wr := decode(got.Left), decode(got.Right)
	var differing int
	var maxDiff float64
	var peak float32
	first := -1
	for i := range left {
		for _, pair := range [][2]float32{{left[i], wl[i]}, {right[i], wr[i]}} {
			if a := float32(math.Abs(float64(pair[0]))); a > peak {
				peak = a
			}
			if pair[0] != pair[1] {
				differing++
				if first < 0 {
					first = i
				}
				maxDiff = math.Max(maxDiff, math.Abs(float64(pair[0])-float64(pair[1])))
			}
		}
	}
	t.Logf("compared %d samples per channel; peak %.4f; differing samples %d; max |diff| %g; worklet CPU per 128-frame quantum, microseconds (Node/V8): steady %v, whole run %v",
		len(left), peak, differing, maxDiff, got.CPUMicros["steady"], got.CPUMicros["all"])
	if peak < 0.01 {
		t.Fatal("native render is silent; the comparison proves nothing")
	}
	if differing != 0 {
		t.Fatalf("worklet differs from native in %d samples (first at frame %d, max diff %g)", differing, first, maxDiff)
	}

	// The messages the worklet reported must be a prefix of the native stream.
	// Playhead and Meter messages are left out: the engine coalesces a pending
	// Playhead and drops Meters when its ring fills, so they depend on how often
	// the host drains (every quantum here, every eighth in the processor). The
	// processor also drains late, so the tail may still be queued in the kernel.
	significant := func(m []byte) bool {
		kind := cmd.Kind(m[0])
		return kind != cmd.Playhead && kind != cmd.Meter
	}
	var nativeSignificant, workletSignificant [][]byte
	for _, m := range nativeMessages {
		if significant(m) {
			nativeSignificant = append(nativeSignificant, m)
		}
	}
	for _, s := range got.Messages {
		b, _ := base64.StdEncoding.DecodeString(s)
		for at := 0; at+cmd.MessageSize <= len(b); at += cmd.MessageSize {
			if significant(b[at : at+cmd.MessageSize]) {
				workletSignificant = append(workletSignificant, b[at:at+cmd.MessageSize])
			}
		}
	}
	// The replay renders 16 quanta past the end to flush the last batch, so the
	// worklet may hold a few messages the native run never produced; the
	// shared prefix must match and the counts must be close.
	shared := min(len(workletSignificant), len(nativeSignificant))
	if shared < 10 || abs(len(workletSignificant)-len(nativeSignificant)) > 4 {
		t.Fatalf("worklet reported %d state messages, native %d", len(workletSignificant), len(nativeSignificant))
	}
	for i, m := range workletSignificant[:shared] {
		if string(m) != string(nativeSignificant[i]) {
			t.Fatalf("message %d differs: worklet %v native %v", i, m, nativeSignificant[i])
		}
	}
	t.Logf("%d worklet and %d native state messages (bars, scene landings, layers, stingers); the first %d are identical", len(workletSignificant), len(nativeSignificant), shared)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}
