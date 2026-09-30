package engine

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/kernel/voice/drum"
)

// Test-only controls let the renderer investigation change private smoothing
// state without adding diagnostic switches to the native/WASM engine API.
// Generate the input with render.TestRenderDriftProbes, then set
// CICADA_ENGINE_DRIFT_CONFIG to the resulting chorus-control.json.
func TestEngineRenderDriftControls(t *testing.T) {
	path := os.Getenv("CICADA_ENGINE_DRIFT_CONFIG")
	if path == "" {
		t.Skip("set CICADA_ENGINE_DRIFT_CONFIG to run private-state controls")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Config  Config
		Bars    int
		TailSec float64
		Trim    int
	}
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	for _, scene := range input.Config.Scenes {
		if len(scene.Settings) != 0 {
			t.Fatal("static-parameter controls require no scene settings")
		}
	}
	clock, err := seq.NewClock(input.Config.SampleRate, input.Config.BPMMilli)
	if err != nil {
		t.Fatal(err)
	}
	end := int(clock.SampleAtTick(int64(input.Bars) * seq.TicksPerBar))
	n := end + int(math.Ceil(input.TailSec*float64(input.Config.SampleRate))) + input.Trim
	var referenceL, referenceR []float32
	for _, variant := range []string{"reference", "paramAlpha-immediate", "mix-immediate", "voice-budget-exact", "params-at-note-on", "reset-noise-state", "flush-output-denormals"} {
		t.Run(variant, func(t *testing.T) {
			cfg := input.Config
			if variant == "voice-budget-exact" {
				cfg.MaxVoices = 9
			} // three mono voices and six enabled drum lanes
			player, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "paramAlpha-immediate":
				for i := range player.paramAlpha {
					player.paramAlpha[i] = 1
				}
			case "mix-immediate":
				for i := 0; i < player.tracks; i++ {
					player.voices[i].mixSmooth = 1
					player.voices[i].sendSmooth = 1
				}
			case "reset-noise-state":
				player.Reset()
			}
			if !player.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
				t.Fatal("play rejected")
			}
			left, right := make([]float32, n), make([]float32, n)
			for at := 0; at < n; {
				if at == end {
					if !player.Push(cmd.Command{Op: cmd.OpStop, Track: 0xff}) {
						t.Fatal("stop rejected")
					}
				}
				frames := min(cfg.MaxBlock, n-at)
				if at < end {
					frames = min(frames, end-at)
				}
				if variant == "params-at-note-on" { // one-frame blocks set the authored values immediately before every note callback
					frames = 1
					for i := 0; i < player.tracks; i++ {
						v := &player.voices[i]
						if v.acid != nil {
							if err := v.acid.SetParams(v.acidTarget); err != nil {
								t.Fatal(err)
							}
						}
						if v.drums != nil {
							for lane, p := range v.drumTargets {
								if p.Decay > 0 {
									if err := v.drums.SetParams(drum.Lane(lane), p); err != nil {
										t.Fatal(err)
									}
								}
							}
						}
					}
				}
				player.Render(left[at:at+frames], right[at:at+frames])
				var message cmd.Message
				for player.Poll(&message) {
					if message.Kind == cmd.Fault {
						t.Fatalf("engine fault: %+v", message)
					}
				}
				at += frames
			}
			if variant == "flush-output-denormals" {
				for i := range left {
					if math.Abs(float64(left[i])) < float64(math.Float32frombits(0x00800000)) {
						left[i] = 0
					}
					if math.Abs(float64(right[i])) < float64(math.Float32frombits(0x00800000)) {
						right[i] = 0
					}
				}
			}
			if variant == "reference" {
				referenceL, referenceR = left, right
			}
			var peak float64
			for i := range left {
				peak = max(peak, math.Abs(float64(left[i])-float64(referenceL[i])), math.Abs(float64(right[i])-float64(referenceR[i])))
			}
			if peak != 0 {
				t.Fatalf("control changed output by %.9g", peak)
			}
			// Byte-identical PCM implies identical fingerprints at every band.
			t.Logf("CAUSE %s mean_db=0 max_db=0 peak=%.9f", variant, peak)
			pcm := make([]byte, (n-input.Trim)*8)
			for i := input.Trim; i < n; i++ {
				binary.LittleEndian.PutUint32(pcm[(i-input.Trim)*8:], math.Float32bits(left[i]))
				binary.LittleEndian.PutUint32(pcm[(i-input.Trim)*8+4:], math.Float32bits(right[i]))
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "control."+variant+".f32"), pcm, 0644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
