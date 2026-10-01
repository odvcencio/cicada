// Package stream shares a fixed PCM arena between one asset worker and bounded
// audio readers. Only the worker writes pages; readers pin immutable ready pages.
package stream

import "sync/atomic"

const (
	PageFrames = 4096
	// Guards cover half of the sample voice's largest 768-tap SRC kernel.
	GuardFrames = 384
	PageBytes   = (PageFrames + 2*GuardFrames) * 2 * 4
	MaxReaders  = 64
	empty       = uint32(0)
	writing     = uint32(1)
	ready       = uint32(2)
	pin         = uint32(4)
)

type Error string

func (e Error) Error() string { return string(e) }

// Asset IDs identify immutable contents for the lifetime of a cache. A new
// revision must use a new ID, even when the path and dimensions are unchanged.
type Asset struct {
	ID               uint32
	Frames           int64
	RateHz, Channels int
}

type Config struct {
	Pages, Readers int
	// AheadPages is a source-frame horizon; use ceil(rate*seconds*ratio/4096)
	// for pitched playback. Three pages cover 250 ms at unity 48 kHz.
	AheadPages int
}

type page struct {
	state       atomic.Uint32
	asset       uint32
	index       int64
	used        uint64
	left, right [PageFrames + 2*GuardFrames]float32
}

type intent struct {
	sequence           atomic.Uint64
	asset              atomic.Uint32
	current, low, high atomic.Int64
	direction          atomic.Int32
}

type window struct {
	asset              uint32
	current, low, high int64
	direction          int
}

func (i *intent) snapshot() (window, bool) {
	before := i.sequence.Load()
	if before == 0 || before&1 != 0 {
		return window{}, false
	}
	w := window{i.asset.Load(), i.current.Load(), i.low.Load(), i.high.Load(), int(i.direction.Load())}
	return w, i.sequence.Load() == before
}

type Cache struct {
	config                             Config
	assets                             []Asset
	pages                              []page
	readers                            []Reader
	readerCount                        int // registration finishes before worker/render start
	nextReader                         int
	clock                              uint64 // worker-owned
	loaded, evicted, cancelled, errors atomic.Uint64
}

// New allocates every page and reader before playback. Admission includes each
// reader's entire window plus one old pinned page during a window change.
func New(config Config, assets []Asset) (*Cache, error) {
	if config.Readers < 1 || config.Readers > MaxReaders || config.AheadPages < 0 || config.AheadPages > 64 ||
		config.Pages < config.Readers*(config.AheadPages+3) || config.Pages > 65536 || len(assets) == 0 {
		return nil, Error("stream cache dimensions exceed admission limits")
	}
	for i, a := range assets {
		if a.ID == 0 || a.Frames < 1 || a.Frames > 1<<52 || a.RateHz < 8000 || a.RateHz > 192000 || a.Channels < 1 || a.Channels > 2 {
			return nil, Error("invalid streamed asset dimensions")
		}
		for _, prior := range assets[:i] {
			if prior.ID == a.ID {
				return nil, Error("duplicate streamed asset ID")
			}
		}
	}
	return &Cache{config: config, assets: append([]Asset(nil), assets...), pages: make([]page, config.Pages), readers: make([]Reader, config.Readers)}, nil
}

// NewReader registers a single audio owner's cursor. Call before starting the
// worker. Distinct voices need distinct readers; a reader is never shared.
func (c *Cache) NewReader(id uint32) (*Reader, error) {
	if c.readerCount == len(c.readers) {
		return nil, Error("stream reader admission limit reached")
	}
	for _, a := range c.assets {
		if a.ID == id {
			r := &c.readers[c.readerCount]
			c.readerCount++
			r.cache, r.asset, r.direction, r.fadeFrames, r.ended = c, a, 1, a.RateHz/500, true
			return r, nil
		}
	}
	return nil, Error("unknown streamed asset ID")
}

type Stats struct {
	ArenaBytes                             int64
	Loaded, Evicted, Cancelled, ReadErrors uint64
}

func (c *Cache) Stats() Stats {
	return Stats{int64(len(c.pages)) * PageBytes, c.loaded.Load(), c.evicted.Load(), c.cancelled.Load(), c.errors.Load()}
}

// Work holds exclusive ownership of a page until Publish, Cancel or Fail. Only
// one worker may call Next and complete work; audio owners never call them.
type Work struct {
	cache          *Cache
	page           *page
	Asset          Asset
	Index          int64
	Start          int64
	Offset, Frames int
}

func (w Work) Left() []float32  { return w.page.left[w.Offset : w.Offset+w.Frames] }
func (w Work) Right() []float32 { return w.page.right[w.Offset : w.Offset+w.Frames] }

// Wanted reads bounded mailboxes without waiting for an audio owner mid-write.
func (w Work) Wanted() bool { return w.cache.Wanted(w.Asset.ID, w.Index) }

// Wanted reports demand to workers/control callers. An incomplete mailbox
// conservatively retains demand until the audio owner finishes rewriting it.
func (c *Cache) Wanted(asset uint32, index int64) bool {
	for i := 0; i < c.readerCount; i++ {
		w, ok := c.readers[i].intent.snapshot()
		// A mailbox being rewritten is uncertain demand, not obsolete demand.
		// Defer cancellation/eviction to the next worker iteration.
		if !ok && c.readers[i].intent.sequence.Load() != 0 {
			return true
		}
		if ok && w.asset == asset && index >= w.low && index <= w.high {
			return true
		}
	}
	return false
}

func (w Work) Publish() bool {
	if !w.Wanted() {
		w.Cancel()
		return false
	}
	w.cache.clock++
	w.page.used = w.cache.clock
	w.page.state.Store(ready)
	w.cache.loaded.Add(1)
	return true
}
func (w Work) Cancel() { w.page.state.Store(empty); w.cache.cancelled.Add(1) }
func (w Work) Fail()   { w.page.state.Store(empty); w.cache.errors.Add(1) }

func (c *Cache) present(asset uint32, index int64) bool {
	for i := range c.pages {
		p := &c.pages[i]
		if p.state.Load() != empty && p.asset == asset && p.index == index {
			return true
		}
	}
	return false
}

func (c *Cache) reserve(a Asset, index int64) (Work, bool) {
	var victim *page
	for i := range c.pages {
		p := &c.pages[i]
		state := p.state.Load()
		if state == empty {
			victim = p
			break
		}
		if state == ready && !c.Wanted(p.asset, p.index) && (victim == nil || p.used < victim.used) {
			victim = p
		}
	}
	if victim == nil {
		return Work{}, false
	}
	state := victim.state.Load()
	if (state != empty && state != ready) || !victim.state.CompareAndSwap(state, writing) {
		return Work{}, false
	}
	if state == ready {
		c.evicted.Add(1)
	}
	victim.asset, victim.index = a.ID, index
	// Clear both guards, including the clipped first/last page and mono right.
	clear(victim.left[:])
	clear(victim.right[:])
	first := index*PageFrames - GuardFrames
	start := max(int64(0), first)
	count := min(a.Frames, (index+1)*PageFrames+GuardFrames) - start
	return Work{c, victim, a, index, start, int(start - first), int(count)}, true
}

// Next coalesces seek storms into the latest window instead of queuing every
// seek. Current pages take precedence over prefetch; readers rotate fairly.
func (c *Cache) Next() (Work, bool) {
	return c.NextEligible(nil)
}

// NextEligible skips pages whose reads the worker has deferred, allowing other
// current pages and prefetch to progress. The predicate runs only on the worker
// and must not change cache ownership. A nil predicate admits every page.
func (c *Cache) NextEligible(eligible func(asset uint32, index int64) bool) (Work, bool) {
	for distance := 0; distance <= c.config.AheadPages+1; distance++ {
		for n := 0; n < c.readerCount; n++ {
			i := (c.nextReader + n) % c.readerCount
			r := &c.readers[i]
			w, ok := r.intent.snapshot()
			if !ok {
				continue
			}
			index := w.current + int64(distance*w.direction)
			if distance == c.config.AheadPages+1 {
				index = w.current - int64(w.direction)
			}
			if index < w.low || index > w.high || c.present(w.asset, index) {
				continue
			}
			if eligible != nil && !eligible(w.asset, index) {
				continue
			}
			if work, ok := c.reserve(r.asset, index); ok {
				c.nextReader = (i + 1) % c.readerCount
				return work, true
			}
		}
	}
	return Work{}, false
}

// Ready checks immutable publication for workers/control callers without
// retaining a page pin or exposing PCM.
func (c *Cache) Ready(asset uint32, index int64) bool {
	p := c.acquire(asset, index)
	if p == nil {
		return false
	}
	p.state.Add(^uint32(pin - 1))
	return true
}

func (c *Cache) acquire(asset uint32, index int64) *page {
	for i := range c.pages {
		p := &c.pages[i]
		state := p.state.Load()
		// One CAS per slot: contention becomes a counted miss, never a spin.
		if state < ready || !p.state.CompareAndSwap(state, state+pin) {
			continue
		}
		if p.asset == asset && p.index == index {
			return p
		}
		p.state.Add(^uint32(pin - 1))
	}
	return nil
}
