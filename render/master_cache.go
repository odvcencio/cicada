package render

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"

	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
)

// MasterCache lets repeated direct renders of one score skip synthesis.
//
// A render that sets Options.MasterCache stores the signal that enters the
// master stage as it renders: the summed music and SFX buses, before the export
// gain, the master chain, the DC correction and the safety limiter. A later
// render of the same score and range replays that signal through the master
// stage instead of synthesizing it again, and writes the same bytes and report
// as a full render. Loudness targeting relies on this, because each of its
// passes changes only the export gain and the DC correction.
//
// The signal lives in a temporary file of 8 bytes per frame. Only the direct
// renderer with a master chain uses the cache; the scheduled engine, stem
// renders and renders that set NoteTap ignore it. A render with another score,
// range, sample rate or block size replaces what the cache holds. If the file
// cannot be written, for example because the disk is full, the cache turns
// itself off and renders continue without it. A MasterCache is not safe for
// concurrent use. Close removes the file.
type MasterCache struct {
	file     *os.File
	key      masterCacheKey
	frames   int64 // frames the stored capture holds, or the current capture will hold
	ready    bool  // the file holds a complete capture of key
	disabled bool  // the file failed; renders run without the cache
	position int64 // frames written or read so far in the current render
	replays  int
	block    []float32 // interleaved stereo frames of the current block
	scratch  []byte
}

// masterCacheKey is everything besides the export gain and the DC correction
// that changes the signal entering the master stage.
type masterCacheKey struct {
	score      *notation.Score
	sampleRate int
	block      int
	from, bars int
	tailSec    float64
	assetRoot  string
	assetDir   string
	baseline   bool
}

// NewMasterCache creates the cache's temporary file in dir.
func NewMasterCache(dir string) (*MasterCache, error) {
	file, err := os.CreateTemp(dir, ".cicada-master-cache-*")
	if err != nil {
		return nil, err
	}
	return &MasterCache{file: file}, nil
}

// Close removes the temporary file. It is safe to call on a nil or closed
// cache.
func (c *MasterCache) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	name := c.file.Name()
	err := c.file.Close()
	c.file, c.ready = nil, false
	if removeErr := os.Remove(name); err == nil && !os.IsNotExist(removeErr) {
		err = removeErr
	}
	return err
}

// Replays reports how many renders have played back from the cache.
func (c *MasterCache) Replays() int {
	if c == nil {
		return 0
	}
	return c.replays
}

// begin decides what a render does with the cache. Without use the render
// ignores the cache. With use and replay the render plays the stored signal
// back; with use alone it stores the signal as it synthesizes it.
func (c *MasterCache) begin(key masterCacheKey, frames int64) (use, replay bool, err error) {
	if c == nil || c.file == nil || c.disabled {
		return false, false, nil
	}
	c.position = 0
	if c.ready && c.key == key && c.frames == frames {
		if _, err := c.file.Seek(0, io.SeekStart); err != nil {
			return false, false, fmt.Errorf("rewind master cache: %w", err)
		}
		c.replays++
		return true, true, nil
	}
	c.key, c.frames, c.ready = key, frames, false
	if err := c.file.Truncate(0); err != nil {
		c.disabled = true
		return false, false, nil
	}
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		c.disabled = true
		return false, false, nil
	}
	return true, false, nil
}

// beginBlock prepares the next block: a replay loads its frames from the file
// into c.block, and a capture clears c.block for renderBlock to fill.
func (c *MasterCache) beginBlock(frames int, replay bool) error {
	c.block = c.block[:0]
	if !replay {
		return nil
	}
	if c.position+int64(frames) > c.frames {
		return fmt.Errorf("master cache holds %d frames, render asked for more", c.frames)
	}
	if cap(c.scratch) < frames*8 {
		c.scratch = make([]byte, frames*8)
	}
	data := c.scratch[:frames*8]
	if _, err := io.ReadFull(c.file, data); err != nil {
		return fmt.Errorf("read master cache: %w", err)
	}
	if cap(c.block) < frames*2 {
		c.block = make([]float32, 0, frames*2)
	}
	c.block = c.block[:frames*2]
	for i := range c.block {
		c.block[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	c.position += int64(frames)
	return nil
}

// endBlock writes the block that renderBlock captured. A failed write turns
// the cache off instead of failing the render.
func (c *MasterCache) endBlock() {
	if cap(c.scratch) < len(c.block)*4 {
		c.scratch = make([]byte, len(c.block)*4)
	}
	data := c.scratch[:len(c.block)*4]
	for i, sample := range c.block {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(sample))
	}
	if _, err := c.file.Write(data); err != nil {
		c.disabled, c.ready = true, false
		return
	}
	c.position += int64(len(c.block) / 2)
}

// finishCapture marks a completed capture ready for replay.
func (c *MasterCache) finishCapture() error {
	if c.disabled {
		return nil
	}
	if c.position != c.frames {
		return fmt.Errorf("master cache captured %d frames, expected %d", c.position, c.frames)
	}
	c.ready = true
	return nil
}

// renderMasterReplay finishes a render from the stored signal. The caller has
// already written the WAV header and holds the encoder, limiter, gains and
// report that a full render would use at this point, so the master stage sees
// the same input in the same order and the output matches byte for byte.
func renderMasterReplay(writer io.Writer, score *notation.Score, opts Options, cache *MasterCache, report Report, encoder *wavEncoder, limiter *mix.Limiter, busState busMixerState, masterGain float32, buffer []byte) (Report, error) {
	for position := int64(0); position < cache.frames; {
		frames := int(min(int64(opts.Block), cache.frames-position))
		if err := renderBlock(writer, nil, nil, nil, nil, 0, opts.MasterBiasL, opts.MasterBiasR, masterGain, busState, limiter, nil, encoder, nil, position, frames, buffer, &report); err != nil {
			return report, err
		}
		position += int64(frames)
	}
	if err := finishWAV(writer, score, limiter, nil, encoder, buffer, &report); err != nil {
		return report, err
	}
	return report, nil
}
