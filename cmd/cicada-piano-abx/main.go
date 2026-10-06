// Command cicada-piano-abx prepares blind, loudness-matched piano comparisons.
package main

import (
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"m31labs.dev/cicada/kernel/voice/piano"
)

const outputRate = 48000

type event struct {
	Frame    int     `json:"frame"`
	Kind     string  `json:"kind"`
	Note     uint8   `json:"note,omitempty"`
	Velocity uint8   `json:"velocity,omitempty"`
	Pedal    float32 `json:"pedal,omitempty"`
}

type fixture struct {
	ID     string  `json:"id"`
	Prompt string  `json:"prompt"`
	Frames int     `json:"frames"`
	Events []event `json:"events"`
}

type instrument interface {
	NoteOn(uint8, uint8) error
	NoteOff(uint8)
	SetSustain(float32) error
	NextStereo() (float32, float32)
	Reset()
}

type trial struct {
	ID     string `json:"id"`
	Clip   string `json:"clip"`
	A      string `json:"a"`
	B      string `json:"b"`
	X      string `json:"x"`
	Frames int    `json:"frames"`
}

type answer struct {
	Trial  string            `json:"trial"`
	A      string            `json:"a_identity"`
	B      string            `json:"b_identity"`
	X      string            `json:"x_matches"`
	WAVSHA map[string]string `json:"wav_sha256"`
}

type comparison struct {
	Fixture    fixture     `json:"performance"`
	Modeled    measurement `json:"modeled"`
	Sampled    measurement `json:"sampled"`
	Difference float64     `json:"difference_lu"`
}

type trialFile struct {
	name string
	data []byte
}

func blindTrial(rng *rand.Rand, id, clip string, frames int, modeled, sampled []byte) (trial, answer, []trialFile) {
	t := trial{ID: id, Clip: clip, A: id + "-A.wav", B: id + "-B.wav", X: id + "-X.wav", Frames: frames}
	a, b := modeled, sampled
	key := answer{Trial: id, A: "modeled", B: "sampled", X: "A", WAVSHA: make(map[string]string)}
	if rng.IntN(2) == 1 {
		a, b, key.A, key.B = b, a, key.B, key.A
	}
	x := a
	if rng.IntN(2) == 1 {
		x, key.X = b, "B"
	}
	files := []trialFile{{t.A, a}, {t.B, b}, {t.X, x}}
	for _, file := range files {
		key.WAVSHA[file.name] = fmt.Sprintf("%x", sha256.Sum256(file.data))
	}
	return t, key, files
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cicada-piano-abx:", err)
		os.Exit(1)
	}
}

func run() error {
	packPath := flag.String("pack", "", "grand pack directory or its manifest.json (required)")
	out := flag.String("out", "piano-modeled", "new output directory for listener files and organizer metadata")
	trials := flag.Int("trials", 4, "ABX trials per performance (1–32)")
	target := flag.Float64("lufs", -23, "requested integrated loudness; lowered equally if peak headroom requires it")
	seedText := flag.String("seed", "", "optional uint64 randomization seed; omitted uses cryptographic randomness")
	modelSource := flag.String("model-source", "kernel/voice/piano", "modeled piano source directory to hash for reproducibility")
	flag.Parse()
	if *packPath == "" || flag.NArg() != 0 || *trials < 1 || *trials > 32 || math.IsNaN(*target) || math.IsInf(*target, 0) || *target < -40 || *target > -12 {
		return fmt.Errorf("supply -pack, 1–32 trials, and a loudness target between -40 and -12 LUFS")
	}
	seed, err := randomSeed(*seedText)
	if err != nil {
		return err
	}
	pack, err := loadPack(*packPath)
	if err != nil {
		return err
	}
	modelHashes, err := sourceHashes(*modelSource)
	if err != nil {
		return err
	}
	// Refuse overwrites: existing responses and answer keys belong to their run.
	if err := os.Mkdir(*out, 0755); err != nil {
		return fmt.Errorf("create new output directory: %w", err)
	}
	if err := os.Mkdir(filepath.Join(*out, "organizer"), 0700); err != nil {
		return err
	}
	model, err := piano.New(outputRate)
	if err != nil {
		return err
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var publicTrials []trial
	var answers []answer
	var comparisons []comparison
	var maxDifference float64
	for _, f := range performances() {
		start := time.Now()
		sampled, err := pack.prepare(f)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", f.ID, err)
		}
		m, modelTime, err := render(model, f)
		if err != nil {
			return err
		}
		s, sampleTime, err := render(sampled, f)
		if err != nil {
			return err
		}
		m, s, mm, sm, err := matchPair(m, s, *target)
		if err != nil {
			return fmt.Errorf("match %s: %w", f.ID, err)
		}
		mm.RenderSeconds, sm.RenderSeconds = modelTime.Seconds(), sampleTime.Seconds()
		difference := math.Abs(mm.LUFS - sm.LUFS)
		maxDifference = math.Max(maxDifference, difference)
		comparisons = append(comparisons, comparison{f, mm, sm, difference})
		modeledWAV, err := encodeWAV(m)
		if err != nil {
			return err
		}
		sampledWAV, err := encodeWAV(s)
		if err != nil {
			return err
		}
		for i := 0; i < *trials; i++ {
			id := fmt.Sprintf("%s-%02d", f.ID, i+1)
			t, key, files := blindTrial(rng, id, f.ID, f.Frames, modeledWAV, sampledWAV)
			for _, file := range files {
				if err := os.WriteFile(filepath.Join(*out, file.name), file.data, 0644); err != nil {
					return err
				}
			}
			publicTrials, answers = append(publicTrials, t), append(answers, key)
		}
		fmt.Printf("%s: %d trials, matched within %.4f LU (%s)\n", f.ID, *trials, difference, time.Since(start).Round(time.Millisecond))
	}
	// Trial presentation order is independent of reference identity and X choice.
	rng.Shuffle(len(publicTrials), func(i, j int) { publicTrials[i], publicTrials[j] = publicTrials[j], publicTrials[i] })
	listener := struct {
		Format string  `json:"format"`
		Rate   int     `json:"sample_rate"`
		Bits   int     `json:"bits"`
		Trials []trial `json:"trials"`
	}{"cicada.piano-abx/1", outputRate, 24, publicTrials}
	if err := writeJSON(filepath.Join(*out, "trials.json"), listener, 0644); err != nil {
		return err
	}
	private := struct {
		Format       string            `json:"format"`
		ModelSources map[string]string `json:"modeled_source_sha256"`
		Build        map[string]string `json:"build"`
		ModelLicense string            `json:"modeled_license"`
		PackSHA      string            `json:"pack_manifest_sha256"`
		License      string            `json:"sample_license"`
		Assets       []packAsset       `json:"verified_assets"`
		Comparisons  []comparison      `json:"comparisons"`
		Limit        string            `json:"listening_status"`
	}{"cicada.piano-abx-organizer/1", modelHashes, buildIdentity(), "MIT", pack.hash, "CC0-1.0", pack.usedAssets(), comparisons,
		"Listening material only; no listener responses or acoustic-equivalence claim."}
	if err := writeJSON(filepath.Join(*out, "organizer", "manifest.json"), private, 0600); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(*out, "organizer", "answer-key.json"), struct {
		Seed    uint64   `json:"randomization_seed"`
		Answers []answer `json:"answers"`
	}{seed, answers}, 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "LISTEN.txt"), []byte(listenerInstructions), 0644); err != nil {
		return err
	}
	responses := "listener,trial,x_matches_a_or_b,confidence_1_to_5,notes\n"
	for _, t := range publicTrials {
		responses += "," + t.ID + ",,,\n"
	}
	if err := os.WriteFile(filepath.Join(*out, "responses.csv"), []byte(responses), 0644); err != nil {
		return err
	}
	fmt.Printf("Prepared %d blind trials; maximum loudness difference %.4f LU. Keep organizer/ private.\n", len(publicTrials), maxDifference)
	return nil
}

func sourceHashes(path string) (map[string]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("read modeled piano source: %w", err)
	}
	hashes := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		hashes[entry.Name()] = fmt.Sprintf("%x", sha256.Sum256(b))
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("modeled piano source directory has no Go sources")
	}
	return hashes, nil
}

func buildIdentity() map[string]string {
	identity := make(map[string]string)
	if info, ok := debug.ReadBuildInfo(); ok {
		identity["go_version"] = info.GoVersion
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs", "vcs.revision", "vcs.time", "vcs.modified", "GOOS", "GOARCH":
				identity[setting.Key] = setting.Value
			}
		}
	}
	return identity
}

func writeJSON(path string, value any, mode os.FileMode) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), mode)
}

func randomSeed(value string) (uint64, error) {
	if value != "" {
		seed, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("seed must be a uint64: %w", err)
		}
		return seed, nil
	}
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}

const listenerInstructions = `Piano ABX listening set

Use headphones or speakers at a comfortable, fixed level. Disable player
normalization, EQ, crossfade, spatial effects, and volume changes between files.

Follow the trial order in trials.json. For each trial, listen to A and B, then X.
You may replay and switch freely. X is exactly one reference: decide whether
X matches A or B. Record your answer, confidence, and observations in a copy of
responses.csv. Do not examine organizer/ or compare file bytes or checksums.

The files contain identical note, velocity, and pedal commands. They use stereo
48 kHz PCM24 and are matched within 0.1 LU with ITU-R BS.1770-4 integrated
loudness measurement. Constant gain preserves attacks and dynamics; no limiter
or reverb is added. Some trials include natural decays and pedal release.

Organizers: distribute this directory without organizer/. Collect responses
before revealing identities. Use independent response sheets for each listener.
These files establish a comparison protocol; they do not establish that the
instruments sound equivalent or meet a ten-listener acceptance threshold.
`
