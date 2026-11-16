package render

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/mix"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// MasterCache lets repeated renders of one score skip synthesis.
//
// A render that sets Options.MasterCache stores, as it renders, the signal at
// the export gain point (project.ExportGainPoint), and a later render of the
// same score and range replays that signal instead of synthesizing it again,
// and writes the same bytes and report as a full render. Loudness targeting
// relies on this, because each of its passes changes only the export gain and
// the DC correction, and so does peak normalization, whose second render
// changes only the output gain. Every gain that differs between the passes
// enters at or after the stored signal, so the stages after it (the last master
// limiter, the inserts after it and the safety limiter) run again on each
// replay and see exactly the input a full render gives them.
//
// Where the signal is stored depends on the master chain:
//   - When the export gain enters inside the chain, ahead of its last limiter
//     or after a chain without one, the chain stores the signal at that point
//     (project.GainPointTap). A replay runs only the gain and what follows it:
//     the inserts ahead of the gain point run as in playback whatever the
//     export gain, so their output is the same on every pass.
//   - Otherwise the render stores the signal that enters the master stage: the
//     summed music and SFX buses after their mutes and the master mute, before
//     the authored master level, the export gain, the master chain, the DC
//     correction and the safety limiter. That is the case without a master
//     chain, when the last limiter is the chain's first insert, and when a
//     limiter runs ahead of the last one, whose reading the report needs.
//
// Both renderers use it. The direct renderer replays the stored signal through
// its own master stage (renderBlock); the scheduled engine replays it through
// the engine's master stage (engine.Engine.RenderMasterWithTaps), the same code
// a full render runs.
//
// The signal lives in a temporary file of 8 bytes per frame (see NewMasterCache
// for when the file has a name), from the start of
// the song to the end of the rendered range, its tail and the latency of the
// track inserts, the master chain and (on the scheduled engine) the safety
// limiter, even when the render starts at a later bar: 384 KB per second of
// audio at 48 kHz, so about 71 MB for a three-minute song. Nothing of it stays
// in memory beyond one block. Stem renders and renders that set NoteTap ignore
// the cache. A render whose score, range (From, Bars, TailSec), SampleRate, Block,
// AssetRoot, AssetDir or SamplerBaseline differs from the stored capture
// replaces that capture. If the file cannot be written, for example because the
// disk is full, the cache turns itself off, empties its file to give the space
// back, and renders continue without it. If the stored signal cannot be read
// back, the replaying render fails, because it has already written part of its
// output; the cache turns itself off and empties its file in the same way, and
// the caller renders again without it.
//
// A completed capture keeps its file until Close, and the file takes disk space
// that the caller's own output may need. A caller that runs short of space
// closes the cache and repeats the failed step without it.
//
// The cache matches a score by its pointer, not by its contents. Do not modify
// a score between renders that share a cache: a changed score would replay the
// signal of the old one. A copy of a score starts a new capture. A MasterCache
// is not safe for concurrent use. Close empties the file and removes it.
type MasterCache struct {
	file     cacheFile
	key      masterCacheKey
	frames   int64 // frames the stored capture holds, or the current capture will hold
	ready    bool  // the file holds a complete capture of key
	disabled bool  // the file failed; renders run without the cache
	position int64 // frames written or read so far in the current render
	replays  int
	block    []float32 // interleaved stereo frames of the current block
	scratch  []byte
	// atPoint is set when the current render's master chain stores the signal
	// at its gain point through point; otherwise the render stores the master
	// input in block itself.
	atPoint bool
	point   project.GainPointTap
}

// cacheFile is the part of *os.File that a MasterCache uses. Tests wrap it to
// fail the way a full disk does.
type cacheFile interface {
	io.ReadWriteSeeker
	Truncate(size int64) error
	Name() string
	Close() error
}

// masterCacheKey is everything besides the export gain and the DC correction
// that changes the signal entering the master stage. It holds the score
// pointer, so the score stays alive while the cache refers to it and no other
// score can take its address.
type masterCacheKey struct {
	score      *notation.Score
	sampleRate int
	block      int
	from, bars int
	tailSec    float64
	assetRoot  string
	assetDir   string
	baseline   bool
	atPoint    bool // the signal is the one at the chain's gain point
}

// ErrMasterCacheRead marks a render that failed because the stored signal could
// not be read back. That render has already written part of its output; the
// cache has turned itself off, so the same render runs in full when repeated.
var ErrMasterCacheRead = errors.New("master cache cannot be read back")

type masterCacheReadError struct {
	what string
	err  error
}

func (e *masterCacheReadError) Error() string        { return e.what + ": " + e.err.Error() }
func (e *masterCacheReadError) Unwrap() error        { return e.err }
func (e *masterCacheReadError) Is(target error) bool { return target == ErrMasterCacheRead }

// MasterCacheFilePattern is the name pattern of the cache's temporary file, as
// os.CreateTemp takes it.
const MasterCacheFilePattern = ".cicada-master-cache-*"

// NewMasterCache creates the cache's temporary file in dir. On Unix the file's
// name is removed at once and the file lives on as the open descriptor alone,
// so its space returns to the disk however the process ends, an interrupt or a
// crash included. Windows cannot remove the name of a file that is open, so
// there the file keeps its name until Close; a caller that can be interrupted
// removes stale files that match MasterCacheFilePattern itself.
func NewMasterCache(dir string) (*MasterCache, error) {
	file, err := os.CreateTemp(dir, MasterCacheFilePattern)
	if err != nil {
		return nil, err
	}
	unlinkOpenFile(file)
	return NewMasterCacheFile(file), nil
}

// NewMasterCacheFile makes a cache that stores its signal in file, which must
// be empty and open for reading and writing. Close empties the file, closes it
// and removes it by name; a name that no longer exists is not an error.
func NewMasterCacheFile(file *os.File) *MasterCache {
	return &MasterCache{file: file}
}

// Close empties the temporary file and removes it, so a file that cannot be
// removed still holds no disk space. It is safe to call on a nil or closed
// cache.
func (c *MasterCache) Close() error {
	if c == nil || c.file == nil {
		return nil
	}
	name := c.file.Name()
	_ = c.file.Truncate(0)
	err := c.file.Close()
	c.file, c.ready = nil, false
	if removeErr := os.Remove(name); err == nil && !os.IsNotExist(removeErr) {
		err = removeErr
	}
	return err
}

// Replays reports how many renders have started to play back from the cache,
// including one that failed part way.
func (c *MasterCache) Replays() int {
	if c == nil {
		return 0
	}
	return c.replays
}

// begin decides what a render does with the cache. Without use the render
// ignores the cache. With use and replay the render plays the stored signal
// back; with use alone it stores the signal as it synthesizes it. master is
// the render's master chain from project.PrepareMaster, or nil; when it takes
// the cache's gain-point tap (project.SetMasterGainPointTap), the chain itself
// stores or replays the signal at its gain point, and the render leaves the
// master input alone (atPoint).
func (c *MasterCache) begin(key masterCacheKey, frames int64, master engine.StereoProcessor) (use, replay bool, err error) {
	if c == nil || c.file == nil || c.disabled {
		return false, false, nil
	}
	c.position = 0
	c.point = project.GainPointTap{Frames: c.point.Frames[:0]}
	c.atPoint = master != nil && project.SetMasterGainPointTap(master, &c.point)
	key.atPoint = c.atPoint
	if c.ready && c.key == key && c.frames == frames {
		if _, err := c.file.Seek(0, io.SeekStart); err != nil {
			c.release(master)
			return false, false, &masterCacheReadError{"rewind master cache", err}
		}
		c.point.Mode = project.GainPointReplay
		c.replays++
		return true, true, nil
	}
	c.key, c.frames, c.ready = key, frames, false
	if err := c.file.Truncate(0); err != nil {
		c.disabled = true
		c.release(master)
		return false, false, nil
	}
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		c.disabled = true
		c.release(master)
		return false, false, nil
	}
	c.point.Mode = project.GainPointCapture
	return true, false, nil
}

// release detaches the gain-point tap from a render that does not use the
// cache, so its master chain runs in full.
func (c *MasterCache) release(master engine.StereoProcessor) {
	if c.atPoint {
		project.SetMasterGainPointTap(master, nil)
	}
	c.atPoint, c.point.Mode = false, project.GainPointOff
}

// beginBlock prepares the next block: a replay loads its frames from the file
// into c.block, and a capture clears c.block for renderBlock to fill.
func (c *MasterCache) beginBlock(frames int, replay bool) error {
	c.block = c.block[:0]
	if !replay {
		if c.atPoint {
			c.point.Frames = c.block
			c.point.Rewind()
		}
		return nil
	}
	if c.position+int64(frames) > c.frames {
		return &masterCacheReadError{"read master cache", fmt.Errorf("it holds %d frames, the render asked for more", c.frames)}
	}
	if cap(c.scratch) < frames*8 {
		c.scratch = make([]byte, frames*8)
	}
	data := c.scratch[:frames*8]
	if _, err := io.ReadFull(c.file, data); err != nil {
		c.fail()
		return &masterCacheReadError{"read master cache", err}
	}
	if cap(c.block) < frames*2 {
		c.block = make([]float32, 0, frames*2)
	}
	c.block = c.block[:frames*2]
	for i := range c.block {
		c.block[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	if c.atPoint {
		c.point.Frames = c.block
		c.point.Rewind()
	}
	c.position += int64(frames)
	return nil
}

// endBlock writes the block that renderBlock captured. A failed write turns
// the cache off instead of failing the render.
func (c *MasterCache) endBlock() {
	if c.atPoint {
		// The chain appended the block's gain-point frames; an append that
		// grew the slice left c.block behind.
		c.block = c.point.Frames
	}
	if cap(c.scratch) < len(c.block)*4 {
		c.scratch = make([]byte, len(c.block)*4)
	}
	data := c.scratch[:len(c.block)*4]
	for i, sample := range c.block {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(sample))
	}
	if _, err := c.file.Write(data); err != nil {
		c.fail()
		return
	}
	c.position += int64(len(c.block) / 2)
}

// fail turns the cache off for the rest of its life and empties its file. A
// write fails mostly when the disk is full, and the render still has its own
// output to write there; the partial capture would keep the space that output
// needs. A read that fails leaves a stored signal that cannot be trusted, so it
// turns the cache off in the same way. Close removes the file. If it cannot be
// emptied, the cache is still off.
func (c *MasterCache) fail() {
	c.disabled, c.ready = true, false
	// A chain that still holds the tap stops storing; it runs in full.
	c.point.Mode = project.GainPointOff
	_ = c.file.Truncate(0)
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
