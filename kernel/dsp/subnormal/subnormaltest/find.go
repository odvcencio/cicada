// Package subnormaltest finds subnormal numbers in a processor's state, for
// tests that check that decaying state never stalls in them.
package subnormaltest

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"testing"
)

// Find walks v (a pointer to a processor, usually) through structs, arrays,
// slices and pointers, including unexported fields, and returns the path of
// every float32 or float64 that holds a subnormal number, up to limit paths.
func Find(v any, limit int) []string {
	var found []string
	seen := map[uintptr]bool{}
	var walk func(value reflect.Value, path string)
	walk = func(value reflect.Value, path string) {
		if len(found) >= limit {
			return
		}
		switch value.Kind() {
		case reflect.Float32:
			bits := math.Float32bits(float32(value.Float()))
			if bits&0x7f800000 == 0 && bits&0x7fffffff != 0 {
				found = append(found, path)
			}
		case reflect.Float64:
			bits := math.Float64bits(value.Float())
			if bits&0x7ff0000000000000 == 0 && bits&0x7fffffffffffffff != 0 {
				found = append(found, path)
			}
		case reflect.Pointer:
			if value.IsNil() || seen[value.Pointer()] {
				return
			}
			seen[value.Pointer()] = true
			walk(value.Elem(), path)
		case reflect.Struct:
			for i := range value.NumField() {
				walk(value.Field(i), path+"."+value.Type().Field(i).Name)
			}
		case reflect.Array, reflect.Slice:
			for i := range value.Len() {
				walk(value.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(reflect.ValueOf(v), "")
	return found
}

// Hash is FNV-1a over the float32 bits of a stereo output, with one sum per
// second as well, so two architectures can compare a long render and find the
// first second where they differ.
type Hash struct {
	all, second uint64
	frames      int
	rate        int
	Seconds     []uint64
}

// NewHash starts a hash of output at rate frames per second.
func NewHash(rate int) *Hash {
	return &Hash{all: fnvOffset, second: fnvOffset, rate: rate}
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Add hashes one stereo frame.
func (h *Hash) Add(left, right float32) {
	for _, value := range [2]float32{left, right} {
		bits := math.Float32bits(value)
		for range 4 {
			h.all = (h.all ^ uint64(byte(bits))) * fnvPrime
			h.second = (h.second ^ uint64(byte(bits))) * fnvPrime
			bits >>= 8
		}
	}
	h.frames++
	if h.frames%h.rate == 0 {
		h.Seconds = append(h.Seconds, h.second)
		h.second = fnvOffset
	}
}

// Sum is the hash of every frame added.
func (h *Hash) Sum() uint64 { return h.all }

// Report formats the hash and the per-second hashes for a test log.
func (h *Hash) Report() string {
	text := fmt.Sprintf("sum %#016x, %d frames; per second:", h.all, h.frames)
	for _, second := range h.Seconds {
		text += fmt.Sprintf(" %016x", second)
	}
	return text
}

// CheckPin fails the test when h does not match pinned, a hash that amd64 and
// arm64 produced alike. With CICADA_DECAY_HASHES=1 it logs the hashes instead,
// which is how a pin is made: run the test on both architectures, compare the
// logged sums, and pin a sum only when they agree. A pinned value of zero
// means not pinned; the test then only logs.
func CheckPin(t testing.TB, name string, h *Hash, pinned uint64) {
	t.Helper()
	if os.Getenv("CICADA_DECAY_HASHES") == "1" || pinned == 0 {
		t.Logf("DECAY-HASH %s %s", name, h.Report())
		return
	}
	if h.Sum() != pinned {
		t.Fatalf("%s output changed: %#016x, pinned %#016x (rerun with CICADA_DECAY_HASHES=1 for the per-second hashes)", name, h.Sum(), pinned)
	}
}
