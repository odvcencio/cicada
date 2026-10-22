// Generates an isolated portable-song fixture with synthetic PCM; no audio devices are opened.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/projectcopy"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func write(root, name string, data []byte) {
	path := filepath.Join(root, name)
	must(os.MkdirAll(filepath.Dir(path), 0700))
	must(os.WriteFile(path, data, 0600))
}

func main() {
	base, err := os.MkdirTemp("", "cicada-portable-song-")
	must(err)
	fmt.Fprintln(os.Stderr, "fixture:", base)
	root, user := filepath.Join(base, "original"), filepath.Join(base, "user-libraries")
	must(os.Setenv("CICADA_LIBRARY", user))
	wave := testwav.Bytes(48000, 1, 16, 24000, 1)
	write(user, "demo/tone/audio/chime.wav", wave)
	write(user, "demo/tone/cicada.mod", []byte("library demo/tone\ncicada 2\nsource \"main.cicada\"\nlicense \"MIT\"\nauthor \"Cicada fixtures\"\n"))
	write(user, "demo/tone/main.cicada", []byte(fmt.Sprintf(`import "demo/phrases"
asset chime "audio/chime.wav" { sha256 = "%x" format = wav frames = 24000 rate = 48000Hz channels = 1 }
sampler hit { asset = chime root = d3 mode = oneshot voices = 8 }
instrument glass { voice mono { out = sine(pitch) * env(gate,90ms) * velocity } }
pattern melody { use phrases.hook *2 }
`, sha256.Sum256(wave))))
	write(user, "demo/phrases/cicada.mod", []byte("library demo/phrases\ncicada 2\nsource \"main.cicada\"\nlicense \"MIT\"\nauthor \"Cicada fixtures\"\n"))
	write(user, "demo/phrases/main.cicada", []byte("phrase hook { 1 . 3 . 5 . 3 . }\n"))
	write(root, "cicada.mod", []byte("project carry_song\ncicada 2\nentry \"scores/main.cicada\"\nsource \"parts/rhythm.cicada\"\n"))
	write(root, "parts/rhythm.cicada", []byte("pattern bassline notes steps=16 { d2 . . . a2 . . . f2 . . . c3 . . . }\npattern taps notes steps=16 { d3 . . . . . a3 . . . d3 . . . . . }\n"))
	source := []byte(`cicada 2
title "Carry the song"
tempo 108
seed 42
key d minor
import "demo/tone"
track guitar audio { level = -12dB }
track bass acid { level = -18dB }
track keys tone.glass { level = -18dB }
track hits tone.hit { level = -18dB }
scene verse { guitar = off bass = bassline keys = tone.melody hits = taps }
scene break { guitar = off bass = bassline keys = off hits = taps }
song { verse*2 break verse }
`)
	path := filepath.Join(root, "scores/main.cicada")
	write(root, "scores/main.cicada", source)
	sources, err := project.ReadSources(path, nil)
	must(err)
	pins, _, err := sources.UpdateLibraries("")
	must(err)
	write(root, "cicada.sum", pins)
	store, err := takejournal.Open(path)
	must(err)
	var ids []string
	for pass := 0; pass < 2; pass++ {
		id, err := store.Begin("guitar", "verse", takejournal.Revision(source), 48000, 1)
		must(err)
		frames := 24000
		pcm := make([]float32, frames)
		for i := range pcm {
			t := float64(i) / 48000
			pcm[i] = float32(.35 * math.Sin(2*math.Pi*float64(220+pass*110)*t) * math.Exp(-t*4))
		}
		must(store.Write(id, capture.RecordedBlock{Timing: capture.Block{SampleRate: 48000, Frames: frames}}, [][]float32{pcm}))
		must(store.Flush(id))
		must(store.Finalize(id, false))
		must(store.Publish(id))
		ids = append(ids, id)
		if pass == 1 {
			take, _ := store.Get(id)
			after, err := takejournal.SelectProjectSource(path, source, take)
			must(err)
			must(store.Prepare(id, source, after))
			write(root, "scores/main.cicada", after)
			must(store.Mark(id, takejournal.Source))
			must(store.Mark(id, takejournal.Committed))
		}
	}
	must(store.Close())
	renderSong := func(scorePath, output string) []byte {
		score, ds, err := project.LoadScore(scorePath, nil)
		must(err)
		for _, d := range ds {
			if d.Severity == "error" {
				panic(d.Error())
			}
		}
		set, err := project.ReadSources(scorePath, nil)
		must(err)
		var wav bytes.Buffer
		_, err = render.WAV(score, render.Options{AssetRoot: set.Root, AssetDir: set.Root, SampleRate: 48000, Bits: 24, TailSec: 3}, &wav)
		must(err)
		write(base, output, wav.Bytes())
		return wav.Bytes()
	}
	before := renderSong(path, "before.wav")
	target := filepath.Join(base, "portable", "Carry the song.cicada")
	must(os.MkdirAll(filepath.Dir(target), 0700))
	must(projectcopy.SaveAs(path, target))
	must(os.RemoveAll(root))
	must(os.RemoveAll(user))
	after := renderSong(target, "after.wav")
	if !bytes.Equal(before, after) {
		panic("project copy changed WAV")
	}
	copyStore, err := takejournal.Open(target)
	must(err)
	must(copyStore.Recover())
	if len(copyStore.Takes()) != 2 {
		panic("take history lost")
	}
	must(copyStore.Close())
	data, _ := json.MarshalIndent(map[string]any{"fixture": base, "score": target, "takeIDs": ids, "originalRemoved": true, "userLibrariesRemoved": true, "wavSHA256": fmt.Sprintf("%x", sha256.Sum256(after)), "wavBytes": len(after), "userLibraries": user}, "", "  ")
	fmt.Println(string(data))
}
