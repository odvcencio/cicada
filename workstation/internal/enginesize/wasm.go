package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// wasmSection is one top-level section of a WebAssembly file. Bytes counts
// the id, the size prefix and the payload, so the sections sum to the file.
type wasmSection struct {
	ID    byte   `json:"id"`
	Name  string `json:"name"`
	Bytes int    `json:"bytes"`
}

// wasmFunction is one function body in the code section. Bytes includes its
// size prefix, so the bodies and the count prefix sum to the section payload.
type wasmFunction struct {
	Name  string
	Bytes int
}

// wasmSegment is one active data segment: its memory address and contents.
// Overhead is the encoded bytes around the contents (flags, offset, size).
type wasmSegment struct {
	Address  uint32
	Data     []byte
	Overhead int
}

type wasmModule struct {
	Size      int
	Sections  []wasmSection
	Functions []wasmFunction
	Segments  []wasmSegment
	// CodeOverhead is the code section's header and count prefix.
	CodeOverhead int
	// DataOverhead is the data section's header and count prefix.
	DataOverhead int
}

var sectionNames = map[byte]string{0: "custom", 1: "type", 2: "import", 3: "function", 4: "table", 5: "memory", 6: "global", 7: "export", 8: "start", 9: "element", 10: "code", 11: "data", 12: "datacount"}

type reader struct {
	data []byte
	pos  int
}

func (r *reader) byte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, errors.New("unexpected end of data")
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}

func (r *reader) uleb() (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("LEB128 value too long")
}

func (r *reader) sleb32() (int32, error) {
	var value int64
	var shift uint
	for {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		value |= int64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			if shift < 64 && b&0x40 != 0 {
				value |= -1 << shift
			}
			return int32(value), nil
		}
		if shift >= 35 {
			return 0, errors.New("signed LEB128 value too long")
		}
	}
}

func (r *reader) bytes(n uint64) ([]byte, error) {
	if n > uint64(len(r.data)-r.pos) {
		return nil, errors.New("length past end of data")
	}
	out := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *reader) name() (string, error) {
	n, err := r.uleb()
	if err != nil {
		return "", err
	}
	b, err := r.bytes(n)
	return string(b), err
}

// parseWasm reads the sections, function bodies, function names and data
// segments of a Go js/wasm file. Names come from the "name" custom section,
// which the Go linker writes unless -s is given.
func parseWasm(data []byte) (*wasmModule, error) {
	if len(data) < 8 || !bytes.Equal(data[:4], []byte("\x00asm")) || binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return nil, errors.New("not a version 1 WebAssembly file")
	}
	module := &wasmModule{Size: len(data)}
	r := &reader{data: data, pos: 8}
	module.Sections = append(module.Sections, wasmSection{Name: "header", Bytes: 8})
	importedFunctions := 0
	names := map[int]string{}
	var bodies []int
	for r.pos < len(data) {
		start := r.pos
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		size, err := r.uleb()
		if err != nil {
			return nil, err
		}
		payload, err := r.bytes(size)
		if err != nil {
			return nil, fmt.Errorf("section %d: %w", id, err)
		}
		name := sectionNames[id]
		s := &reader{data: payload}
		switch id {
		case 0:
			custom, err := s.name()
			if err != nil {
				return nil, err
			}
			name = "custom:" + custom
			if custom == "name" {
				if err := readNames(s, names); err != nil {
					return nil, fmt.Errorf("name section: %w", err)
				}
			}
		case 2:
			if importedFunctions, err = countFunctionImports(s); err != nil {
				return nil, fmt.Errorf("import section: %w", err)
			}
		case 10:
			count, err := s.uleb()
			if err != nil {
				return nil, err
			}
			module.CodeOverhead = (r.pos - start) - len(payload) + s.pos
			for range count {
				before := s.pos
				n, err := s.uleb()
				if err != nil {
					return nil, err
				}
				if _, err := s.bytes(n); err != nil {
					return nil, err
				}
				bodies = append(bodies, s.pos-before)
			}
		case 11:
			count, err := s.uleb()
			if err != nil {
				return nil, err
			}
			module.DataOverhead = (r.pos - start) - len(payload) + s.pos
			for range count {
				before := s.pos
				flags, err := s.uleb()
				if err != nil {
					return nil, err
				}
				if flags != 0 {
					return nil, fmt.Errorf("data segment flags %d: only active memory-0 segments are expected from the Go linker", flags)
				}
				if op, err := s.byte(); err != nil || op != 0x41 {
					return nil, errors.New("data segment offset is not i32.const")
				}
				address, err := s.sleb32()
				if err != nil {
					return nil, err
				}
				if end, err := s.byte(); err != nil || end != 0x0b {
					return nil, errors.New("data segment offset expression is not terminated")
				}
				n, err := s.uleb()
				if err != nil {
					return nil, err
				}
				contents, err := s.bytes(n)
				if err != nil {
					return nil, err
				}
				module.Segments = append(module.Segments, wasmSegment{Address: uint32(address), Data: contents, Overhead: s.pos - before - len(contents)})
			}
		}
		module.Sections = append(module.Sections, wasmSection{ID: id, Name: name, Bytes: r.pos - start})
	}
	for i, n := range bodies {
		module.Functions = append(module.Functions, wasmFunction{Name: names[importedFunctions+i], Bytes: n})
	}
	return module, nil
}

func countFunctionImports(r *reader) (int, error) {
	count, err := r.uleb()
	if err != nil {
		return 0, err
	}
	functions := 0
	for range count {
		if _, err := r.name(); err != nil {
			return 0, err
		}
		if _, err := r.name(); err != nil {
			return 0, err
		}
		kind, err := r.byte()
		if err != nil {
			return 0, err
		}
		switch kind {
		case 0: // function: type index
			functions++
			_, err = r.uleb()
		case 1: // table: reference type and limits
			if _, err = r.byte(); err == nil {
				err = skipLimits(r)
			}
		case 2: // memory: limits
			err = skipLimits(r)
		case 3: // global: value type and mutability
			if _, err = r.byte(); err == nil {
				_, err = r.byte()
			}
		default:
			err = fmt.Errorf("import kind %d", kind)
		}
		if err != nil {
			return 0, err
		}
	}
	return functions, nil
}

func skipLimits(r *reader) error {
	flags, err := r.uleb()
	if err != nil {
		return err
	}
	if _, err := r.uleb(); err != nil {
		return err
	}
	if flags&1 != 0 {
		_, err = r.uleb()
	}
	return err
}

func readNames(r *reader, names map[int]string) error {
	for r.pos < len(r.data) {
		id, err := r.byte()
		if err != nil {
			return err
		}
		size, err := r.uleb()
		if err != nil {
			return err
		}
		payload, err := r.bytes(size)
		if err != nil {
			return err
		}
		if id != 1 { // only function names
			continue
		}
		s := &reader{data: payload}
		count, err := s.uleb()
		if err != nil {
			return err
		}
		for range count {
			index, err := s.uleb()
			if err != nil {
				return err
			}
			name, err := s.name()
			if err != nil {
				return err
			}
			names[int(index)] = name
		}
	}
	return nil
}

// memory reads little-endian values at linear-memory addresses from the data
// segments. Addresses no segment covers read as zero, as at run time.
type memory struct{ segments []wasmSegment }

func (m memory) read(address uint64, n int) []byte {
	out := make([]byte, n)
	for _, s := range m.segments {
		start, end := uint64(s.Address), uint64(s.Address)+uint64(len(s.Data))
		if address+uint64(n) <= start || address >= end {
			continue
		}
		for i := range n {
			a := address + uint64(i)
			if a >= start && a < end {
				out[i] = s.Data[a-start]
			}
		}
	}
	return out
}

func (m memory) u64(address uint64) uint64 {
	return binary.LittleEndian.Uint64(m.read(address, 8))
}

// find returns the first address whose bytes equal pattern.
func (m memory) find(pattern []byte, from uint64) (uint64, bool) {
	for _, s := range m.segments {
		if uint64(s.Address)+uint64(len(s.Data)) <= from {
			continue
		}
		offset := 0
		if from > uint64(s.Address) {
			offset = int(from - uint64(s.Address))
		}
		if i := bytes.Index(s.Data[offset:], pattern); i >= 0 {
			return uint64(s.Address) + uint64(offset+i), true
		}
	}
	return 0, false
}
