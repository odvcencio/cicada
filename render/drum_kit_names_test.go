package render

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

// kitNameScore plays every lane of one std/drum-machines kit for two bars.
func kitNameScore(kit string) string {
	return "cicada 2\nimport \"std/drum-machines\"\ntempo 120\nseed 17\n" +
		"track k drum-machines." + kit + " { level = -6dB }\n" +
		"pattern p drums steps=16 {\n" +
		"  bd: X... ..x. x... ..x.\n  sd: .... x... .... X...\n  ch: x.x. x.x. x.x. x.x.\n  oh: .... .... ..x. ....\n" +
		"  cp: .... .... .... x...\n  rs: .... ...x .... ....\n  lt: .... .... .... ...x\n  mt: .... .... .... .x..\n" +
		"  ht: .... .... .... ..x.\n  cb: .... ..x. .... ..x.\n  cy: X... .... .... ....\n}\n" +
		"scene main { k = p }\nsong { main main }\n"
}

// renderKitDigest compiles the kit's score and hashes the exact float32 bits
// of two seconds of native engine output.
func renderKitDigest(t *testing.T, kit string) string {
	t.Helper()
	t.Setenv("CICADA_LIBRARY", t.TempDir())
	root := t.TempDir()
	main := filepath.Join(root, "main.cicada")
	if err := os.WriteFile(main, []byte(kitNameScore(kit)), 0o644); err != nil {
		t.Fatal(err)
	}
	sources, err := project.ReadSources(main, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0o644); err != nil {
		t.Fatal(err)
	}
	score, ds, err := project.LoadScore(main, nil)
	if err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("load %s: %v %+v", kit, err, ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatalf("project %s: %+v", kit, ds)
	}
	cfg, err := project.CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
	h := sha256.New()
	var l, r [128]float32
	var word [4]byte
	for frame := 0; frame < 2*48000; frame += 128 {
		e.Render(l[:], r[:])
		for i := range l {
			binary.LittleEndian.PutUint32(word[:], math.Float32bits(l[i]))
			h.Write(word[:])
			binary.LittleEndian.PutUint32(word[:], math.Float32bits(r[i]))
			h.Write(word[:])
		}
		var m cmd.Message
		for e.Poll(&m) {
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The digests were taken from the library before the kits were renamed.
var kitDigests = map[string]string{
	"eight-oh-eight": "326715dc8b439dbedf8a6b37c6f49f02e3740303ee9f8b026f463262f1a09784",
	"nine-oh-nine":   "ba0bc7c74a7492395e34c5b864860deeb8a940af0c930de333481ae71439237f",
}

func TestOldDrumKitNamesRenderUnchanged(t *testing.T) {
	for kit, want := range kitDigests {
		if got := renderKitDigest(t, kit); got != want {
			t.Errorf("%s render digest %s, want %s", kit, got, want)
		}
	}
}

func TestNeutralDrumKitNamesMatchOldKits(t *testing.T) {
	for neutral, old := range map[string]string{"boom-kit": "eight-oh-eight", "punch-kit": "nine-oh-nine"} {
		if got := renderKitDigest(t, neutral); got != kitDigests[old] {
			t.Errorf("%s digest %s, want the %s digest %s", neutral, got, old, kitDigests[old])
		}
	}
}
