package sampleasset

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"m31labs.dev/cicada/audioasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/stream"
	"m31labs.dev/cicada/project"
)

func TestStreamWAVMatchesResidentDecodeWithGuards(t *testing.T) {
	for _, bits := range []int{16, 24, 32} {
		for _, channels := range []int{1, 2} {
			for _, encoding := range []int{1, 3} {
				if encoding == 3 && bits != 32 {
					continue
				}
				t.Run(fmt.Sprintf("%d/%d/%d", bits, channels, encoding), func(t *testing.T) {
					frames := stream.PageFrames*3 + 31
					data := testwav.Bytes(48000, channels, bits, frames, encoding)
					for i := 0; i < frames*channels; i++ {
						value := float32(i%251-125) / 256
						b := data[44+i*bits/8:]
						switch {
						case encoding == 3:
							binary.LittleEndian.PutUint32(b, math.Float32bits(value))
						case bits == 16:
							binary.LittleEndian.PutUint16(b, uint16(int16(value*32768)))
						case bits == 24:
							v := int32(value * 8388608)
							b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16)
						default:
							binary.LittleEndian.PutUint32(b, uint32(int32(float64(value)*2147483648)))
						}
					}
					// An ancillary chunk means PCM does not start at byte 44.
					data = append(data[:36:36], append([]byte{'J', 'U', 'N', 'K', 2, 0, 0, 0, 1, 2}, data[36:]...)...)
					binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
					dir := t.TempDir()
					path := filepath.Join(dir, "take.wav")
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					asset := project.Asset{Path: "take.wav", Format: "wav", SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Frames: int64(frames), RateHz: 48000, Channels: channels}
					resident, err := LoadRegion(dir, asset, 0, 0, 60, false)
					if err != nil {
						t.Fatal(err)
					}
					source, err := OpenStream(dir, asset)
					if err != nil {
						t.Fatal(err)
					}
					defer source.Close()
					cache, err := stream.New(stream.Config{Pages: 6, Readers: 1, AheadPages: 3}, []stream.Asset{source.Asset(1)})
					if err != nil {
						t.Fatal(err)
					}
					reader, _ := cache.NewReader(1)
					reader.SeekFrame(0, 1)
					for {
						work, ok := cache.Next()
						if !ok {
							break
						}
						if err := source.ReadFrames(context.Background(), work.Start, work.Left(), work.Right()); err != nil {
							t.Fatal(err)
						}
						work.Publish()
					}
					for frame := 0; frame < frames; frame++ {
						l, r, ok := reader.ReadFrame(int64(frame))
						wantRight := resident.Left[frame]
						if channels == 2 {
							wantRight = resident.Right[frame]
						}
						if !ok || l != resident.Left[frame] || r != wantRight {
							t.Fatalf("frame %d: %g/%g expected %g/%g", frame, l, r, resident.Left[frame], wantRight)
						}
					}
					reader.Stop()
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					var l, r [32]float32
					if err := source.ReadFrames(ctx, 0, l[:], r[:]); !errors.Is(err, context.Canceled) {
						t.Fatal("decode ignored cancellation", err)
					}
					asset.SHA256 = "bad"
					if bad, err := OpenStream(dir, asset); err == nil {
						bad.Close()
						t.Fatal("unverified stream admitted")
					}
				})
			}
		}
	}
}

type slowSource struct {
	started, cancelled, release chan struct{}
	reads                       atomic.Int64
}

func (s *slowSource) ReadFrames(ctx context.Context, first int64, left, right []float32) error {
	if s.reads.Add(1) == 1 {
		close(s.started)
		<-ctx.Done()
		close(s.cancelled)
		// Simulate a syscall which continues stalling after cancellation.
		<-s.release
	}
	for i := range left {
		left[i], right[i] = .25, -.25
	}
	return ctx.Err()
}

func streamFixture(t *testing.T) (*stream.Cache, *stream.Reader) {
	t.Helper()
	c, err := stream.New(stream.Config{Pages: 6, Readers: 1, AheadPages: 3}, []stream.Asset{{ID: 1, Frames: 48000 * 3600, RateHz: 48000, Channels: 2}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.NewReader(1)
	if err != nil {
		t.Fatal(err)
	}
	return c, r
}

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach simulation checkpoint")
	}
}

func TestSlowStorageSeekStormNeverBlocksRender(t *testing.T) {
	c, r := streamFixture(t)
	r.SeekFrame(0, 1)
	s := &slowSource{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	w := StartWorker(context.Background(), c, map[uint32]PageSource{1: s})
	defer w.Cancel()
	defer func() {
		select {
		case <-s.release:
		default:
			close(s.release)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		w.Cancel()
		if err := w.Wait(ctx); err != nil {
			t.Error(err)
		}
	}()
	waitSignal(t, s.started)
	var left, right [128]float32
	// Warm the runtime, then prove the callback still allocates nothing with a
	// worker holding a page and blocked in storage.
	if a := testing.AllocsPerRun(1000, func() { r.SeekFrame(90*stream.PageFrames, 1); r.Render(left[:], right[:]) }); a != 0 {
		t.Fatalf("stalled callback allocations=%g", a)
	}
	waitSignal(t, s.cancelled)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := int64(1); i <= 10000; i++ {
			r.SeekFrame((i*7919)%(48000*3600-4096), 1)
			r.Render(left[:], right[:])
		}
	}()
	waitSignal(t, done)
	if s.reads.Load() != 1 || c.Stats().Loaded != 0 || r.Stats().MissingFrames != 1001*128+10000*128 {
		t.Fatal("stalled work grew or transport stopped", s.reads.Load(), c.Stats(), r.Stats())
	}
	position := r.Position()
	close(s.release)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.WaitReady(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Render(left[:], right[:])
	if left[127] != .25 || right[127] != -.25 || r.Position() != position+128 || c.Stats().Cancelled != 1 {
		t.Fatal("latest seek did not recover", c.Stats(), r.Stats())
	}
	t.Logf("slow storage: seeks=10000 queued_reads=1 arena=%d missing_frames=%d recovered=%d", c.Stats().ArenaBytes, r.Stats().MissingFrames, r.Stats().Recoveries)
}

type constantSource struct{}

func (constantSource) ReadFrames(ctx context.Context, first int64, left, right []float32) error {
	for i := range left {
		left[i], right[i] = .25, -.25
	}
	return ctx.Err()
}

func TestConcurrentWorkerEvictionAndAudioSeeks(t *testing.T) {
	c, r := streamFixture(t)
	r.SeekFrame(0, 1)
	w := StartWorker(context.Background(), c, map[uint32]PageSource{1: constantSource{}})
	defer func() {
		w.Cancel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := w.Wait(ctx); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.WaitReady(ctx, r); err != nil {
		t.Fatal(err)
	}
	var left, right [128]float32
	for i := int64(1); i < 3000; i++ {
		r.SeekFrame((i*3571)%(48000*3600-4096), 1)
		r.Render(left[:], right[:])
		for j, l := range left {
			if l < 0 || l > .25 || right[j] != -l {
				t.Fatal("torn or non-finite PCM", l, right[j])
			}
		}
	}
	if err := w.WaitReady(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Render(left[:], right[:])
	if left[127] != .25 {
		t.Fatal("concurrent seek did not recover")
	}
	r.Stop()
}

type failingSource struct{ err error }

func (s failingSource) ReadFrames(context.Context, int64, []float32, []float32) error { return s.err }

func TestOfflineWaitFailsOnStorageErrorAndDeadline(t *testing.T) {
	c, r := streamFixture(t)
	r.SeekFrame(0, 1)
	failure := errors.New("storage unavailable")
	w := StartWorker(context.Background(), c, map[uint32]PageSource{1: failingSource{failure}})
	defer func() {
		w.Cancel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		w.Wait(ctx)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := w.WaitReady(ctx, r); !errors.Is(err, failure) {
		t.Fatal("offline operation did not fail explicitly", err)
	}
	if c.Stats().ReadErrors == 0 || c.Stats().Loaded != 0 {
		t.Fatal("failed PCM published", c.Stats())
	}
	w.Cancel()
	if err := w.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	c2, r2 := streamFixture(t)
	r2.SeekFrame(0, 1)
	w2 := StartWorker(context.Background(), c2, map[uint32]PageSource{1: constantSource{}})
	defer w2.Cancel()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := w2.WaitReady(cancelled, r2); !errors.Is(err, context.Canceled) {
		t.Fatal("offline deadline ignored", err)
	}
	w2.Cancel()
	w2.Wait(ctx)
}

func TestStreamRejectsNonFinitePCM(t *testing.T) {
	data := testwav.Bytes(48000, 2, 32, 16, 3)
	binary.LittleEndian.PutUint32(data[44:], math.Float32bits(float32(math.NaN())))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.wav"), data, 0600); err != nil {
		t.Fatal(err)
	}
	a := project.Asset{Path: "bad.wav", Format: "wav", SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Frames: 16, RateHz: 48000, Channels: 2}
	s, err := OpenStream(dir, a)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var left, right [16]float32
	if err := s.ReadFrames(context.Background(), 0, left[:], right[:]); err == nil {
		t.Fatal("non-finite PCM accepted")
	}
}

func TestHourLongSparseWAVStreamsBeyondResidentLimit(t *testing.T) {
	const frames = int64(48000 * 3600)
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "hour.wav"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header := testwav.Bytes(48000, 2, 32, 1, 3)[:44]
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+frames*8))
	binary.LittleEndian.PutUint32(header[40:44], uint32(frames*8))
	if _, err := f.Write(header); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(44 + frames*8); err != nil {
		t.Fatal(err)
	}
	var marker [8]byte
	binary.LittleEndian.PutUint32(marker[:4], math.Float32bits(.5))
	binary.LittleEndian.PutUint32(marker[4:], math.Float32bits(-.5))
	for _, frame := range []int64{0, frames / 4, frames / 2, frames - 1} {
		if _, err := f.WriteAt(marker[:], 44+frame*8); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	hash, err := audioasset.SHA256(f)
	if err != nil {
		t.Fatal(err)
	}
	a := project.Asset{Path: "hour.wav", Format: "wav", SHA256: hash, Frames: frames, RateHz: 48000, Channels: 2}
	if _, err := LoadRegion(dir, a, 0, 0, 60, false); err == nil || !strings.Contains(err.Error(), "resident frames") {
		t.Fatal("resident baseline unexpectedly admitted one hour", err)
	}
	source, err := OpenStream(dir, a)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	c, r := streamFixture(t)
	w := StartWorker(context.Background(), c, map[uint32]PageSource{1: source})
	defer func() {
		w.Cancel()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := w.Wait(ctx); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var left, right [128]float32
	for _, frame := range []int64{0, frames / 2, frames / 4, frames - 1} {
		r.SeekFrame(frame, 1)
		if err := w.WaitReady(ctx, r); err != nil {
			t.Fatal(err)
		}
		l, rr, ok := r.ReadFrame(frame)
		if !ok || l != .5 || rr != -.5 {
			t.Fatalf("hour WAV seek %d: %g/%g ready=%v", frame, l, rr, ok)
		}
		r.Render(left[:], right[:])
	}
	r.Stop()
	if c.Stats().ArenaBytes != 233472 || r.Stats().MissingFrames != 0 {
		t.Fatal(c.Stats(), r.Stats())
	}
	t.Logf("verified hour WAV: frames=%d PCM bytes=%d arena=%d resident rejected=true", frames, frames*8, c.Stats().ArenaBytes)
}
