package main

import "sync"

// samplerMailbox retains only the newest asynchronous result while one queue
// notification is outstanding. Values may include decoded buffers; events and
// DOM nodes must never be stored here.
type samplerMailbox[T any] struct {
	mu       sync.Mutex
	value    T
	pending  bool
	notified bool
	epoch    uint64
}

// put replaces an older result and reports whether a notifier must be started.
func (m *samplerMailbox[T]) put(value T) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.value = value
	m.pending = true
	if m.notified {
		return m.epoch, false
	}
	m.notified = true
	m.epoch++
	return m.epoch, true
}

func (m *samplerMailbox[T]) current(epoch uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending && m.notified && m.epoch == epoch
}

func (m *samplerMailbox[T]) peek() (T, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.value, m.pending
}

// renotify invalidates a discarded queued notification while preserving its
// result. Any previous retry worker observes its old epoch and exits.
func (m *samplerMailbox[T]) renotify() (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.pending {
		return 0, false
	}
	m.epoch++
	m.notified = true
	return m.epoch, true
}

func (m *samplerMailbox[T]) take() (T, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.value, m.pending
	var zero T
	m.value = zero
	m.pending = false
	m.notified = false
	m.epoch++
	return value, ok
}

func (m *samplerMailbox[T]) clear() {
	m.mu.Lock()
	var zero T
	m.value = zero
	m.pending = false
	m.notified = false
	m.epoch++
	m.mu.Unlock()
}
