package sampleasset

import (
	"context"
	"fmt"
	"sync"
	"time"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/stream"
	"m31labs.dev/cicada/project"
)

// StreamPlan holds verified asset declarations, with no files or goroutines.
// Every engine receives its own readers, cache, worker and pinned file handles.
type StreamPlan struct {
	root   string
	assets []project.Asset
	ids    []uint32
}

func PrepareStreamPlan(root string, assets []project.Asset, ids []uint32) (*StreamPlan, error) {
	if len(assets) == 0 || len(assets) != len(ids) {
		return nil, fmt.Errorf("invalid streamed asset plan")
	}
	for i, asset := range assets {
		if ids[i] == 0 {
			return nil, fmt.Errorf("invalid streamed asset ID")
		}
		source, err := OpenStream(root, asset)
		if err != nil {
			return nil, err
		}
		if err = source.Close(); err != nil {
			return nil, err
		}
	}
	return &StreamPlan{root: root, assets: append([]project.Asset(nil), assets...), ids: append([]uint32(nil), ids...)}, nil
}
func (p *StreamPlan) New(readers, outputRate, maxBlock int) (engine.StreamingRuntime, error) {
	runtime := &streamRuntime{}
	var assets []stream.Asset
	sources := map[uint32]PageSource{}
	maxRate := 0
	for i, asset := range p.assets {
		source, err := OpenStream(p.root, asset)
		if err != nil {
			runtime.Close()
			return nil, err
		}
		runtime.sources = append(runtime.sources, source)
		assets = append(assets, source.Asset(p.ids[i]))
		sources[p.ids[i]] = source
		maxRate = max(maxRate, asset.RateHz)
	}
	// A quarter second of source frames plus enough for the largest admitted
	// callback. PCM arena admission remains bounded independently of song length.
	ahead := int((max(int64(maxRate)/4, (int64(maxBlock)*int64(maxRate)+int64(outputRate)-1)/int64(outputRate)) + stream.PageFrames - 1) / stream.PageFrames)
	cache, err := stream.New(stream.Config{Pages: readers * (ahead + 3), Readers: readers, AheadPages: ahead}, assets)
	if err != nil {
		runtime.Close()
		return nil, err
	}
	runtime.cache = cache
	for range readers {
		reader, err := cache.NewReader(assets[0].ID)
		if err != nil {
			runtime.Close()
			return nil, err
		}
		reader.Stop()
		runtime.readers = append(runtime.readers, reader)
	}
	runtime.worker = startOwnedWorker(context.Background(), cache, sources, runtime.closeSources)
	return runtime, nil
}

type streamRuntime struct {
	cache      *stream.Cache
	readers    []*stream.Reader
	sources    []*WAVSource
	worker     *Worker
	sourceOnce sync.Once
	closeOnce  sync.Once
	closeMu    sync.Mutex
	closeError error
}

func (r *streamRuntime) Reader(i int) *stream.Reader { return r.readers[i] }
func (r *streamRuntime) WaitReady(ctx context.Context, reader *stream.Reader) error {
	return r.worker.WaitReady(ctx, reader)
}
func (r *streamRuntime) Stats() stream.Stats { return r.cache.Stats() }
func (r *streamRuntime) LastError() error {
	if r.worker == nil {
		return nil
	}
	return r.worker.LastError()
}
func (r *streamRuntime) Retire() {
	if r.worker != nil {
		r.worker.Retire()
	}
}
func (r *streamRuntime) closeSources() {
	r.sourceOnce.Do(func() {
		for _, source := range r.sources {
			if err := source.Close(); err != nil {
				r.setCloseError(err)
			}
		}
	})
}
func (r *streamRuntime) Close() error {
	r.closeOnce.Do(func() {
		if r.worker != nil {
			r.worker.Cancel()
		}
		if r.worker == nil {
			r.closeSources()
		}
		if r.worker != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.worker.Wait(ctx); err != nil {
				r.setCloseError(err)
			}
		}
	})
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	return r.closeError
}

func (r *streamRuntime) setCloseError(err error) {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	if r.closeError == nil {
		r.closeError = err
	}
}
