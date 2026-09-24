package render

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

type VerifyStemsOptions struct {
	ResidualMaxDB float64
}

type VerifyStemsReport struct {
	Files          int
	Frames         int64
	SampleRate     int
	Bars           int
	MusicResidual  float64
	SFXResidual    float64
	ResidualPeakDB float64
}

// VerifyStems checks float WAV containers, alignment, and the pre-compressor
// bus equations. The master is checked for a valid container and finite data;
// its nonlinear processing means it is not compared to a stem sum.
func VerifyStems(score *notation.Score, dir string, opts VerifyStemsOptions) (VerifyStemsReport, error) {
	var report VerifyStemsReport
	if math.IsNaN(opts.ResidualMaxDB) || math.IsInf(opts.ResidualMaxDB, 0) {
		return report, fmt.Errorf("invalid residual maximum")
	}
	if opts.ResidualMaxDB == 0 {
		opts.ResidualMaxDB = -80
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return report, err
	}
	var manifest stemManifest
	if len(manifestData) > 8192 || json.Unmarshal(manifestData, &manifest) != nil || manifest.Version != 1 || len(manifest.Tracks) < 1 || len(manifest.Tracks) > 16 {
		return report, fmt.Errorf("invalid stem manifest")
	}
	seen := map[string]bool{}
	for _, track := range manifest.Tracks {
		if track.ID == "" || track.ID == "." || track.ID == ".." || filepath.Base(track.ID) != track.ID || seen[track.ID] || track.Bus != "music" && track.Bus != "sfx" {
			return report, fmt.Errorf("invalid stem manifest track")
		}
		seen[track.ID] = true
	}
	songBars := 256
	if score != nil {
		p, diagnostics := project.FromScore(score)
		if p == nil {
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == "error" {
					return report, fmt.Errorf("%s: %s", diagnostic.Code, diagnostic.Message)
				}
			}
			return report, fmt.Errorf("score cannot compile to a Cicada project")
		}
		if len(p.Tracks) != len(manifest.Tracks) {
			return report, fmt.Errorf("stem manifest differs from score")
		}
		for i, track := range p.Tracks {
			if track.ID != manifest.Tracks[i].ID || track.Mixer.Bus != manifest.Tracks[i].Bus {
				return report, fmt.Errorf("stem manifest differs from score")
			}
		}
		songBars = 0
		for _, entry := range p.Song {
			songBars += int(entry.Bars)
		}
	}
	names := make([]string, 0, len(manifest.Tracks)+5)
	for index, track := range manifest.Tracks {
		names = append(names, fmt.Sprintf("%02d-%s.wav", index+1, track.ID))
	}
	names = append(names, "return-a.wav", "return-b.wav", "music.wav", "sfx.wav", "master.wav")
	files := make([]*os.File, 0, len(names))
	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()
	readers := make([]*bufio.Reader, 0, len(names))
	var reference [3]uint32 // sample rate, frames, bars
	var timing [2]uint32    // tempo milli, tail frames
	for i, name := range names {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return report, err
		}
		files = append(files, file)
		info, err := file.Stat()
		if err != nil {
			return report, err
		}
		var header [44]byte
		if _, err := io.ReadFull(file, header[:]); err != nil {
			return report, fmt.Errorf("%s: %w", name, err)
		}
		get16 := func(offset int) uint16 { return binary.LittleEndian.Uint16(header[offset : offset+2]) }
		get32 := func(offset int) uint32 { return binary.LittleEndian.Uint32(header[offset : offset+4]) }
		dataBytes := get32(40)
		if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" || string(header[12:16]) != "fmt " || string(header[36:40]) != "data" ||
			get32(4) != uint32(info.Size()-8) || get32(16) != 16 || get16(20) != 3 || get16(22) != 2 ||
			get32(28) != get32(24)*8 || get16(32) != 8 || get16(34) != 32 || dataBytes%8 != 0 || info.Size() != int64(dataBytes)+68 {
			return report, fmt.Errorf("%s: invalid stereo float32 WAV", name)
		}
		var metadata [24]byte
		if _, err := file.ReadAt(metadata[:], 44+int64(dataBytes)); err != nil {
			return report, fmt.Errorf("%s: %w", name, err)
		}
		if string(metadata[:4]) != "cica" || binary.LittleEndian.Uint32(metadata[4:8]) != 16 || binary.LittleEndian.Uint32(metadata[8:12]) != 1 {
			return report, fmt.Errorf("%s: missing Cicada timing metadata", name)
		}
		current := [3]uint32{get32(24), dataBytes / 8, binary.LittleEndian.Uint32(metadata[16:20])}
		currentTiming := [2]uint32{binary.LittleEndian.Uint32(metadata[12:16]), binary.LittleEndian.Uint32(metadata[20:24])}
		if i == 0 {
			reference, timing = current, currentTiming
			if current[0] != 44_100 && current[0] != 48_000 && current[0] != 96_000 || current[2] < 1 || current[2] > 256 || int(current[2]) > songBars || score != nil && currentTiming[0] != uint32(score.TempoMilli) || currentTiming[1] > current[0]*10 {
				return report, fmt.Errorf("%s: invalid rate, bars, tempo, or tail", name)
			}
			clock, err := seq.NewClock(int(current[0]), int64(currentTiming[0]))
			if err != nil || clock.SampleAtTick(int64(current[2])*seq.TicksPerBar)+int64(currentTiming[1]) != int64(current[1]) {
				return report, fmt.Errorf("%s: frame count differs from musical duration", name)
			}
		} else if current != reference || currentTiming != timing {
			return report, fmt.Errorf("%s: stem timing differs", name)
		}
		readers = append(readers, bufio.NewReaderSize(file, 32*1024))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return report, err
	}
	if len(entries) != len(names)+1 {
		return report, fmt.Errorf("stem directory contains %d entries, expected %d", len(entries), len(names)+1)
	}
	report.Files, report.Frames, report.SampleRate, report.Bars = len(names), int64(reference[1]), int(reference[0]), int(reference[2])
	var data [8]byte
	var samples [21]stemPair
	for frame := int64(0); frame < report.Frames; frame++ {
		for i, reader := range readers {
			if _, err := io.ReadFull(reader, data[:]); err != nil {
				return report, fmt.Errorf("%s frame %d: %w", names[i], frame, err)
			}
			left := math.Float32frombits(binary.LittleEndian.Uint32(data[:4]))
			right := math.Float32frombits(binary.LittleEndian.Uint32(data[4:]))
			if math.IsNaN(float64(left)) || math.IsInf(float64(left), 0) || math.IsNaN(float64(right)) || math.IsInf(float64(right), 0) {
				return report, fmt.Errorf("%s frame %d: nonfinite sample", names[i], frame)
			}
			samples[i] = stemPair{left, right}
		}
		var musicL, musicR, sfxL, sfxR float64
		for i, track := range manifest.Tracks {
			if track.Bus == "sfx" {
				sfxL += float64(samples[i].left)
				sfxR += float64(samples[i].right)
			} else {
				musicL += float64(samples[i].left)
				musicR += float64(samples[i].right)
			}
		}
		n := len(manifest.Tracks)
		musicL += float64(samples[n].left) + float64(samples[n+1].left)
		musicR += float64(samples[n].right) + float64(samples[n+1].right)
		report.MusicResidual = max(report.MusicResidual, math.Abs(musicL-float64(samples[n+2].left)), math.Abs(musicR-float64(samples[n+2].right)))
		report.SFXResidual = max(report.SFXResidual, math.Abs(sfxL-float64(samples[n+3].left)), math.Abs(sfxR-float64(samples[n+3].right)))
	}
	report.ResidualPeakDB = amplitudeDB(max(report.MusicResidual, report.SFXResidual))
	if report.ResidualPeakDB > opts.ResidualMaxDB {
		return report, fmt.Errorf("stem residual %.2f dBFS exceeds %.2f dBFS", report.ResidualPeakDB, opts.ResidualMaxDB)
	}
	return report, nil
}
