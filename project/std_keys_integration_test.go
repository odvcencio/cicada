package project

import (
	"math"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/notation"
)

// Every public export must render exactly like its direct native patch.
func TestStdKeysImportedPresetsRenderExactlyLikeNativePatches(t *testing.T) {
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	for _, name := range keyboard.Names {
		t.Run(name, func(t *testing.T) {
			path := stdKeysExamplePath(name)
			score, ds, err := LoadScore(path, nil)
			if err != nil || score == nil || hasErrors(ds) {
				t.Fatalf("imported keys score: %v %+v", err, ds)
			}
			imported, ds := FromScore(score)
			if imported == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(string(data), "import \"std/keys\"\n", "", 1)
			source = strings.Replace(source, "track part keys."+name, "track part "+name, 1)
			directScore, ds := notation.Parse([]byte(source))
			if directScore == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			direct, ds := FromScore(directScore)
			if direct == nil || hasErrors(ds) {
				t.Fatal(ds)
			}
			for _, rate := range []int{44100, 48000} {
				makeEngine := func(p *Project) *engine.Engine {
					cfg, err := CompileEngine(p, rate, 128)
					if err != nil {
						t.Fatal(err)
					}
					if cfg.Track[0].Kind != engine.VoiceKeys || cfg.Track[0].Keys.Patch != keyboard.ID(name) {
						t.Fatal("library preset lost its native keyboard engine")
					}
					e, err := engine.New(cfg)
					if err != nil || !e.Push(cmd.Command{Op: cmd.OpPlay, Track: 255}) {
						t.Fatalf("native engine: %v", err)
					}
					return e
				}
				a, b := makeEngine(imported), makeEngine(direct)
				var aL, aR, bL, bR [128]float32
				energy := float64(0)
				for block := 0; block < 128; block++ {
					a.Render(aL[:], aR[:])
					b.Render(bL[:], bR[:])
					for frame := range aL {
						if math.Float32bits(aL[frame]) != math.Float32bits(bL[frame]) || math.Float32bits(aR[frame]) != math.Float32bits(bR[frame]) {
							t.Fatalf("rate=%d block=%d frame=%d preset/native samples differ", rate, block, frame)
						}
						energy += float64(aL[frame]*aL[frame] + aR[frame]*aR[frame])
					}
				}
				if energy == 0 {
					t.Fatalf("rate=%d imported native keyboard is silent", rate)
				}
			}
		})
	}
}
