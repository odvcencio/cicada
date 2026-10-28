package instrumentpack

import (
	"path/filepath"
	"slices"
	"sync"
)

// packKey names a pack by what it is, not by who asked for it: the folder the
// manifest was opened in, the manifest's path there and the digest of the
// manifest's bytes. The manifest pins the hash of every asset, so packs with
// equal keys decode to equal PCM. The output rate is not part of the key: the
// PCM does not depend on it, only the check that the pack can play at it does.
type packKey struct{ root, manifest, digest string }

func (pk *Pack) key() packKey {
	root := pk.dir
	if abs, err := filepath.Abs(pk.dir); err == nil {
		root = abs
	}
	return packKey{root: root, manifest: pk.manifestPath, digest: pk.digest}
}

// sharedPack is one decoded pack in the process-wide cache. prepared is never
// changed after it is stored, and neither is the PCM it holds.
type sharedPack struct {
	prepared *Prepared
	bytes    int64
	used     uint64 // the cache clock at the last use; the least recent goes first

	mu       sync.Mutex
	admitted map[int]bool // output rates for which the pack passed Admit
}

type decodeFlight struct{ done chan struct{} }

var shared struct {
	mu      sync.Mutex
	packs   map[packKey]*sharedPack
	flights map[packKey]*decodeFlight
	bytes   int64
	clock   uint64
}

// LoadShared returns the pack decoded and admitted for the output rate. The
// first call in the process decodes the audio; later calls for the same pack
// (same folder, manifest path and manifest bytes) reuse that PCM, so a play
// reload, a Studio request or an audition does not hold another copy. The PCM
// is immutable; each caller gets its own Prepared, whose manifest configuration
// and zone table it may change.
//
// The cache keeps the packs it has decoded after the callers are done with
// them, so that the next compile finds them. It drops the least recently used
// pack when it holds more than retain bytes (zero means the default cap),
// except the pack just used. A caller that still holds a dropped pack keeps its
// PCM alive; the cache only forgets it. The cache trusts the manifest's pins:
// while the manifest's bytes are unchanged, an audio file changed on disk does
// not change the PCM that later loads return.
func (pk *Pack) LoadShared(rate int, retain int64) (*Prepared, error) {
	if retain <= 0 {
		retain = defaultMaxResidentPCM()
	}
	key := pk.key()
	for {
		shared.mu.Lock()
		if entry := shared.packs[key]; entry != nil {
			shared.clock++
			entry.used = shared.clock
			shared.mu.Unlock()
			return entry.share(rate)
		}
		if flight := shared.flights[key]; flight != nil {
			// Another goroutine is decoding this pack. Wait for it rather than
			// decode a second copy, then look again.
			shared.mu.Unlock()
			<-flight.done
			continue
		}
		flight := &decodeFlight{done: make(chan struct{})}
		if shared.flights == nil {
			shared.flights = map[packKey]*decodeFlight{}
		}
		shared.flights[key] = flight
		shared.mu.Unlock()

		prepared, err := pk.Decode(rate)

		shared.mu.Lock()
		delete(shared.flights, key)
		var entry *sharedPack
		if err == nil {
			entry = &sharedPack{prepared: prepared, bytes: prepared.ResidentPCMBytes(), admitted: map[int]bool{rate: true}}
			if shared.packs == nil {
				shared.packs = map[packKey]*sharedPack{}
			}
			shared.packs[key] = entry
			shared.bytes += entry.bytes
			shared.clock++
			entry.used = shared.clock
			evictLocked(retain, key)
		}
		shared.mu.Unlock()
		close(flight.done)
		if err != nil {
			return nil, err
		}
		return entry.share(rate)
	}
}

// share admits the pack for the rate if no earlier caller did, and returns a
// Prepared of the caller's own over the shared PCM.
func (e *sharedPack) share(rate int) (*Prepared, error) {
	e.mu.Lock()
	known := e.admitted[rate]
	e.mu.Unlock()
	if !known {
		if err := e.prepared.Admit(rate); err != nil {
			return nil, err
		}
		e.mu.Lock()
		e.admitted[rate] = true
		e.mu.Unlock()
	}
	return &Prepared{Manifest: e.prepared.Manifest, Zones: slices.Clone(e.prepared.Zones)}, nil
}

// evictLocked drops least recently used packs, never keep, until the cache
// holds at most retain bytes. The caller holds shared.mu.
func evictLocked(retain int64, keep packKey) {
	for shared.bytes > retain {
		var oldest packKey
		var found bool
		var oldestUsed uint64
		for key, entry := range shared.packs {
			if key != keep && (!found || entry.used < oldestUsed) {
				oldest, oldestUsed, found = key, entry.used, true
			}
		}
		if !found {
			return
		}
		shared.bytes -= shared.packs[oldest].bytes
		delete(shared.packs, oldest)
	}
}

// ReleaseShared drops every pack from the shared cache, for a test or a host
// that wants the memory back at once. Callers that still hold a pack keep it.
func ReleaseShared() {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	clear(shared.packs)
	shared.bytes = 0
}
