package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// Stems renders every track, effect return, bus, and the master in one pass.
// Each file is stereo IEEE float32 WAV. The music and track taps precede the
// music compressor; master includes the compressor and limiter.
func Stems(score *notation.Score, opts Options, dir string) (Report, error) {
	if dir == "" {
		return Report{}, fmt.Errorf("stem output directory is required")
	}
	return renderWAV(score, opts, io.Discard, filepath.Clean(dir))
}

type stemFile struct {
	file   *os.File
	buffer []byte
	frames int64
}

type stemPair struct{ left, right float32 }

type stemOutput struct {
	dir, temporary string
	files          []stemFile
	frame          []stemPair
	report         Report
	mixFrames      int64
	skipFrames     int64
	committed      bool
}

func newStemOutput(dir string, p *project.Project, report Report) (*stemOutput, error) {
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
	stems := &stemOutput{dir: dir, temporary: temporary, report: report}
	defer func() {
		if err != nil {
			stems.abort()
		}
	}()
	names := make([]string, 0, len(p.Tracks)+5)
	for index, track := range p.Tracks {
		if track.ID == "" || track.ID == "." || track.ID == ".." || filepath.Base(track.ID) != track.ID {
			err = fmt.Errorf("invalid track id for stem file: %q", track.ID)
			return nil, err
		}
		names = append(names, fmt.Sprintf("%02d-%s.wav", index+1, track.ID))
	}
	names = append(names, "return-a.wav", "return-b.wav", "music.wav", "sfx.wav", "master.wav")
	stems.frame = make([]stemPair, len(names)-1)
	for _, name := range names {
		var file *os.File
		file, err = os.OpenFile(filepath.Join(temporary, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return nil, err
		}
		stems.files = append(stems.files, stemFile{file: file, buffer: make([]byte, 0, 4096*8)})
		err = writeFloatHeader(file, report.SampleRate, uint32(report.Frames*8))
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
		s.files[i].append(frame.left, frame.right)
	}
	s.mixFrames++
}

func (s *stemOutput) appendMaster(left, right float32) {
	s.files[len(s.files)-1].append(left, right)
}

func (f *stemFile) append(left, right float32) {
	f.buffer = binary.LittleEndian.AppendUint32(f.buffer, math.Float32bits(left))
	f.buffer = binary.LittleEndian.AppendUint32(f.buffer, math.Float32bits(right))
	f.frames++
}

func (s *stemOutput) flush() error {
	for i := range s.files {
		f := &s.files[i]
		if len(f.buffer) == 0 {
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
		if f.frames != s.report.Frames {
			return fmt.Errorf("stem %d wrote %d frames, expected %d", i, f.frames, s.report.Frames)
		}
		if err := writeMetadata(f.file, tempoMilli, uint32(s.report.Bars), uint32(s.report.TailFrames)); err != nil {
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

func writeFloatHeader(w io.Writer, sampleRate int, dataBytes uint32) error {
	var header [44]byte
	copy(header[:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], dataBytes+60)
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
