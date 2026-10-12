package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/host/padsampler"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/project"
)

type samplerCommand struct {
	Action       string                 `json:"action"`
	Revision     string                 `json:"revision"`
	BankRevision string                 `json:"bankRevision"`
	Source       string                 `json:"source"`
	Placement    string                 `json:"placement"`
	Pad          int                    `json:"pad"`
	Count        int                    `json:"count"`
	Settings     *padsampler.Pad        `json:"settings"`
	Effects      *padsampler.Effects    `json:"effects"`
	Performance  padsampler.Performance `json:"performance"`
	NewName      string                 `json:"newName"`
	Track        string                 `json:"track"`
	At           string                 `json:"at"`
	Destination  int                    `json:"destination"`
	Place        bool                   `json:"place"`
}

func (s *studio) padStore() (*padsampler.Store, error) {
	root, err := studioProjectRoot(s.path)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, s.path)
	if err != nil {
		return nil, err
	}
	return padsampler.NewStore(root, relative), nil
}
func (s *studio) samplerState(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.padStore()
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	b, _, err := store.Read()
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	source, err := os.ReadFile(s.path)
	if err != nil {
		studioJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	ids := make([]string, 16)
	for i, p := range b.Pads {
		if src, ok := b.Source(p.Source); ok {
			ids[i] = padsampler.SoundID(p, b.Effects, src)
		}
	}
	tempo := 120000
	if s.lastGoodProject != nil {
		tempo = s.lastGoodProject.TempoMilli
	}
	studioJSON(w, 200, map[string]any{"revision": studioRevision(source), "bankRevision": padsampler.Revision(b), "bank": b, "sounds": ids, "canUndo": len(s.samplerUndo) > 0, "tempo": tempo})
}

func (s *studio) samplerSound(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.padStore()
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	id := r.PathValue("id")
	sound, err := store.Sound(id)
	if err != nil {
		b, _, e := store.Read()
		if e == nil {
			for _, p := range b.Pads {
				src, ok := b.Source(p.Source)
				if ok && padsampler.SoundID(p, b.Effects, src) == id {
					sound, err = store.Prepare(p, b.Effects, src)
					break
				}
			}
		}
	}
	if err != nil {
		http.Error(w, "prepared pad sound is unavailable: "+err.Error(), 422)
		return
	}
	a, err := store.Audio(sound.Asset)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	data, err := padsampler.WAV(a)
	if err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *studio) samplerImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 65<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		studioJSON(w, 400, map[string]any{"error": "choose a WAV no larger than 64 MiB"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	s.mu.Lock()
	defer s.mu.Unlock()
	store, err := s.padStore()
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	b, _, err := store.Read()
	source, e := os.ReadFile(s.path)
	if err != nil || e != nil {
		studioJSON(w, 422, map[string]any{"error": "could not read the score or pad bank"})
		return
	}
	if studioRevision(source) != r.FormValue("revision") || padsampler.Revision(b) != r.FormValue("bankRevision") {
		studioJSON(w, 409, map[string]any{"error": "score or pad bank changed; refresh before importing"})
		return
	}
	f, h, err := r.FormFile("audio")
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": "choose a WAV file"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<20+1))
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	src, err := store.Import(data, filepath.Base(h.Filename))
	if err == nil {
		s.addPadSource(&b, src)
		err = padsampler.Chop(&b, src.ID, 16)
	}
	if err == nil {
		err = s.savePadBank(store, b)
	}
	if err != nil {
		studioJSON(w, 422, map[string]any{"error": err.Error()})
		return
	}
	studioJSON(w, 200, map[string]any{"revision": studioRevision(source), "bankRevision": padsampler.Revision(b)})
}
func (s *studio) addPadSource(b *padsampler.Bank, src padsampler.Source) {
	if _, ok := b.Source(src.ID); !ok {
		b.Sources = append(b.Sources, src)
	}
}
func (s *studio) savePadBank(store *padsampler.Store, b padsampler.Bank) error {
	old, _, err := store.Read()
	if err != nil {
		return err
	}
	if len(b.Sources) > 128 {
		return fmt.Errorf("pad bank has reached its 128-source limit")
	}
	if err = store.Write(store.BankPath, padsampler.BankBytes(b), true); err != nil {
		return err
	}
	s.samplerUndo = append(s.samplerUndo, old)
	if len(s.samplerUndo) > 32 {
		s.samplerUndo = s.samplerUndo[len(s.samplerUndo)-32:]
	}
	return nil
}

func (s *studio) samplerCommand(w http.ResponseWriter, r *http.Request) {
	var input samplerCommand
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		studioJSON(w, 400, map[string]any{"error": "invalid sampler command"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fail := func(err error) { studioJSON(w, 422, map[string]any{"error": err.Error()}) }
	store, err := s.padStore()
	if err != nil {
		fail(err)
		return
	}
	b, before, err := store.Read()
	if err != nil {
		fail(err)
		return
	}
	source, err := os.ReadFile(s.path)
	if err != nil {
		fail(err)
		return
	}
	if studioRevision(source) != input.Revision || padsampler.Revision(b) != input.BankRevision {
		studioJSON(w, 409, map[string]any{"error": "score or pad bank changed; refresh before editing the sampler"})
		return
	}
	p, err := compileStudioSource(s.path, source)
	if err != nil {
		fail(err)
		return
	}
	switch input.Action {
	case "pad":
		if input.Pad < 1 || input.Pad > 16 || input.Settings == nil {
			fail(fmt.Errorf("choose a pad from 1 to 16"))
			return
		}
		src, ok := b.Source(input.Settings.Source)
		if !ok {
			fail(fmt.Errorf("choose an imported sample"))
			return
		}
		if err = input.Settings.Validate(src.Asset.Frames); err != nil {
			fail(err)
			return
		}
		b.Pads[input.Pad-1] = *input.Settings
	case "effects":
		if input.Effects == nil {
			fail(fmt.Errorf("effect settings are required"))
			return
		}
		if err = input.Effects.Validate(); err != nil {
			fail(err)
			return
		}
		b.Effects = *input.Effects
	case "chop":
		if err = padsampler.Chop(&b, input.Source, input.Count); err != nil {
			fail(err)
			return
		}
	case "sample-placement":
		a, err := samplePlacement(p, store.Root, input.Placement)
		if err != nil {
			fail(err)
			return
		}
		data, err := padsampler.WAV(a)
		if err != nil {
			fail(err)
			return
		}
		src, err := store.Import(data, input.Placement)
		if err != nil {
			fail(err)
			return
		}
		s.addPadSource(&b, src)
		if err = padsampler.Chop(&b, src.ID, input.Count); err != nil {
			fail(err)
			return
		}
	case "asset":
		var asset *project.Asset
		for i := range p.Assets {
			if p.Assets[i].Name == input.Source {
				asset = &p.Assets[i]
			}
		}
		if asset == nil {
			fail(fmt.Errorf("choose an existing score asset"))
			return
		}
		root, err := os.OpenRoot(asset.Directory(store.Root))
		if err != nil {
			fail(err)
			return
		}
		f, err := root.Open(asset.Path)
		if err != nil {
			root.Close()
			fail(err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(f, 64<<20+1))
		f.Close()
		root.Close()
		if err != nil || padsampler.Digest(data) != asset.SHA256 {
			fail(fmt.Errorf("source asset checksum changed"))
			return
		}
		src, err := store.Import(data, asset.Name)
		if err != nil {
			fail(err)
			return
		}
		s.addPadSource(&b, src)
		if err = padsampler.Chop(&b, src.ID, input.Count); err != nil {
			fail(err)
			return
		}
	case "undo":
		if len(s.samplerUndo) == 0 {
			fail(fmt.Errorf("no pad edit to undo"))
			return
		}
		b = s.samplerUndo[len(s.samplerUndo)-1]
		if err = store.Write(store.BankPath, padsampler.BankBytes(b), true); err != nil {
			fail(err)
			return
		}
		s.samplerUndo = s.samplerUndo[:len(s.samplerUndo)-1]
		studioJSON(w, 200, map[string]any{"revision": input.Revision, "bankRevision": padsampler.Revision(b)})
		return
	case "resample":
		if input.Performance.Frames < 240 || input.Performance.Frames > padsampler.MaxFrames || len(input.Performance.Hits) < 1 || len(input.Performance.Hits) > 512 {
			fail(fmt.Errorf("record 1 to 512 hits within the output frame limit"))
			return
		}
		sounds := map[string]padsampler.Voice{}
		var loaded int64
		for _, hit := range input.Performance.Hits {
			if _, ok := sounds[hit.Sound]; ok {
				continue
			}
			sound, err := store.Sound(hit.Sound)
			if err != nil {
				fail(fmt.Errorf("audition each pad before resampling: %w", err))
				return
			}
			loaded += sound.Asset.Frames
			if loaded > padsampler.MaxFrames {
				fail(fmt.Errorf("performance sounds exceed the 64 MiB limit"))
				return
			}
			a, err := store.Audio(sound.Asset)
			if err != nil {
				fail(err)
				return
			}
			sounds[hit.Sound] = padsampler.Voice{Audio: a, Pad: sound.Pad}
		}
		a, err := padsampler.Print(input.Performance, sounds)
		if err != nil {
			fail(err)
			return
		}
		if !studioPatternName.MatchString(input.NewName) {
			fail(fmt.Errorf("choose a lowercase name for the resampled clip"))
			return
		}
		data, err := padsampler.WAV(a)
		if err != nil {
			fail(err)
			return
		}
		src, err := store.Import(data, input.NewName)
		if err != nil {
			fail(err)
			return
		}
		s.addPadSource(&b, src)
		b.Performance = input.Performance
		if input.Destination < 0 || input.Destination > 16 {
			fail(fmt.Errorf("destination pad must be 0 to 16"))
			return
		}
		if input.Destination > 0 {
			b.Pads[input.Destination-1] = padsampler.Pad{Name: input.NewName, Source: src.ID, End: src.Asset.Frames, Mode: "oneshot", ReleaseMS: 5, BypassEffects: true}
		}
		if input.Place {
			updated, err := placeSamplerPrint(source, p, src, input)
			if err != nil {
				fail(err)
				return
			}
			mutation := studioMutation{Source: updated, HistoryDetail: "Resample pads into Session", Files: []studioAuxiliaryFile{{Path: filepath.Join(store.Root, store.BankPath), Before: before, After: padsampler.BankBytes(b), Mode: 0600}}, Response: map[string]any{"placement": input.NewName, "bankRevision": padsampler.Revision(b)}}
			outcome := s.commitMutationLocked(studioEdit{Revision: input.Revision, Label: "Resample pads into Session"}, source, mutation, nil, studioHistoryWriteNew, 0, commitHook{})
			studioJSON(w, outcome.Status, outcome.Response)
			return
		}
	default:
		fail(fmt.Errorf("unknown sampler action"))
		return
	}
	if err = s.savePadBank(store, b); err != nil {
		fail(err)
		return
	}
	studioJSON(w, 200, map[string]any{"revision": input.Revision, "bankRevision": padsampler.Revision(b)})
}

func samplePlacement(p *project.Project, root, id string) (audition.Audio, error) {
	if p.Arrange == nil {
		return audition.Audio{}, fmt.Errorf("sample a named Session placement")
	}
	var selected *project.Placement
	for i := range p.Arrange.Placements {
		if p.Arrange.Placements[i].ID == id {
			v := p.Arrange.Placements[i]
			selected = &v
			break
		}
	}
	if selected == nil {
		return audition.Audio{}, fmt.Errorf("choose an existing placement")
	}
	copy, arrange := *p, *p.Arrange
	selected.AtTick = 0
	arrange.Placements = []project.Placement{*selected}
	arrange.Markers = []project.Marker{}
	copy.Arrange = &arrange
	clock, err := seq.NewClock(padsampler.Rate, int64(p.TempoMilli))
	if err != nil {
		return audition.Audio{}, err
	}
	frames := clock.SampleAtTick(selected.LengthTicks)
	if frames < 1 || frames > padsampler.MaxFrames {
		return audition.Audio{}, fmt.Errorf("placement exceeds the sample frame limit")
	}
	cfg, err := schedule.Compile(&copy, root, padsampler.Rate, 128)
	if err != nil {
		return audition.Audio{}, err
	}
	audio, err := engine.New(cfg)
	if err != nil {
		return audition.Audio{}, err
	}
	defer audio.Close()
	if !audio.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff}) {
		return audition.Audio{}, fmt.Errorf("could not start placement sampling")
	}
	out := audition.Audio{Rate: padsampler.Rate, Left: make([]float32, frames), Right: make([]float32, frames)}
	for at := 0; at < len(out.Left); {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		frames, err := audio.PrepareRender(ctx, min(128, len(out.Left)-at))
		cancel()
		if err != nil {
			return audition.Audio{}, err
		}
		end := at + frames
		audio.Render(out.Left[at:end], out.Right[at:end])
		if stats := audio.StreamStats(); stats.MissingFrames > 0 {
			return audition.Audio{}, fmt.Errorf("placement sampling missed %d streamed frames", stats.MissingFrames)
		}
		at = end
	}
	// Bring an isolated part to a useful pad level before the pad gain control.
	peak := float32(0)
	for i := range out.Left {
		peak = max(peak, float32(math.Abs(float64(out.Left[i]))), float32(math.Abs(float64(out.Right[i]))))
	}
	if peak > 0 {
		gain := float32(.8) / peak
		for i := range out.Left {
			out.Left[i] *= gain
			out.Right[i] *= gain
		}
	}
	return out, nil
}

func placeSamplerPrint(source []byte, p *project.Project, src padsampler.Source, input samplerCommand) ([]byte, error) {
	if p.Edition != 2 || p.Arrange == nil {
		return nil, fmt.Errorf("placing resamples requires an edition-2 score with a Session arrangement")
	}
	if !studioPatternName.MatchString(input.Track) {
		return nil, fmt.Errorf("choose a lowercase audio track name")
	}
	found := false
	for _, t := range p.Tracks {
		if t.ID == input.Track {
			if t.Kind != "audio" {
				return nil, fmt.Errorf("choose an audio track for the resample")
			}
			found = true
		}
	}
	var err error
	if !found {
		source, err = newAudioTrackSource(source, input.Track, 2)
		if err != nil {
			return nil, err
		}
	}
	assetName, clipName := input.NewName+"-audio", input.NewName+"-clip"
	a := src.Asset
	declarations := fmt.Sprintf("\nasset %s %s { sha256 = %s format = wav frames = %d rate = %dHz channels = 2 }\nclip %s %s { start = 0frames end = %dframes fade_in = 240frames fade_out = 240frames }\n", assetName, strconv.Quote(a.Path), strconv.Quote(a.SHA256), a.Frames, a.RateHz, clipName, assetName, a.Frames)
	// Add declarations without rewriting the musician's existing score.
	source = append(append([]byte(nil), source...), []byte(declarations)...)
	length := int64(math.Ceil(float64(a.Frames) * float64(p.TempoMilli) * float64(seq.PPQ) / (float64(padsampler.Rate) * 60000)))
	return editedArrangementSource(source, studioEdit{Action: "append", NewName: input.NewName, Placement: &studioPlaceSettings{At: input.At, Length: fmt.Sprintf("%dticks", length), Track: input.Track, Content: clipName}})
}
