//go:build !js || !wasm

package padsampler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"m31labs.dev/cicada/host/audition"
	"m31labs.dev/cicada/project"
)

type Sound struct {
	ID                string        `json:"id"`
	ProcessingVersion int           `json:"processingVersion,omitempty"`
	Pad               Pad           `json:"pad"`
	Effects           Effects       `json:"effects"`
	Source            Source        `json:"source"`
	Asset             project.Asset `json:"asset"`
}

// Increment when preparation DSP changes. Bank versioning and immutable
// performance sound references are independent of the current processor.
const soundProcessingVersion = 2

type Store struct {
	Root     string
	BankPath string
}

func NewStore(root, score string) *Store {
	id := sha256.Sum256([]byte(filepath.ToSlash(score)))
	return &Store{Root: root, BankPath: fmt.Sprintf("sampler/bank-%x.json", id[:8])}
}
func DefaultBank() Bank {
	b := Bank{Version: 1, Effects: Clean()}
	for i := range b.Pads {
		b.Pads[i] = Pad{Name: "Pad " + strconv.Itoa(i+1), Mode: "oneshot", ReleaseMS: 5, GainDB: -6}
	}
	return b
}
func Revision(b Bank) string    { data, _ := json.Marshal(b); return Digest(data) }
func Digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func validID(id string) bool {
	b, err := hex.DecodeString(id)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == id
}

func (s *Store) Read() (Bank, []byte, error) {
	b := DefaultBank()
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return b, nil, err
	}
	defer root.Close()
	f, err := root.Open(s.BankPath)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil, nil
	}
	if err != nil {
		return b, nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 2<<20+1))
	if err != nil {
		return b, nil, err
	}
	if len(data) > 2<<20 {
		return b, nil, fmt.Errorf("pad bank is too large")
	}
	if err = json.Unmarshal(data, &b); err != nil {
		return b, nil, err
	}
	if b.Version != 1 || len(b.Sources) > 128 {
		return b, nil, fmt.Errorf("unsupported pad bank version or source count")
	}
	if err = b.Effects.Validate(); err != nil {
		return b, nil, err
	}
	for _, p := range b.Pads {
		if p.Source != "" {
			src, ok := b.Source(p.Source)
			if !ok {
				return b, nil, fmt.Errorf("pad source is missing")
			}
			if err = p.Validate(src.Asset.Frames); err != nil {
				return b, nil, err
			}
		}
	}
	return b, data, nil
}
func BankBytes(b Bank) []byte { data, _ := json.MarshalIndent(b, "", "  "); return append(data, '\n') }

func (s *Store) Write(path string, data []byte, replace bool) error {
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if !replace {
		if f, e := root.Open(path); e == nil {
			defer f.Close()
			old, e := io.ReadAll(io.LimitReader(f, int64(len(data))+1))
			if e == nil && Digest(old) == Digest(data) {
				return nil
			}
			return fmt.Errorf("immutable sampler file already exists with different bytes")
		}
	}
	tmp, err := root.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			root.Remove(path + ".tmp")
		}
	}()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = root.Rename(path+".tmp", path); err != nil {
		return err
	}
	ok = true
	return nil
}

func (s *Store) Import(data []byte, name string) (Source, error) {
	a, err := Decode(data)
	if err != nil {
		return Source{}, err
	}
	h, err := WAV(a)
	if err != nil {
		return Source{}, err
	}
	id := Digest(h)
	src := Source{ID: id, Name: name, Peaks: Peaks(a), Asset: project.Asset{Name: "sample-" + id[:12], Path: "sampler/audio/" + id + ".wav", SHA256: id, Format: "wav", Frames: int64(len(a.Left)), RateHz: a.Rate, Channels: 2}}
	return src, s.Write(src.Asset.Path, h, false)
}
func (s *Store) Audio(asset project.Asset) (audition.Audio, error) {
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return audition.Audio{}, err
	}
	defer root.Close()
	f, err := root.Open(asset.Path)
	if err != nil {
		return audition.Audio{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<20)+45))
	if err != nil {
		return audition.Audio{}, err
	}
	if Digest(data) != asset.SHA256 {
		return audition.Audio{}, fmt.Errorf("sampler source checksum changed")
	}
	a, err := Decode(data)
	if err == nil && (a.Rate != asset.RateHz || int64(len(a.Left)) != asset.Frames) {
		err = fmt.Errorf("sampler source dimensions changed")
	}
	return a, err
}
func SoundID(p Pad, e Effects, src Source) string {
	return soundIDForVersion(p, e, src, soundProcessingVersion)
}

func soundIDForVersion(p Pad, e Effects, src Source, version int) string {
	if p.BypassEffects {
		e = Clean()
	}
	data, _ := json.Marshal(struct {
		Version int
		Pad     Pad
		Effects Effects
		Source  string
	}{version, p, e, src.ID})
	return Digest(data)
}
func (s *Store) Prepare(p Pad, e Effects, src Source) (Sound, error) {
	id := SoundID(p, e, src)
	if sound, err := s.Sound(id); err == nil {
		return sound, nil
	}
	a, err := s.Audio(src.Asset)
	if err != nil {
		return Sound{}, err
	}
	a, err = Prepare(a, p, e)
	if err != nil {
		return Sound{}, err
	}
	data, err := WAV(a)
	if err != nil {
		return Sound{}, err
	}
	sound := Sound{ID: id, ProcessingVersion: soundProcessingVersion, Pad: p, Effects: e, Source: src, Asset: project.Asset{Name: "pad-" + id[:12], Path: "sampler/sounds/" + id + ".wav", SHA256: Digest(data), Format: "wav", Frames: int64(len(a.Left)), RateHz: Rate, Channels: 2}}
	if err = s.Write(sound.Asset.Path, data, false); err != nil {
		return Sound{}, err
	}
	meta, _ := json.Marshal(sound)
	err = s.Write("sampler/sounds/"+id+".json", meta, false)
	return sound, err
}
func (s *Store) Sound(id string) (Sound, error) {
	var sound Sound
	if !validID(id) {
		return sound, fmt.Errorf("invalid prepared sound identifier")
	}
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return sound, err
	}
	defer root.Close()
	f, err := root.Open("sampler/sounds/" + id + ".json")
	if err != nil {
		return sound, err
	}
	defer f.Close()
	var stored struct {
		Sound
		ProcessingVersion json.RawMessage `json:"processingVersion"`
	}
	if err = json.NewDecoder(io.LimitReader(f, 32<<10)).Decode(&stored); err != nil {
		return sound, err
	}
	sound = stored.Sound
	version := 1 // Original metadata omitted the field and used hash version 1.
	if len(stored.ProcessingVersion) > 0 {
		var explicit int
		if err = json.Unmarshal(stored.ProcessingVersion, &explicit); err != nil {
			return sound, fmt.Errorf("invalid prepared sound processing version: %w", err)
		}
		version = explicit
	}
	if version != 1 && version != soundProcessingVersion {
		return sound, fmt.Errorf("unsupported prepared sound processing version %d", version)
	}
	sound.ProcessingVersion = version
	if sound.ID != id || soundIDForVersion(sound.Pad, sound.Effects, sound.Source, version) != id || sound.Asset.Path != "sampler/sounds/"+id+".wav" {
		err = fmt.Errorf("prepared sound metadata changed")
	}
	return sound, err
}

func Chop(b *Bank, id string, count int) error {
	src, ok := b.Source(id)
	if !ok || count < 1 || count > 16 || src.Asset.Frames < int64(count) {
		return fmt.Errorf("choose a source and 1 to 16 slices")
	}
	for i := 0; i < count; i++ {
		b.Pads[i] = Pad{Name: fmt.Sprintf("%s %02d", truncate(src.Name, 48), i+1), Source: id, Start: int64(i) * src.Asset.Frames / int64(count), End: int64(i+1) * src.Asset.Frames / int64(count), GainDB: -6, ReleaseMS: 5, Mode: "oneshot", Choke: 1}
	}
	return nil
}
func truncate(s string, n int) string {
	if len(s) > n {
		for !utf8.RuneStart(s[n]) {
			n--
		}
		return s[:n]
	}
	return s
}
