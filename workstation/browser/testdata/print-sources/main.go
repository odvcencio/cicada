// Generates a disposable self-contained project using synthetic PCM only.
package main

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/padsampler"
	"m31labs.dev/cicada/host/takejournal"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	primary := flag.Bool("primary-arrangement", false, "keep arrangement in entry for compatibility checks")
	seedBank := flag.Bool("sampler-bank", false, "seed one stored performance for native UI checks")
	sceneSong := flag.Bool("scene-song", false, "keep scenes and the song in separate project sources")
	flag.Parse()
	if *sceneSong && *primary {
		panic("scene-song and primary-arrangement are separate fixture modes")
	}
	root, err := os.MkdirTemp("/tmp", "cicada-print-sources-")
	must(err)
	entry := []byte(`cicada 2
// cicada-print-sources-test: disposable fixture
// cicada-sampler-test: disposable fixture
// cicada-vocal-test: disposable fixture
title "Printed sources"
tempo 120
seed 42
instrument tone { voice mono { out = sine(pitch) * env(gate,400ms) * velocity } }
track lead tone {
  level = 0dB
}
track vox audio {
  level = -12dB
}
pattern melody { c4 . c4 . c4 . c4 . c4 . c4 . c4 . c4 . }
`)
	arrangement := []byte("// Keep section notes.\r\narrange {\r\n  place lead lead melody { at = @1.1.1 length = 8bars }\r\n  marker middle { at = @5.1.1 }\r\n}\r\n")
	score, owner := filepath.Join(root, "main.cicada"), filepath.Join(root, "arrangement.cicada")
	manifest := "project print-sources\ncicada 2\nentry \"main.cicada\"\n"
	scenesPath := ""
	if *sceneSong {
		owner = filepath.Join(root, "song.cicada")
		scenesPath = filepath.Join(root, "scenes.cicada")
		must(os.WriteFile(scenesPath, []byte("scene opening { lead=melody vox=off }\nscene held { lead=keep }\nscene quiet { lead=off }\n"), 0600))
		must(os.WriteFile(owner, []byte("// Keep song notes.\r\nsong { opening*2 // Preserve this song note.\r\nheld*2 opening*2 quiet opening } // Keep this ending.\r\n"), 0600))
		manifest += "source \"scenes.cicada\"\nsource \"song.cicada\"\n"
	} else if *primary {
		entry = append(entry, arrangement...)
		owner = score
	} else {
		must(os.WriteFile(owner, arrangement, 0600))
		manifest += "source \"arrangement.cicada\"\n"
	}
	must(os.WriteFile(filepath.Join(root, "cicada.mod"), []byte(manifest), 0600))
	must(os.WriteFile(score, entry, 0600))
	store, err := takejournal.Open(score)
	must(err)
	var ids []string
	for pass := 0; pass < 3; pass++ {
		start, end, frames := int64(0), int64(30720), 768000
		if pass == 2 {
			start, end, frames = 7680, 15360, 192000
		}
		id, err := store.BeginTimeline("vox", "session", takejournal.Revision(entry), 48000, 1, &takejournal.Timeline{StartTick: start, EndTick: end, PlaybackStartTick: start, TempoMilli: 120000})
		must(err)
		pcm := make([]float32, frames)
		for i := range pcm {
			pcm[i] = float32(.25 * math.Sin(float64(i)*2*math.Pi*float64(220+pass*110)/48000))
		}
		must(store.Write(id, capture.RecordedBlock{Timing: capture.Block{SampleRate: 48000, Frames: frames}}, [][]float32{pcm}))
		must(store.Flush(id))
		must(store.Finalize(id, false))
		must(store.Publish(id))
		ids = append(ids, id)
	}
	must(store.Close())
	if *seedBank {
		bankStore := padsampler.NewStore(root, "main.cicada")
		audio := audition.Audio{Rate: 48000, Left: make([]float32, 48000), Right: make([]float32, 48000)}
		for i := range audio.Left {
			audio.Left[i] = float32(.3 * math.Sin(float64(i)*2*math.Pi*220/48000))
			audio.Right[i] = audio.Left[i]
		}
		wav, err := padsampler.WAV(audio)
		must(err)
		src, err := bankStore.Import(wav, "Seeded sine")
		must(err)
		bank := padsampler.DefaultBank()
		bank.Sources = []padsampler.Source{src}
		must(padsampler.Chop(&bank, src.ID, 16))
		prepared, err := bankStore.Prepare(bank.Pads[0], bank.Effects, src)
		must(err)
		bank.Performance = padsampler.Performance{Frames: 96000, Hits: []padsampler.Hit{{Sound: prepared.ID, Pad: 1, Velocity: 1, Choke: prepared.Pad.Choke}}}
		must(bankStore.Write(bankStore.BankPath, padsampler.BankBytes(bank), true))
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"score": score, "arrangement": owner, "scenes": scenesPath, "sceneSong": *sceneSong, "primaryArrangement": *primary, "takeIDs": ids}))
}
