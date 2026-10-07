package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/internal/audiobackend"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/project"
)

const liveSampleRate = 48_000
const liveBlockFrames = 256

func playCommand(args []string) error {
	args, backendName, backend, err := selectCommandAudio("play", args)
	if err != nil {
		return err
	}
	restoreThreads := raiseAudioProcessThreads(backendName)
	defer restoreThreads()
	path, err := playScorePath(args)
	if err != nil {
		return err
	}
	initialHash, err := playSourceHash(path)
	if err != nil {
		return err
	}
	sampleRate, err := backend.SampleRate(audiobackend.Config{Channels: 2, FramesPerPeriod: liveBlockFrames})
	if err != nil {
		return err
	}
	initial, err := compileLiveScoreAtRate(path, sampleRate)
	if err != nil {
		return err
	}
	stream, err := liveplay.New(initial, sampleRate)
	if err != nil {
		return err
	}
	defer stream.Close()
	var pcm []byte
	audio, err := backend.Open(audiobackend.Config{
		SampleRate: sampleRate, Channels: 2, FramesPerPeriod: liveBlockFrames,
	}, func(_ [][]float32, output [][]float32) error {
		return renderLivePlayPeriod(stream, pcm, output)
	})
	if err != nil {
		return err
	}
	defer audio.Close()
	format := audio.Format()
	pcm = make([]byte, format.FramesPerPeriod*8)
	ctxSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := audio.Start(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, playAudioBanner(format))
	fmt.Printf("playing %s at %d Hz; edits land on the next bar (Ctrl-C to stop)\n", path, format.SampleRate)
	err = watchLiveScore(ctxSignal, path, initialHash, stream, audio, os.Stderr)
	if format.Backend == audiobackend.Tymbal {
		fmt.Fprintln(os.Stderr, playAudioRunSummary(audio.Format(), audio.Stats()))
	}
	return err
}

func playAudioBanner(format audiobackend.Format) string {
	host := format.Host
	if host == "" {
		host = string(format.Backend)
	}
	return fmt.Sprintf("audio: %s (%s, %d Hz, %d frames)", format.Backend, host, format.SampleRate, format.FramesPerPeriod)
}

func playAudioRunSummary(format audiobackend.Format, stats audiobackend.Stats) string {
	return fmt.Sprintf("audio: callbacks=%d glitches=%d period=%d frames", stats.Callbacks, stats.Dropouts+stats.Late, format.FramesPerPeriod)
}

func renderLivePlayPeriod(source io.Reader, pcm []byte, output [][]float32) error {
	if len(output) == 0 {
		return nil
	}
	frames := len(output[0])
	need := frames * 8
	if need > len(pcm) {
		return io.ErrShortBuffer
	}
	if _, err := io.ReadFull(source, pcm[:need]); err != nil {
		clearStudioAudioOutput(output)
		return err
	}
	if len(output) == 1 {
		for frame := 0; frame < frames; frame++ {
			left := math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8 : frame*8+4]))
			right := math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8+4 : frame*8+8]))
			output[0][frame] = (left + right) * 0.5
		}
		return nil
	}
	for frame := 0; frame < frames; frame++ {
		output[0][frame] = math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8 : frame*8+4]))
		output[1][frame] = math.Float32frombits(binary.LittleEndian.Uint32(pcm[frame*8+4 : frame*8+8]))
	}
	for channel := 2; channel < len(output); channel++ {
		clear(output[channel])
	}
	return nil
}

func playScorePath(args []string) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("usage: cicada play [score.cicada] [--audio tymbal|oto|null]")
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
	return compileLiveScoreAtRate(path, liveSampleRate)
}

func compileLiveScoreAtRate(path string, sampleRate int) (liveplay.Score, error) {
	p, err := loadProject(path)
	if err != nil {
		return liveplay.Score{}, err
	}
	return compileLiveProjectAtRate(path, p, sampleRate)
}

func compileLiveProject(path string, p *project.Project) (liveplay.Score, error) {
	return compileLiveProjectAtRate(path, p, liveSampleRate)
}

func compileLiveProjectAtRate(path string, p *project.Project, sampleRate int) (liveplay.Score, error) {
	if sampleRate <= 0 {
		return liveplay.Score{}, fmt.Errorf("audio sample rate must be positive")
	}
	root, err := studioProjectRoot(path)
	if err != nil {
		return liveplay.Score{}, err
	}
	cfg, err := schedule.Compile(p, root, sampleRate, liveBlockFrames)
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
		tracks[i].Kind = track.Kind
		tracks[i].Pitched = cfg.Track[i].Kind == engine.VoiceGraph
		for _, kit := range p.Kits {
			if track.Kind == kit.ID {
				tracks[i].Kind = "drums"
				break
			}
		}
		for _, voice := range p.Instruments {
			if track.Kind == voice.ID {
				tracks[i].Kind = "graph"
				if voice.Mode == "poly" {
					tracks[i].Kind = "poly"
				}
				break
			}
		}
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
	return liveplay.Score{
		Engine: created, SampleRate: sampleRate, BPMMilli: int64(p.TempoMilli), Name: path,
		SceneIDs: sceneIDs, Tracks: tracks, Song: song, Parameters: liveProjectParameters(p),
		HasReturnA: cfg.DelayA != nil, HasReturnB: cfg.ReverbB != nil, HasSFX: hasSFXTracks(p),
	}, nil
}

func hasSFXTracks(p *project.Project) bool {
	for _, track := range p.Tracks {
		if track.Mixer.Bus == "sfx" {
			return true
		}
	}
	return false
}

func liveProjectParameters(p *project.Project) []liveplay.ParameterValue {
	addresses := project.ParamAddresses(p)
	values := make([]liveplay.ParameterValue, 0, len(addresses))
	for _, address := range addresses {
		descriptor, ok := project.LookupParamDescriptor(address.Param)
		if !ok || !descriptor.Live {
			continue
		}
		track := uint8(0xff)
		if address.Track != "" {
			found := false
			for i, candidate := range p.Tracks {
				if candidate.ID == address.Track {
					track, found = uint8(i), true
					break
				}
			}
			if !found {
				continue
			}
		}
		value := float32(0)
		switch number := address.Value.(type) {
		case float64:
			value = float32(number)
		case float32:
			value = number
		case int:
			value = float32(number)
		case nil:
			if !descriptor.Off {
				continue
			}
			value = float32(math.Inf(-1))
		default:
			continue
		}
		id, ok := kernel.FindParam(address.Param)
		if !ok {
			continue
		}
		values = append(values, liveplay.ParameterValue{Track: track, ID: id, Value: value})
	}
	return values
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

func watchLiveScore(ctx context.Context, path string, initialHash [32]byte, stream *liveplay.Player, audio audiobackend.Stream, errorsTo io.Writer) error {
	watcher := liveScoreWatcher{path: path, last: initialHash, stream: stream, output: errorsTo}
	ticker := time.NewTicker(100 * time.Millisecond)
	health := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			audio.Pause()
			return nil
		case event := <-stream.Events():
			fmt.Fprintf(errorsTo, "landed %s at bar %d\n", event.Name, event.Bar)
		case <-ticker.C:
			watcher.poll()
		case <-health.C:
			if err := audio.Err(); err != nil {
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
	return playSourceHashBytes(path, source)
}

func playSourceHashBytes(path string, source []byte) ([32]byte, error) {
	var empty [32]byte
	hash := sha256.New()
	_, _ = hash.Write(source)
	sources, err := project.ReadSources(path, nil)
	if err != nil {
		return empty, err
	}
	if sources.Manifest.ExplicitSources() {
		for _, file := range sources.Files {
			_, _ = hash.Write([]byte(file.Path))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write(file.Source)
		}
	}
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
