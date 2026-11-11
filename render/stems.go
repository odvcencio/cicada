package render

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Stems renders every track, effect return, bus, and the master in one pass.
// Each file is stereo IEEE float32 WAV when opts.Bits is 32 (or zero, which
// Stems reads as 32), or 16- or 24-bit PCM. The music and track taps precede
// the music compressor; master includes the compressor and limiter.
//
// With opts.StemTracks set, Stems writes only those tracks' stems and
// master.wav. The audio is the same: every track still plays, so the master
// and each written stem equal the full set's files. An unknown name is an
// *UnknownTrackError.
func Stems(score *notation.Score, opts Options, dir string) (Report, error) {
	if dir == "" {
		return Report{}, fmt.Errorf("stem output directory is required")
	}
	if opts.Bits == 0 {
		opts.Bits = 32
	}
	if score != nil && len(opts.StemTracks) > 0 {
		// Check the names before any pack loads, against the compiled track
		// ids that name the stem files.
		if p, _ := project.FromScore(score); p != nil {
			ids := make([]string, 0, len(p.Tracks))
			for _, track := range p.Tracks {
				ids = append(ids, track.ID)
			}
			if err := CheckTrackNames(opts.StemTracks, ids); err != nil {
				return Report{}, err
			}
		}
	}
	return renderWithOptions(score, opts, io.Discard, filepath.Clean(dir))
}

// UnknownTrackError is a track name that the score does not declare. Valid
// lists the score's track names in score order.
type UnknownTrackError struct {
	Name  string
	Valid []string
}

func (e *UnknownTrackError) Error() string {
	return fmt.Sprintf("unknown track %q; the score's tracks are %s", e.Name, strings.Join(e.Valid, ", "))
}

// CheckTrackNames returns an *UnknownTrackError for the first name that is not
// in valid, or an error for a name given twice or an empty list entry.
func CheckTrackNames(names, valid []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" {
			return fmt.Errorf("empty track name in the track list")
		}
		if seen[name] {
			return fmt.Errorf("track %q is listed twice", name)
		}
		seen[name] = true
		found := false
		for _, id := range valid {
			found = found || id == name
		}
		if !found {
			return &UnknownTrackError{Name: name, Valid: append([]string(nil), valid...)}
		}
	}
	return nil
}

type stemFile struct {
	file   *os.File
	buffer []byte
	frames int64
	// skip marks a stem that StemTracks leaves out: it has no file.
	skip bool
	// encoder writes 16- or 24-bit PCM; nil writes float32.
	encoder *wavEncoder
	counts  Report // peak and clip counts of the integer encoder
}

type stemPair struct{ left, right float32 }

type stemOutput struct {
	dir, temporary string
	files          []stemFile
	frame          []stemPair
	manifest       stemManifest
	report         Report
	mixFrames      int64
	skipFrames     int64
	committed      bool
}

type stemManifest struct {
	Version int              `json:"version"`
	From    int              `json:"from,omitempty"`
	Tracks  []stemTrackRoute `json:"tracks"`
	// Selected lists the tracks a --tracks render wrote; the set then has no
	// return or bus stems, and verify-stems refuses it. Absent for a full set.
	Selected []string `json:"selected,omitempty"`
	// Bits is the sample format when it is not float32.
	Bits int `json:"bits,omitempty"`
}

type stemTrackRoute struct {
	ID  string `json:"id"`
	Bus string `json:"bus"`
}

func newStemOutput(dir string, p *project.Project, report Report, opts Options, seed uint64) (*stemOutput, error) {
	if len(p.Tracks) == 0 {
		return nil, fmt.Errorf("score has no tracks")
	}
	if report.Frames > (int64(^uint32(0))-60)/8 {
		return nil, fmt.Errorf("stems exceed RIFF size limit")
	}
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("stem output directory already exists: %s", dir)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	parent := filepath.Dir(filepath.Clean(dir))
	if err := os.MkdirAll(parent, 0755); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(parent, ".cicada-stems-*")
	if err != nil {
		return nil, err
	}
	stems := &stemOutput{dir: dir, temporary: temporary, report: report, manifest: stemManifest{Version: 2, From: report.From + 1}}
	defer func() {
		if err != nil {
			stems.abort()
		}
	}()
	bits := opts.Bits
	if bits == 0 {
		bits = 32
	}
	if bits != 16 && bits != 24 && bits != 32 {
		err = fmt.Errorf("stem bits must be 16, 24, or 32 float")
		return nil, err
	}
	if bits != 32 {
		stems.manifest.Bits = bits
	}
	selected := map[string]bool{}
	for _, id := range opts.StemTracks {
		selected[id] = true
	}
	names := make([]string, 0, len(p.Tracks)+5)
	skip := make([]bool, 0, len(p.Tracks)+5)
	for index, track := range p.Tracks {
		if track.ID == "" || track.ID == "." || track.ID == ".." || filepath.Base(track.ID) != track.ID {
			err = fmt.Errorf("invalid track id for stem file: %q", track.ID)
			return nil, err
		}
		names = append(names, fmt.Sprintf("%02d-%s.wav", index+1, track.ID))
		skip = append(skip, len(selected) > 0 && !selected[track.ID])
		stems.manifest.Tracks = append(stems.manifest.Tracks, stemTrackRoute{ID: track.ID, Bus: track.Mixer.Bus})
		if selected[track.ID] {
			stems.manifest.Selected = append(stems.manifest.Selected, track.ID)
		}
	}
	names = append(names, "return-a.wav", "return-b.wav", "music.wav", "sfx.wav", "master.wav")
	partial := len(selected) > 0
	skip = append(skip, partial, partial, partial, partial, false)
	stems.frame = make([]stemPair, len(names)-1)
	dither := opts.Dither == nil || *opts.Dither
	for i, name := range names {
		if skip[i] {
			stems.files = append(stems.files, stemFile{skip: true})
			continue
		}
		var file *os.File
		file, err = os.OpenFile(filepath.Join(temporary, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		stem := stemFile{file: file, buffer: make([]byte, 0, 4096*8)}
		if bits == 32 {
			err = writeFloatHeader(file, report.SampleRate, uint32(report.Frames*8), len(report.tempoChanges))
		} else {
			// Each stem draws its own dither from the score seed and its
			// position in the set, so the files are reproducible.
			var encoder wavEncoder
			encoder, err = newWAVEncoder(bits, dither, seed^uint64(0x57E40000+i), 1)
			if err != nil {
				stems.files = append(stems.files, stem)
				return nil, err
			}
			stem.encoder = &encoder
			err = writeWAVHeaderTempo(file, report.SampleRate, bits, uint32(report.Frames*int64(bits/4)), len(report.tempoChanges))
		}
		stems.files = append(stems.files, stem)
		if err != nil {
			return nil, err
		}
	}
	return stems, nil
}

func (s *stemOutput) track(index int, left, right float32, track mix.Track, sfx bool) {
	gain := float32(mix.MusicGain)
	if sfx {
		gain = 1
	}
	s.frame[index] = stemPair{left * track.Left * gain, right * track.Right * gain}
}

func (s *stemOutput) returnA(left, right float32) {
	s.frame[len(s.frame)-4] = stemPair{left * mix.MusicGain, right * mix.MusicGain}
}

func (s *stemOutput) returnB(left, right float32) {
	s.frame[len(s.frame)-3] = stemPair{left * mix.MusicGain, right * mix.MusicGain}
}

func (s *stemOutput) buses(musicL, musicR, sfxL, sfxR float32) {
	s.frame[len(s.frame)-2] = stemPair{musicL, musicR}
	s.frame[len(s.frame)-1] = stemPair{sfxL, sfxR}
}

func (s *stemOutput) appendMixFrame(sample int64) {
	if sample < s.skipFrames || sample >= s.skipFrames+s.report.Frames {
		return
	}
	for i, frame := range s.frame {
		if !s.files[i].skip {
			s.files[i].append(frame.left, frame.right)
		}
	}
	s.mixFrames++
}

func (s *stemOutput) appendMaster(left, right float32) {
	s.files[len(s.files)-1].append(left, right)
}

func (f *stemFile) append(left, right float32) {
	if f.encoder != nil {
		var frame [8]byte
		size := f.encoder.frameBytes()
		f.encoder.writeFrame(frame[:size], left, right, 1, &f.counts)
		f.buffer = append(f.buffer, frame[:size]...)
		f.frames++
		return
	}
	f.buffer = binary.LittleEndian.AppendUint32(f.buffer, math.Float32bits(left))
	f.buffer = binary.LittleEndian.AppendUint32(f.buffer, math.Float32bits(right))
	f.frames++
}

func (s *stemOutput) flush() error {
	for i := range s.files {
		f := &s.files[i]
		if f.skip || len(f.buffer) == 0 {
			continue
		}
		n, err := f.file.Write(f.buffer)
		if err != nil {
			return err
		}
		if n != len(f.buffer) {
			return io.ErrShortWrite
		}
		f.buffer = f.buffer[:0]
	}
	return nil
}

func (s *stemOutput) finish(tempoMilli uint32) error {
	if err := s.flush(); err != nil {
		return err
	}
	if s.mixFrames != s.report.Frames {
		return fmt.Errorf("stem taps wrote %d frames, expected %d", s.mixFrames, s.report.Frames)
	}
	for i := range s.files {
		f := &s.files[i]
		if f.skip {
			continue
		}
		if f.frames != s.report.Frames {
			return fmt.Errorf("stem %d wrote %d frames, expected %d", i, f.frames, s.report.Frames)
		}
		if err := writeMetadata(f.file, tempoMilli, uint32(s.report.Bars), uint32(s.report.TailFrames), s.report.tempoChanges); err != nil {
			return err
		}
		if err := f.file.Sync(); err != nil {
			return err
		}
		if err := f.file.Close(); err != nil {
			return err
		}
		f.file = nil
	}
	manifest, err := json.MarshalIndent(s.manifest, "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')
	if err := os.WriteFile(filepath.Join(s.temporary, "manifest.json"), manifest, 0644); err != nil {
		return err
	}
	if _, err := os.Stat(s.dir); err == nil {
		return fmt.Errorf("stem output directory already exists: %s", s.dir)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(s.temporary, s.dir); err != nil {
		return err
	}
	s.committed = true
	return nil
}

func (s *stemOutput) abort() {
	if s == nil || s.committed {
		return
	}
	for i := range s.files {
		if s.files[i].file != nil {
			s.files[i].file.Close()
		}
	}
	os.RemoveAll(s.temporary)
}

func writeFloatHeader(w io.Writer, sampleRate int, dataBytes uint32, tempoChanges int) error {
	var header [44]byte
	copy(header[:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], dataBytes+36+uint32(metadataBytes(tempoChanges)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 3)
	binary.LittleEndian.PutUint16(header[22:24], 2)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*8))
	binary.LittleEndian.PutUint16(header[32:34], 8)
	binary.LittleEndian.PutUint16(header[34:36], 32)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataBytes)
	n, err := w.Write(header[:])
	if err != nil {
		return err
	}
	if n != len(header) {
		return io.ErrShortWrite
	}
	return nil
}
