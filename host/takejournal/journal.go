// Package takejournal persists capture PCM before publishing score references.
package takejournal

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/edition"
	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type Stage string

const (
	WAVWritten    Stage = "wav-written"
	BlobLinked    Stage = "blob-linked"
	TakeLinked    Stage = "take-linked"
	SourceStaged  Stage = "source-staged"
	SourceSwapped Stage = "source-swapped"
	Begun         Stage = "begun"
	Chunk         Stage = "chunk"
	Finalized     Stage = "finalized"
	Blob          Stage = "blob"
	Published     Stage = "published"
	Prepared      Stage = "prepared"
	Source        Stage = "source"
	Committed     Stage = "committed"
	Conflict      Stage = "conflict"
)

// Take keeps all passes, including pending/conflicted takes. Each chunk event
// retains device timing; FirstBlock supplies placement without loading the timeline.
type Take struct {
	ID         string                 `json:"id"`
	Track      string                 `json:"track"`
	Scene      string                 `json:"scene"`
	Expected   string                 `json:"expected"`
	Rate       int                    `json:"rate"`
	Channels   int                    `json:"channels"`
	Frames     uint64                 `json:"frames"`
	Created    time.Time              `json:"created"`
	Stage      Stage                  `json:"stage"`
	Incomplete bool                   `json:"incomplete"`
	Asset      project.Asset          `json:"asset"`
	Before     string                 `json:"before,omitempty"`
	After      string                 `json:"after,omitempty"`
	Candidate  []byte                 `json:"candidate,omitempty"`
	FirstBlock *capture.RecordedBlock `json:"firstBlock,omitempty"`
}
type event struct {
	Stage      Stage                  `json:"stage"`
	Take       *Take                  `json:"take,omitempty"`
	Block      *capture.RecordedBlock `json:"block,omitempty"`
	Frames     uint64                 `json:"frames,omitempty"`
	Incomplete bool                   `json:"incomplete,omitempty"`
	Asset      *project.Asset         `json:"asset,omitempty"`
	Before     string                 `json:"before,omitempty"`
	After      string                 `json:"after,omitempty"`
	Candidate  []byte                 `json:"candidate,omitempty"`
}

type Store struct {
	mu          sync.Mutex
	root        *os.Root
	dir         string
	takes       map[string]*Take
	pending     map[string][]event
	writeFaults map[string]error
	lock        *os.File
	// Boundary is a fault-injection hook, called after the named durable stage.
	Boundary func(Stage)
}

func Namespace(score string) string {
	root, _ := ProjectRoot(score)
	rel, _ := filepath.Rel(root, score)
	h := sha256.Sum256([]byte(filepath.ToSlash(rel)))
	return fmt.Sprintf(".cicada/takes/%x", h[:8])
}
func Revision(source []byte) string { h := sha256.Sum256(source); return hex.EncodeToString(h[:]) }

func Open(score string) (*Store, error) {
	projectDir, err := ProjectRoot(score)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return nil, err
	}
	s := &Store{root: root, dir: Namespace(score), takes: map[string]*Take{}, pending: map[string][]event{}, writeFaults: map[string]error{}}
	if err = s.mkdir(s.dir); err != nil {
		root.Close()
		return nil, err
	}
	s.lock, err = root.OpenFile(s.dir+"/lock", os.O_CREATE|os.O_RDWR, 0600)
	if err == nil {
		err = lockJournal(s.lock)
	}
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("take journal is already open or unavailable: %w", err)
	}
	if err := s.loadJournals(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// loadJournals reads the durable prefix and repairs only an unterminated tail.
// Recovery discards volatile batches and faults before admitting another append.
func (s *Store) loadJournals() error {
	takes := map[string]*Take{}
	d, err := s.root.Open(s.dir)
	if err != nil {
		return err
	}
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if filepath.Ext(name) != ".jsonl" {
			continue
		}
		file, err := s.root.Open(s.dir + "/" + name)
		if err != nil {
			return err
		}
		t, n, err := decode(file)
		info, statErr := file.Stat()
		file.Close()
		if err == nil {
			err = statErr
		}
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if n < info.Size() {
			f, e := s.root.OpenFile(s.dir+"/"+name, os.O_WRONLY, 0600)
			if e == nil {
				e = f.Truncate(int64(n))
				if e == nil {
					e = f.Sync()
				}
				f.Close()
			}
			if e != nil {
				return e
			}
		}
		if t == nil {
			continue
		}
		if name != t.ID+".jsonl" || !validID(t.ID) || !validID(t.Track) || !validID(t.Scene) || t.Rate < 8000 || t.Rate > 384000 || t.Channels < 1 || t.Channels > 2 {
			return errors.New("invalid take journal identity or format")
		}
		takes[t.ID] = t
	}

	s.takes = takes
	s.pending = map[string][]event{}
	s.writeFaults = map[string]error{}
	return nil
}

func (s *Store) Close() error {
	if s.lock != nil {
		s.lock.Close()
	}
	if s.root != nil {
		return s.root.Close()
	}
	return nil
}
func validID(v string) bool {
	if len(v) == 0 {
		return false
	}
	for i, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && (c >= '0' && c <= '9' || c == '-')) {
			return false
		}
	}
	return true
}
func (s *Store) mkdir(path string) error {
	// Sync each parent, so a new nested directory survives a power loss.
	if err := s.root.MkdirAll(path, 0700); err != nil {
		return err
	}
	for p := path; ; p = filepath.Dir(p) {
		if err := s.syncDir(p); err != nil {
			return err
		}
		if p == "." {
			break
		}
	}
	return nil
}
func (s *Store) syncDir(path string) error {
	f, err := s.root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return syncDirectory(f)
}
func (s *Store) logPath(id string) string      { return s.dir + "/" + id + ".jsonl" }
func (s *Store) rawPath(id string) string      { return s.dir + "/" + id + ".pcm" }
func (s *Store) wavPath(id string) string      { return s.dir + "/" + id + ".wav" }
func (s *Store) append(t *Take, e event) error { return s.appendRecords(t, []event{e}) }
func (s *Store) appendRecords(t *Take, events []event) (err error) {
	if fault := s.writeFaults[t.ID]; fault != nil {
		return fault
	}
	// Every event append can leave a torn record, including completion events.
	// Block all retries until recovery has reread and repaired the durable tail.
	defer func() {
		if err != nil {
			s.writeFaults[t.ID] = err
		}
	}()
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, e := range events {
		if err := encoder.Encode(e); err != nil {
			return err
		}
	}
	f, err := s.root.OpenFile(s.logPath(t.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	n, err := f.Write(data.Bytes())
	if err == nil && n != data.Len() {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if events[0].Stage == Begun {
		if err = s.syncDir(s.dir); err != nil {
			return err
		}
	}
	for _, e := range events {
		apply(t, e)
	}
	s.Checkpoint(events[len(events)-1].Stage)
	return nil
}

// Flush acknowledges all pending blocks after PCM and journal have reached
// stable storage. Finalize always flushes, including a short last batch.
func (s *Store) Flush(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.takes[id]
	if t == nil {
		return errors.New("unknown take")
	}
	return s.flush(t)
}
func (s *Store) flush(t *Take) (err error) {
	if fault := s.writeFaults[t.ID]; fault != nil {
		return fault
	}
	defer func() {
		if err != nil {
			s.writeFaults[t.ID] = err
		}
	}()
	events := s.pending[t.ID]
	if len(events) == 0 {
		return nil
	}
	raw, err := s.root.OpenFile(s.rawPath(t.ID), os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = raw.Sync()
	raw.Close()
	if err != nil {
		return err
	}
	if err = s.appendRecords(t, events); err != nil {
		return err
	}
	delete(s.pending, t.ID)
	return nil
}
func apply(t *Take, e event) {
	if e.Take != nil {
		*t = *e.Take
	}
	t.Stage = e.Stage
	if e.Block != nil {
		if t.FirstBlock == nil {
			b := *e.Block
			t.FirstBlock = &b
		}
		t.Frames = e.Frames
		t.Incomplete = t.Incomplete || e.Block.Timing.Flags != 0 || e.Block.Timing.GapFrames != 0
	}
	t.Incomplete = t.Incomplete || e.Incomplete
	if e.Asset != nil {
		t.Asset = *e.Asset
	}
	if e.Candidate != nil {
		t.Candidate = e.Candidate
		t.Before = e.Before
		t.After = e.After
	}
}

// decode streams complete records and discards only a torn last line. Timing
// stays on disk; memory holds the first block needed for clip placement.
func decode(reader io.Reader) (*Take, int64, error) {
	var t *Take
	var offset int64
	r := bufio.NewReader(reader)
	for {
		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			return t, offset, nil
		}
		if err != nil {
			return nil, offset, err
		}
		var e event
		if err = json.Unmarshal(line, &e); err != nil {
			return nil, offset, err
		}
		if t == nil {
			if e.Stage != Begun || e.Take == nil {
				return nil, offset, errors.New("journal must begin with take")
			}
			t = &Take{}
		}
		apply(t, e)
		offset += int64(len(line))
	}
}
func (s *Store) Begin(track, scene, expected string, rate, channels int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(track) || !validID(scene) || len(expected) != 64 || rate < 8000 || rate > 384000 || channels < 1 || channels > 2 {
		return "", errors.New("invalid take target, revision or format")
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	t := &Take{ID: "take-" + hex.EncodeToString(id[:]), Track: track, Scene: scene, Expected: expected, Rate: rate, Channels: channels, Created: time.Now().UTC()}
	f, err := s.root.OpenFile(s.rawPath(t.ID), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	err = f.Sync()
	f.Close()
	if err != nil {
		return "", err
	}
	if err = s.append(t, event{Stage: Begun, Take: t}); err != nil {
		return "", err
	}
	s.takes[t.ID] = t
	return t.ID, nil
}
func clone(t *Take) Take {
	v := *t
	if t.FirstBlock != nil {
		b := *t.FirstBlock
		v.FirstBlock = &b
	}
	v.Candidate = bytes.Clone(t.Candidate)
	return v
}
func (s *Store) Takes() []Take {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ts []Take
	for _, t := range s.takes {
		ts = append(ts, clone(t))
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Created.Before(ts[j].Created) })
	return ts
}
func (s *Store) Get(id string) (Take, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.takes[id]
	if t == nil {
		return Take{}, errors.New("unknown take")
	}
	return clone(t), nil
}
func (s *Store) Writer(id string) capture.Writer {
	return func(b capture.RecordedBlock, pcm [][]float32) error { return s.Write(id, b, pcm) }
}
func (s *Store) Write(id string, b capture.RecordedBlock, pcm [][]float32) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fault := s.writeFaults[id]; fault != nil {
		return fault
	}
	defer func() {
		if err != nil {
			s.writeFaults[id] = err
		}
	}()
	t := s.takes[id]
	if t == nil || t.Stage != Begun && t.Stage != Chunk {
		return errors.New("take is not capturing")
	}
	cursor := t.Frames
	if es := s.pending[id]; len(es) > 0 {
		cursor = es[len(es)-1].Frames
	}
	// A drained recorder can end with a gap and no following PCM block.
	// Persist that interval as a sparse hole with its discontinuity metadata.
	gapOnly := b.Timing.Frames == 0 && b.Timing.GapFrames > 0 && len(pcm) == 0 && b.RawFrame >= cursor && b.RawFrame-cursor == b.Timing.GapFrames
	if !gapOnly && (len(pcm) != t.Channels || b.Timing.Frames <= 0) || b.Timing.SampleRate != t.Rate || b.RawFrame < cursor {
		return errors.New("capture block format or position changed")
	}
	for _, ch := range pcm {
		if len(ch) != b.Timing.Frames {
			return errors.New("capture channel length mismatch")
		}
	}
	end := b.RawFrame + uint64(b.Timing.Frames)
	if end < b.RawFrame || end > uint64((math.MaxUint32-36)/(t.Channels*4)) {
		return errors.New("take exceeds RIFF WAV capacity")
	}
	f, err := s.root.OpenFile(s.rawPath(id), os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	// Sparse holes represent declared lost device frames; original samples stay intact.
	if err = f.Truncate(int64(b.RawFrame) * int64(t.Channels*4)); err != nil {
		return err
	}
	if _, err = f.Seek(int64(b.RawFrame)*int64(t.Channels*4), io.SeekStart); err != nil {
		return err
	}
	data := make([]byte, b.Timing.Frames*t.Channels*4)
	for i := 0; i < b.Timing.Frames; i++ {
		for ch := range pcm {
			binary.LittleEndian.PutUint32(data[(i*t.Channels+ch)*4:], math.Float32bits(pcm[ch][i]))
		}
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	s.pending[id] = append(s.pending[id], event{Stage: Chunk, Block: &b, Frames: end})
	if end-t.Frames >= uint64(t.Rate/10) {
		return s.flush(t)
	}
	return nil
}
func wavHeader(t *Take) []byte {
	h := make([]byte, 44)
	copy(h, "RIFF")
	n := uint32(t.Frames) * uint32(t.Channels*4)
	binary.LittleEndian.PutUint32(h[4:], 36+n)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 3)
	binary.LittleEndian.PutUint16(h[22:], uint16(t.Channels))
	binary.LittleEndian.PutUint32(h[24:], uint32(t.Rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(t.Rate*t.Channels*4))
	binary.LittleEndian.PutUint16(h[32:], uint16(t.Channels*4))
	binary.LittleEndian.PutUint16(h[34:], 32)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], n)
	return h
}
func (s *Store) Finalize(id string, incomplete bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finalize(s.takes[id], incomplete)
}
func (s *Store) finalize(t *Take, incomplete bool) error {
	if t == nil {
		return errors.New("unknown take")
	}
	if fault := s.writeFaults[t.ID]; fault != nil {
		return fmt.Errorf("take writer failed; reopen for durable-prefix recovery: %w", fault)
	}
	if t.Stage != Begun && t.Stage != Chunk {
		return nil
	}
	if err := s.flush(t); err != nil {
		return err
	}
	if t.Frames == 0 {
		return errors.New("take has no durable PCM")
	}
	raw, err := s.root.Open(s.rawPath(t.ID))
	if err != nil {
		return err
	}
	defer raw.Close()
	dst, err := s.root.OpenFile(s.wavPath(t.ID), os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err = dst.Write(wavHeader(t)); err != nil {
		return err
	}
	n := int64(t.Frames) * int64(t.Channels*4)
	if copied, e := io.CopyN(dst, raw, n); e != nil || copied != n {
		return fmt.Errorf("incomplete raw PCM: %w", e)
	}
	if err = dst.Sync(); err != nil {
		return err
	}
	if err = s.syncDir(s.dir); err != nil {
		return err
	}
	s.Checkpoint(WAVWritten)
	if _, err = dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h, err := audioasset.ReadWAVHeader(dst)
	if err != nil {
		return err
	}
	if _, err = dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	hash, err := audioasset.SHA256(dst)
	if err != nil {
		return err
	}
	a := project.Asset{Name: t.ID, Path: fmt.Sprintf("audio/takes/%s-%s-%s.wav", t.Track, t.Created.Format("20060102T150405Z"), t.ID), SHA256: hash, Format: "wav", Frames: h.Frames, RateHz: h.RateHz, Channels: h.Channels, Source: "recorded"}
	return s.append(t, event{Stage: Finalized, Asset: &a, Incomplete: incomplete})
}
func (s *Store) Publish(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.publish(s.takes[id])
}
func (s *Store) publish(t *Take) error {
	if t == nil {
		return errors.New("unknown take")
	}
	if t.Stage == Begun || t.Stage == Chunk {
		return errors.New("finalize take before publication")
	}
	if !notationAsset(t.Asset) {
		return errors.New("invalid published asset")
	}
	blob := "audio/blobs/" + t.Asset.SHA256 + ".wav"
	if err := s.mkdir("audio/blobs"); err != nil {
		return err
	}
	if err := s.mkdir("audio/takes"); err != nil {
		return err
	}
	if err := s.copyImmutable(s.wavPath(t.ID), blob, t.Asset); err != nil {
		return err
	}
	s.Checkpoint(BlobLinked)
	if t.Stage == Finalized {
		if err := s.append(t, event{Stage: Blob}); err != nil {
			return err
		}
	}
	if err := s.copyImmutable(blob, t.Asset.Path, t.Asset); err != nil {
		return err
	}
	s.Checkpoint(TakeLinked)
	if t.Stage == Blob {
		return s.append(t, event{Stage: Published})
	}
	return nil
}
func notationAsset(a project.Asset) bool {
	_, err := hex.DecodeString(a.SHA256)
	return err == nil && len(a.SHA256) == 64 && notation.ValidAssetPath(a.Path) && a.Format == "wav" && a.Frames > 0 && a.RateHz >= 8000 && a.RateHz <= 384000 && a.Channels >= 1 && a.Channels <= 2
}
func Verify(root *os.Root, path string, a project.Asset) error {
	if !notationAsset(a) {
		return errors.New("invalid asset declaration")
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("asset is not regular")
	}
	hash, err := audioasset.SHA256(f)
	if err != nil {
		return err
	}
	if hash != a.SHA256 {
		return fmt.Errorf("asset hash mismatch: %s", path)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h, err := audioasset.ReadWAVHeader(f)
	if err != nil {
		return err
	}
	if h.Frames != a.Frames || h.RateHz != a.RateHz || h.Channels != a.Channels {
		return fmt.Errorf("asset dimensions mismatch: %s", path)
	}
	return nil
}
func (s *Store) copyImmutable(from, to string, a project.Asset) error {
	if err := Verify(s.root, to, a); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	// Staging never exposes a partial blob or overwrites existing bytes.
	stage := to + "." + tSafeID(a.Name) + ".tmp"
	src, err := s.root.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := s.root.OpenFile(stage, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if err == nil {
		err = dst.Sync()
	}
	ce := dst.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = Verify(s.root, stage, a); err != nil {
		return err
	}
	// Link is atomic and refuses collisions; unsupported filesystems fail explicitly.
	if err = s.root.Link(stage, to); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if err = Verify(s.root, to, a); err != nil {
			return err
		}
	}
	if err = s.root.Remove(stage); err != nil {
		return err
	}
	return s.syncDir(filepath.Dir(to))
}
func tSafeID(name string) string { h := sha256.Sum256([]byte(name)); return fmt.Sprintf("%x", h[:8]) }
func (s *Store) Prepare(id string, before, after []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.takes[id]
	if t == nil || t.Stage == Begun || t.Stage == Chunk || t.Stage == Finalized || t.Stage == Blob {
		return errors.New("publish take before preparing source")
	}
	return s.append(t, event{Stage: Prepared, Before: Revision(before), After: Revision(after), Candidate: bytes.Clone(after)})
}
func (s *Store) Mark(id string, stage Stage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.takes[id]
	if t == nil || stage != Conflict && stage != Committed && stage != Source {
		return errors.New("invalid take completion")
	}
	return s.append(t, event{Stage: stage})
}

// Recover finalizes acknowledged PCM and republishes assets. Source recovery is
// delegated to the host's existing revision-checked transaction service.
func (s *Store) Recover() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadJournals(); err != nil {
		return err
	}
	for _, t := range s.takes {
		if t.Frames == 0 {
			continue
		}
		if err := s.finalize(t, t.Stage == Begun || t.Stage == Chunk); err != nil {
			return err
		}
		if err := s.publish(t); err != nil {
			return err
		}
	}
	return nil
}

func ProjectRoot(score string) (string, error) {
	_, manifest, err := edition.ScoreEdition(score)
	if err != nil {
		return "", err
	}
	if manifest != "" {
		return filepath.Dir(manifest), nil
	}
	return filepath.Abs(filepath.Dir(score))
}

// Checkpoint exposes additional durable file boundaries to crash tests.
func (s *Store) Checkpoint(stage Stage) {
	if s.Boundary != nil {
		s.Boundary(stage)
	}
}
