package gosxhost_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/gamescore"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/sdk/gosxhost"
)

// TestWriteBrowserFixture writes the inputs of GoSX's real-browser audio test
// (e2e/audio_cicada_browser_e2e_test.go in m31labs.dev/gosx): the score image,
// the backend descriptor, and the command batches a game sends to start,
// change scene, change intensity and play a stinger. Set CICADA_BROWSER_FIXTURE
// to an output directory; the test is skipped otherwise.
func TestWriteBrowserFixture(t *testing.T) {
	dir := os.Getenv("CICADA_BROWSER_FIXTURE")
	if dir == "" {
		t.Skip("set CICADA_BROWSER_FIXTURE to a directory")
	}
	score := filepath.Join("..", "..", "examples", "game-music.cicada")
	loaded, ds, err := project.LoadScore(score, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, more := project.FromScore(loaded)
	for _, d := range append(ds, more...) {
		if d.Severity == "error" {
			t.Fatal(d)
		}
	}
	pack, err := gamescore.Build(p, filepath.Dir(score), 48000)
	if err != nil {
		t.Fatal(err)
	}
	var batches = map[string]string{}
	var current string
	music, err := gosxhost.NewMusic(pack, func(b []byte) error {
		batches[current] += string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"start":     func() error { return music.Start("explore") },
		"combat":    func() error { return music.SetScene("combat") },
		"intensity": func() error { return music.SetIntensity(0.9) },
		"stinger":   func() error { return music.Stinger("pickup") },
	} {
		current = name
		if err := fn(); err != nil {
			t.Fatal(err)
		}
	}
	enc := map[string]string{}
	for k, v := range batches {
		enc[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}
	tempo, caps := pack.Info()
	out, _ := json.Marshal(map[string]any{
		"rate": pack.SampleRate, "image": base64.StdEncoding.EncodeToString(pack.Image),
		"tempoMilli": tempo, "requiredCapabilities": caps,
		"backend": json.RawMessage(gosxhost.BackendJSON()), "batches": enc,
		"states": pack.Surface.States,
	})
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
}
