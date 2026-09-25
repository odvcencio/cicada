package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/ebitengine/oto/v3"
	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

const liveSampleRate = 48_000
const liveBlockFrames = 256

func playCommand(args []string) error {
	path, err := playScorePath(args)
	if err != nil {
		return err
	}
	initialHash, err := playSourceHash(path)
	if err != nil {
		return err
	}
	initial, err := compileLiveScore(path)
	if err != nil {
		return err
	}
	stream, err := liveplay.New(initial, liveSampleRate)
	if err != nil {
		return err
	}
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate: liveSampleRate, ChannelCount: 2, Format: oto.FormatFloat32LE,
		BufferSize: 20 * time.Millisecond, ApplicationName: "Cicada",
	})
	if err != nil {
		return err
	}
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		return fmt.Errorf("audio device did not become ready within 10 seconds")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	player := ctx.NewPlayer(stream)
	player.SetBufferSize(liveBlockFrames * 8 * 4)
	ctxSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	player.Play()
	fmt.Printf("playing %s at 48 kHz; edits land on the next bar (Ctrl-C to stop)\n", path)
	return watchLiveScore(ctxSignal, path, initialHash, stream, player, ctx, os.Stderr)
}

func playScorePath(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("usage: cicada play [score.cicada]")
	}
	path := "main.cicada"
	if len(args) == 1 {
		path = args[0]
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, "main.cicada")
	}
	if filepath.Ext(path) != ".cicada" {
		return "", fmt.Errorf("play needs a .cicada score")
	}
	return filepath.Abs(path)
}

func compileLiveScore(path string) (liveplay.Score, error) {
	p, err := loadProject(path)
	if err != nil {
		return liveplay.Score{}, err
	}
	cfg, err := project.CompileEngine(p, liveSampleRate, liveBlockFrames)
	if err != nil {
		return liveplay.Score{}, err
	}
	cfg.LoopSong = true
	created, err := engine.New(cfg)
	if err != nil {
		return liveplay.Score{}, err
	}
	sceneIDs := make([]string, len(p.Scenes))
	for i, scene := range p.Scenes {
		sceneIDs[i] = scene.ID
	}
	tracks := make([]liveplay.TrackSlots, len(p.Tracks))
	for i, track := range p.Tracks {
		tracks[i].ID = track.ID
		for slot, pattern := range track.Slots {
			if pattern != nil {
				tracks[i].Slots[slot] = *pattern
			}
		}
	}
	song := make([]liveplay.SongEntry, len(p.Song))
	startBar := uint32(1)
	for i, entry := range p.Song {
		song[i] = liveplay.SongEntry{Scene: entry.Scene, StartBar: startBar}
		startBar += uint32(entry.Bars)
	}
	return liveplay.Score{Engine: created, SampleRate: liveSampleRate, BPMMilli: int64(p.TempoMilli), Name: path, SceneIDs: sceneIDs, Tracks: tracks, Song: song}, nil
}

type liveScoreWatcher struct {
	path      string
	last      [32]byte
	lastError string
	stream    *liveplay.Player
	output    io.Writer
}

func (w *liveScoreWatcher) poll() {
	fingerprint, err := playSourceHash(w.path)
	if err != nil {
		if err.Error() != w.lastError {
			fmt.Fprintf(w.output, "%s; continuing previous score\n", err)
			w.lastError = err.Error()
		}
		return
	}
	if fingerprint == w.last {
		return
	}
	w.last = fingerprint
	next, err := compileLiveScore(w.path)
	if err == nil {
		err = w.stream.Offer(next)
	}
	if err != nil {
		fmt.Fprintf(w.output, "%s; continuing previous score\n", err)
	} else {
		fmt.Fprintln(w.output, "edit validated; queued for next bar")
	}
	w.lastError = ""
}

func watchLiveScore(ctx context.Context, path string, initialHash [32]byte, stream *liveplay.Player, player *oto.Player, device *oto.Context, errorsTo io.Writer) error {
	watcher := liveScoreWatcher{path: path, last: initialHash, stream: stream, output: errorsTo}
	ticker := time.NewTicker(100 * time.Millisecond)
	health := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			player.PauseAndStopReading()
			return nil
		case event := <-stream.Events():
			fmt.Fprintf(errorsTo, "landed %s at bar %d\n", event.Name, event.Bar)
		case <-ticker.C:
			watcher.poll()
		case <-health.C:
			if err := device.Err(); err != nil {
				return err
			}
			if err := player.Err(); err != nil {
				return err
			}
		}
	}
}

func playSourceHash(path string) ([32]byte, error) {
	var empty [32]byte
	source, err := os.ReadFile(path)
	if err != nil {
		return empty, err
	}
	hash := sha256.New()
	_, _ = hash.Write(source)
	dir := filepath.Dir(path)
	for {
		manifest := filepath.Join(dir, "cicada.mod")
		data, err := os.ReadFile(manifest)
		if err == nil {
			_, _ = hash.Write([]byte(manifest))
			_, _ = hash.Write(data)
			break
		}
		if !os.IsNotExist(err) {
			return empty, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
